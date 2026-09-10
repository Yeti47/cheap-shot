# Port-20190 protocol (XC Things `pprpc` / `avsdk`)

Everything here was recovered by static analysis of `firmware/logical/flash_logical.bin`
(the de-CRC'd dump — see `tools/decrc.py`), disassembled as `ARM:LE:32:v5t` at base
`0x0`. Offsets are logical addresses, which equal file offsets. It is cross-checked
against two independent public reversing efforts of the same protocol family
(`IvanFogel/ino-a9-local-bridge`, `fusetim/insecurity-camera-tools`); where a value
was verified in *our* binary it is marked ✓ with the address it came from.

**Headline: the camera can be driven entirely from the LAN with no vendor app and no
cloud account.** Both secrets needed — the LanAuth password and the RPC AES key — derive
from values that live on the device (in flash / provisioning config), which the firmware
reads locally. Nothing is issued by the Tuya/XC cloud at connection time.

## Transport

- Single TCP service on **port 20190** ✓ — the value `0x4EDE` appears 6× as an ARM
  literal-pool constant (`0x12bc8, 0x2947c, 0x36bdc, 0x36efc, 0x3c9c8, 0x42318`); the
  firmware has no other configurable listen port.
- There is a matching **UDP 20190** discovery/localsrv path: `iot_dev_localsrv_udp_start`,
  `pprpc_udpsrv_create`, `iot_dev_broadcast_discovery` (class code `IPAV`).
- Under pprpc sits an **ikcp (KCP)** reliability layer over UDP for the P2P/relay case
  (`ikcp_nodelay/wndsize/setmtu`, `%s.kcp`, `0x1391ac…`), but a pure-LAN client can use
  the plain TCP mode: the firmware has an explicit `AVSDK_CONN_ONLY_TCP, NO P2P!` branch
  and logs `iot conn local try: tcp://%s:%d` (`0x133a58`).
- No HTTP or RTSP anywhere in the firmware (`HTTP/1.`/`rtsp` = 0 hits). The vendor's
  own binary framing is the only interface. (This corrects the earlier "MJPEG tunneled
  over HTTP through 20190" guess.)

## Frame framing (from `IvanFogel/ino-a9-local-bridge`, consistent with our symbols)

    byte 0 : high nibble = message type, low nibble = flags
    then   : protobuf varint = payload length
    UDP variant prepends the two ASCII bytes "Qp" (0x51 0x70)

    type 3 = heartbeat   4 = protobuf RPC   5 = JSON RPC
    type 6 = audio/video 7 = custom         8 = file

`pprpc_dump_packet` (`0x4d450`) logs each header as `%s,ID:%d-SEQ:%d-RPC:%d` (`0x13a264`)
— the `<cmd id>`, `<sequence>`, `<rpc type>` triple that also feeds the AES key below.

## RPC command IDs (control sequence to start a stream)

The firmware carries a **122-entry command-name table** at logical
`0x5a540`–`0x5a724` (pointers into the name strings at `0x13cd…`): `PPMQPublish`,
`NatTest1`, …, `SyncConn`, `ConnHB`, …, `Discovery`, `WifiAPGet`, `WifiSet`,
`WifiGet`, `VideoPlay`, `VideoPause`, `VideoQosSet`, `FlipSet`, `AudioPlay`, …
It is a **plain string table indexed by an enum**, with *no* parallel ID array —
the id↔name mapping is a compiled switch (its case constants sit in the literal
pools at `0x5b464` and `0x5c2b0`), so IDs cannot simply be read off the table.
Names alone confirm the roles of the live-validated IDs:

    2650  LanAuth        -> returns a fresh 32-hex session_key
     106  SyncConn       -> connection sync
     107  ConnHB         -> time-sync / heartbeat, sent BY the camera
    2610  VideoPlay      -> starts the MJPEG stream (type-6 frames)
    2614  AudioPlay      -> G.711 A-law, 8 kHz

⚠️ **`Discovery`'s ID is still unknown.** The table's ordering is consecutive in
places (`VideoPlay 2610`, `VideoPause 2611`, `VideoQosSet 2612`, `FlipSet 2613`,
`AudioPlay 2614` matches its slot order exactly) which would put `Discovery` at
**2606**, but the same extrapolation predicts `LanAuth 2646` where the real value
is 2650 — so the ordering breaks somewhere and 2606 is only a hypothesis. The
bridge therefore treats UDP discovery as experimental (`--discovery-cmd`
overrides the constant) and relies on a slow TCP probe of port 20190 instead.

**The camera's own heartbeat gates the stream.** After `SyncConn` the camera
sends two unsolicited command-107 *requests*; at least one must be answered
before `VideoPlay` will produce frames. The reply carries field 1 = the
request's field 1 echoed back, field 2 = `-30` (as a two's complement int64
varint, not zigzag), field 3 = the client's wall-clock time in milliseconds.

Request payloads: `LanAuth` = field 2 `user` (string), field 3 `pwd` (string);
`SyncConn` = field 1 `1`; `VideoPlay` = field 2 `QoS`; `AudioPlay` = empty.
`LanAuth`'s response carries the session key in field 1.

The camera clamps video to **640×480 MJPEG (format 4) @ ~10 fps**, QoS 5.

## ✅ LanAuth SOLVED (2026-09-10) — the secret is `deckey(lslat)`, not `scode`

The camera authenticated the bridge over pure LAN (`cmd=2650 rpc=1 code=0`, valid
32-hex session key). The long-standing block was a single wrong assumption: the
auth's **second** MD5 input is *not* the plaintext `product_secret`.
It is the config field **`lslat`**, AES-decrypted at config-load time by
`iot_cfg_deckey` (`0x374b8`), invoked from `iot_dev_cfg_loadmem` (`0x379c4`).

Field mapping in the runtime sub-struct (`*(*0x4032c4)[0x94]`): getterA `+0x10` =
config key **`did`** (plain); getterB `+0x120` = config key **`lslat`** run through
`deckey`. The auth builder (`0x2bf50`) hashes `getterA - getterB - idx`.

**`deckey(did, value)`** (all constants hardcoded in the firmware):

    ciphertext = base64_decode(value)
    key        = MD5_hex(did + "HL4viXBiGEz8mCBkuhkTQFaK")   # 32 hex chars = AES-256 key
    iv         = "e7uJ6Q8uM7ikpUxf"
    secret     = AES-256-CBC-decrypt(ciphertext, key, iv), PKCS#7 stripped

    password   = "$L0$" + MD5_hex(did + "-" + secret + "-0")

`0x7f0f4` is the inverse (`enckey`, hex-decode + AES-encrypt); it produces the
stored `lslat` from the plaintext. Verified by round-trip against the device's
own values. `signkey` = `enckey(device_name)` likewise; both are the factory
record's `DEVICE_NAME`/`DEVICE_SECRET` (hex), the config storing the encrypted
form and decrypting it into the auth secret at boot.

**Live-confirmed on our unit** (device-unique values redacted — `did`, `lslat`,
`deckey(lslat)` and the resulting password live only in the gitignored config and
`firmware/logical/factory_config.json`; `did` also appears in the setup-AP SSID).
The bridge (`src/`) implements
`camera.Deckey`; the config takes `did` + `lslat`. End-to-end: LanAuth →
`VideoPlay` → 640×480 MJPEG at ~10 fps, served over HTTP and to `/dev/video*`.

## LanAuth — the connection gate  ✓ fully recovered

Handled by `iot_conn_local_on_packet`; the password is built by `local_check_auth1`
(**`0x2bf50`**). Recovered exactly:

    password = "$L" + idx + "$" + MD5_hex( did + "-" + scode + "-" + idx )

- Format strings: `"%s-%s-%d"` (`0x133668`) for the MD5 input, `"$L%d$%s"` (`0x133674`)
  for the result — matching the `$L…` form the public bridge observed. ✓
- `did`  = accessor `0x37de4`, config-struct field **+0xA4** ✓
- `scode`= accessor `0x37e48`, config-struct field **+0x1B4** ✓
- Both are **device-local and recoverable straight from the dump.** Confirmed live
  against the boot-time `[iot]` config print (`ut_xciot.c:640`), which maps them onto
  the factory JSON at physical flash `0x1F4000`
  (`firmware/logical/factory_config.json`, gitignored):

  | firmware value | source (factory JSON key) |
  |----------------|---------------------------|
  | `did`     | **`PRODUCT_KEY`** (verbatim, e.g. `PP00A90AC5645E…`) |
  | `scode`   | **`PRODUCT_SECRET`** (verbatim, a short numeric code) |
  | `signkey` | base64 of the `DEVICE_NAME` bytes |
  | `lslat`   | base64 of the `DEVICE_SECRET` bytes |

  So the LanAuth password needs **no live read and no cloud** — `did` and `scode` are
  both in the on-flash provisioning record. Verified end-to-end: the offline-derived
  `did`/`scode` matched the device's live `[iot]` print exactly, and the resulting
  `$L0$…` password reproduces the firmware's format.
- The gate itself: `check LanAuth NO PASS!` (`0x133708`) on failure,
  `local check auth1/2 OK!` (`0x133650`/`0x13367c`) on success.

Reference implementation:

    def deckey(did, lslat):                      # lslat = config DEVICE_SECRET (b64)
        import hashlib, base64
        from Crypto.Cipher import AES
        key = hashlib.md5((did + "HL4viXBiGEz8mCBkuhkTQFaK").encode()).hexdigest().encode()
        pt  = AES.new(key, AES.MODE_CBC, b"e7uJ6Q8uM7ikpUxf").decrypt(base64.b64decode(lslat))
        return pt[:-pt[-1]].decode()             # strip PKCS#7

    def lan_password(did, lslat, idx=0):         # NOTE: scode = deckey(lslat), NOT product_secret
        import hashlib
        h = hashlib.md5(f"{did}-{deckey(did, lslat)}-{idx}".encode()).hexdigest()
        return f"$L{idx}${h}"

## RPC body encryption — AES-256-CBC  ✓ fully recovered

`avsdk_local_aes256` (**`0x4d760`**) → mbedTLS `aes_crypt_cbc` (`bedtls_aes_crypt_cbc`,
`0x13a300`). Key derivation (`0x4d450`, logs `md5_bytes=%s` / `key=%s` at `0x13a318/28`):

    material = PREKEY + ",ID:" + cmd + "-SEQ:" + seq + "-RPC:" + rpc
    key      = MD5_hex(material)     # 32 ASCII hex chars used as the 32-byte AES-256 key
    iv       = key[:16]              # first 16 bytes of that hex string ✓ (0x4d878 copy)

⚠️ **The two IV rules differ and this is the easiest thing to get wrong.** For
**RPC** payloads the IV is the **first** 16 bytes of the hex digest; for **A/V**
payloads it is the **last** 16. Both are pinned by tests in
`src/internal/pprpc`.

- **PREKEY = `A2r0i1m1a2M0a1x6toriQue`** ✓ — hardcoded constant at `0x139194`, returned
  by the getter `0x48e04`. This is the single value that unlocks offline RPC decryption.
- The A/V per-frame key uses the same routine with the format
  `%s,AVSeq:%s-TT:%s-AVChannel:%s` (`0x13a27c`); the A/V getter (`0x48dac`) prefers a
  per-connection session key (struct +0x88) once negotiated, else falls back to a
  constant at `0x139190`.
- The reused-IV (IV = first half of the key) is the same weakness the public write-up
  flagged; it is not something we rely on, just noted.

Reference implementation:

    def rpc_aes_key(cmd, seq, rpc, prekey="A2r0i1m1a2M0a1x6toriQue"):
        import hashlib
        k = hashlib.md5(f"{prekey},ID:{cmd}-SEQ:{seq}-RPC:{rpc}".encode()).hexdigest()
        return k.encode(), k[:16].encode()   # (32-byte key, 16-byte IV)

## Video path in the firmware  (`video.c`, `0x7D800`–`0x7DA00`)

GC0310 → hardware JPEG → either `gwavi` (AVI muxer) → SD card, or `xc_jpeg_stream` →
`mjpeg_server_thread` → AVSDK → pprpc type-6 frames. The MJPEG thread binds no socket of
its own; it is created once a client is connected and gates on
`conn_num / is_ap_up / is_conn_plat / …` (`0x14b0ff`). A LAN client that completes
LanAuth increments `conn_num`, so the stream runs without any cloud (`is_conn_plat`)
link — consistent with the `AVSDK_CONN_ONLY_TCP` mode and with the public bridge
pulling frames on a laptop with no internet.

Only the **first 1040 bytes** of each JPEG frame are AES-encrypted (rest plaintext);
frames are fragmented with a 3-byte `01 <index> 00` header, `ff` marking the last
fragment, and 5 trailer bytes follow the JPEG EOI (per the public bridge; not yet
re-derived from our binary).

## Live bring-up result (2026-09-09) — stack validated, LanAuth credential still open

The Go bridge (`src/`) was run against the powered camera in AP mode. Captured
over the ESP32 UART bridge and the network:

**Validated end-to-end on real hardware — the hard 90 %:**
- **Framing + PREKEY + AES-256-CBC + protobuf are all correct.** The camera
  decrypts our RPC body and cleanly `pb_decode`s it: UART shows
  `on packet LanAuth_Req` → `==LanAuth(2650)req==` (the pprpc command framework
  only logs that line *after* a successful decode). So `PREKEY =
  A2r0i1m1a2M0a1x6toriQue`, the key/IV derivation, the `0x48` type/flag byte and
  the `seq/cmd/enc` header are confirmed against the device, not just in tests.
- **`did`/`scode` read live** from the `[iot]` boot print (format at logical
  `0x14c4e2`, built in Thumb at `0x7f2ae` from JSON `PRODUCT_KEY`/`PRODUCT_SECRET`):
  `did`/`scode` (redacted; see the gitignored factory config). (This also corrects an earlier
  note: the 18-char `did` with the `A3` is genuine — the 16-char copy embedded in
  the firmware image is the corrupted one, not the other way round.)

**The one thing still blocked: LanAuth returns `check LanAuth NO PASS!`.**
The device reaches `local_check_auth` (`0x2c104`), which tries `auth1`
(`0x2bd68`, a `"%s-%s-%s"` scheme, skipped for `$L…` passwords) then `auth2`
(`0x2bff0`, the `$L` path). `auth2` builds
`"$L%d$" + md5hex(did "-" scode "-" idx)` (helper `0x2bf50`, fmt `%s-%s-%d`
`0x133668` / `$L%d$%s` `0x133674`) and `strcmp`s it against the received `pwd`
(decoded request struct: `user` at `+4`, `pwd` at `+0x45`). Exhaustively ruled
out **live**: idx 0/1, upper/lower MD5 hex, `user` = ""/`did`/`scode`/`admin`/…,
`pwd` in field 2 vs 3, and every ordered pair of `did`/`scode`/`signkey`/`lslat`
(and the base64-decoded / raw `DEVICE_NAME`/`DEVICE_SECRET` forms) as the two
hash inputs. All are rejected identically (the firmware closes the socket on a
failed auth without sending a `LanAuth_Resp`).

Since the value/formula/encoding are all independently confirmed, the remaining
suspect is the **exact bytes the auth reads from the runtime config struct**
(getters `0x1a038`→cfg`+0x94`,`+0x10` = `did`; `0x19ff8`→`+0x120` = `scode`) vs.
the JSON the `[iot]` line prints, or the precise behaviour of the auth MD5-hex
veneer at `0x12e074` (the image carries *both* upper- and lower-case hex tables,
so different call sites differ). Resolving it needs either more Thumb RE of the
config-struct population or a one-time capture of a real client's `LanAuth`
during pairing. Raw captures: `docs/logs/boot.log`, `docs/logs/lanauth-attempt.log`
(gitignored — device secrets).

## Live confirmation (done — see `docs/logs/`, gitignored)

Captured over the ESP32 UART bridge with the camera in AP mode, no internet:

- **Runs fully cloud-free.** `is_conn_plat(0)` and endless failed `iot.dev.glbs`
  attempts to `47.240.1.244:465` etc.; the device keeps serving regardless.
- **`did`/`scode` confirmed** = `PRODUCT_KEY`/`PRODUCT_SECRET` (boot `[iot]` print),
  matching the values derived offline from the dump. LanAuth password reproduced.
- **`netstat`**: TCP **20190 LISTEN** + UDP **20190** (control + discovery), plus UDP 67
  (the AP's DHCP). This is the socket the bridge connects to.
- **`fal`**: partition table matches the logical offsets above exactly.

## Implementation

`src/internal/pprpc` (framing + key derivation), `src/internal/camera` (handshake,
heartbeat, frame reassembly) and `src/internal/pb` (the few protobuf fields
involved) implement all of the above in Go with no external dependencies. An
end-to-end test drives the client against a fake camera that answers the way the
firmware does, so the handshake order, both key derivations and the fragment
reassembly are covered before any hardware is involved.

## Still to confirm (during bridge bring-up)

- A captured RPC packet decrypted with the recovered PREKEY → valid protobuf (end-to-end
  proof of the AES chain). The bridge proves this implicitly the moment LanAuth
  succeeds: its response is undecryptable unless the whole AES chain is right.
  `serve --dump-packets <dir>` records the raw packets for offline checking.
- The 20190 handshake wire order (protobuf vs JSON RPC for the initial LanAuth).
- **The `user` value in `LanAuth.Req`.** We never derived it; the public bridge
  captured it during pairing. The client tries `""`, the `did`, then `"admin"`
  and logs which one is accepted — and `local_check_auth1` (`0x2bf50`) will say
  whether the username is validated at all.
- `Discovery`'s command ID (see above).

## Note for re-analysis: ARM/Thumb boundary is not clean

The config parser (`ut_xciot.c`, `~0x7ec00`) is **Thumb**, even though it sits below the
`0x80000` mark that otherwise separates the ARM and Thumb regions. Don't assume a single
hard ARM→Thumb split; expect Thumb functions mixed into the upper part of the nominally-ARM
range. Verify per function (disassemble both ways, keep the sane one) rather than trusting
a fixed boundary.
