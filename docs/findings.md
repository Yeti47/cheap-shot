# Findings

## The device

A battery-powered "mini WiFi camera" bought on Amazon (listing ASIN
B0FH511WHK, since removed; a near-identical listing under a different brand,
"GLOGLOW" ASIN B0BY5XC92S, confirms this is a generic "A9-style" mini-cube
camera sold under dozens of random brand names by different resellers on top
of the same underlying hardware/firmware).

- Setup AP: SSID `LLM_00A9_0AC564`, password `12345678`, camera reachable at
  `192.168.9.252` once joined (it's its own gateway/DHCP server). **Both are
  factory-default values baked into every unit of this camera model** — not
  a secret tied to this specific device or to any personal network of ours;
  they're published here purely as reproducibility context for the rest of
  these findings.
- Companion app ecosystem: "BABIQ" (a Tuya-compatible/clone platform).
  CamHi/CamHipro do **not** work with this device — confirmed via real
  hardware plus decrypted WPA2 traffic capture; those apps can't even find it.
- Buttons/LEDs: ON/OFF = hold 5s. MODE = reset/pairing button, hold 15s to
  re-enter AP mode. Holding MODE while applying power does something very
  different and far more useful — see "Entering bootloader mode" below.
- Only TCP port 20190 is ever open. It completes a TCP handshake but returns
  zero bytes to a plain HTTP request and never closes the connection — a
  proprietary binary-handshake port (this vendor's own P2P/AVSDK protocol),
  not a plain video server.
- Has a microSD slot. Pulling recorded clips directly off the card remains
  the zero-effort fallback for actual footage, independent of everything
  else here.

## Chip identification

Confirmed by direct photo of the chip markings (see `docs/photos/`):
**Beken BK7252UQN48** (lot/date code `AU411LXB`), a 48-pin QFN package.

- ARM968E-S core, runs **RT-Thread** OS plus a vendor SDK with `AVSDK`/
  `OSAL`/`xc_*`-prefixed module naming.
- Camera-capable silicon: DVP camera interface + hardware JPEG encoder.
- This is **not** BK7231T/N (those are switch/plug-oriented chips with no
  camera hardware at all) and **not** the "documented" 68-pin BK7252 variant
  that has a public datasheet — this 48-pin package has a different pinout
  and no public datasheet of its own.
- Board silkscreens: main board `IO-IPC-48B-V1`, camera sensor daughterboard
  `Inocloo`. Both are generic/whitelabel ODM identifiers with no public
  documentation found anywhere.
- Camera sensor, confirmed from the live boot log: **GC0310/GC0312**.

## Dead ends (confirmed closed — don't repeat these)

- **Hichip/PPPP protocol** (0xF1 magic-byte framing, `LanSearch`, etc.) — the
  original hypothesis for this project, later disproven. Wrong platform
  entirely; this device is Tuya/Beken-based, not Hichip.
- **CamHi/CamHipro apps** — genuinely cannot find/connect to this camera.
- **Android emulator** for app-based discovery — structural dead end,
  emulators don't propagate real UDP broadcast/multicast to the physical LAN.
- **Tuya cloud `local_key`** — verified directly against `tinytuya`/`TuyAPI`
  source: only issued through a real device-to-Tuya-cloud activation
  handshake, no protocol-level bootstrap exists. Neither library supports
  camera-class Tuya devices anyway, even with a key in hand.
- **`tuya-cloudcutter`** — has zero BK7252 entries in its entire public
  device/profile catalog. Moot regardless, since three live exploit attempts
  against BK7231T/N stand-in profiles all failed identically (the memory
  corruption bug was reached but never landed as working code execution).
- **Anyka SD-card boot-hijack toolkits** (`tasarren/lsc-tuya-toolkit`,
  `guino/LSCOutdoor1080P`) — these target Anyka Linux SoCs. A live test
  (full toolkit payload on a real SD card, power-cycled) came back a clean
  negative: firmware never touched the marker files. Confirms this chip
  runs Beken's own RTOS, not embedded Linux — an SD card can't bootstrap
  access here the way it does on genuine Anyka boards.
- **A full 1-65535 port nmap sweep at high scan rate** briefly knocked the
  camera's embedded TCP/IP stack completely unresponsive. A slow/targeted
  scan of a candidate port list is fine; a fast full sweep against this
  device is not.
- **OpenBeken / LibreTiny** — neither has any BK7252 camera driver as of the
  last check.

## The community is already building this

[`daniel-dona/beken7252-opencam`](https://github.com/daniel-dona/beken7252-opencam)
("RT-Thread alternative project for A9 cameras") is an active open-firmware
project whose 1.0.0 release plan **explicitly targets our exact chip
package**: "Support the two PCB variations based on QFN48 chips: `JC_V9_V3B`
and `BMY_A9_V3A`." Planned/in-progress features include an HTTP video
streaming endpoint, HTTP single-frame capture, MQTT, Telnet-to-MSH, and OTA.
Sensors targeted include `GC0310` — our exact sensor.

- Found via a moderator of the OpenBK7231T project (which has no BK7252
  support) pointing to it on a public forum thread about this same camera
  family: https://www.elektroda.com/rtvforum/topic4033757.html
- Active development moved from `main` to a `release_1` branch with a real
  CI pipeline producing firmware build artifacts. As of the last check this
  was a live, currently-active project, not something stalled.
- GitHub issue #16 ("A13 board pictures") documents someone with almost
  certainly the same underlying product family (same `LLM_XXXX_XXXXXX` SSID
  convention, same `192.168.9.x` subnet, same chip). Their board's
  `TX2`/`RX2` pads turned out to be JTAG, not UART — **this does not apply to
  our board**, ours are genuine UART (confirmed directly, see below).
- Issue #2 confirms LTChipTool + a plain FT232R adapter works for BK7252
  UART-based flashing — same underlying mechanism as `bk7231tools`, which is
  what we ended up using directly.
- The maintainer has explicitly asked for firmware dumps (issue #13). Worth
  sharing ours — helps their UQN48 support work and is a way to give back.

## Entering bootloader mode (critical — don't lose this)

A plain power cycle boots straight to the RT-Thread app. In that state, the
ROM bootloader's link handshake (`BkLinkCheckCmnd`) gets **zero response** —
confirmed via `bk7231tools chip_info -D` debug trace showing continuous
transmission with no reply at all.

**The fix: hold the MODE/reset button down, apply power while still holding
it, and keep holding for a couple of seconds after power-up.** This is what
actually activates the BK7252 ROM bootloader/download mode.

Bootloader mode does **not** persist across separate tool invocations —
redo the full button+power sequence immediately before every single
`bk7231tools` command. A prior successful connection does not carry over to
the next command.

## UART access setup (ESP32-S3 as a bridge)

See `tools/esp32-uart-bridge/` — a PlatformIO project (Arduino framework)
that turns a spare ESP32-S3 dev board into a 3.3V USB-serial bridge:

```cpp
Serial.begin(115200);                       // native USB CDC -> PC
Serial1.begin(115200, SERIAL_8N1, 18, 17);   // dedicated UART, GPIO18=RX, GPIO17=TX -> camera
```

Wiring: camera `TX2` → ESP32 GPIO18, `RX2` → GPIO17, GND → ESP32 GND (GND
tapped from the camera board's micro-USB connector shell — same net,
physically closest point to the TX2/RX2 pads).

Lessons learned:
- Many ESP32-S3 boards have **two USB-C ports** — one native, one wired to an
  onboard USB-UART bridge chip straight to the default "TX"/"RX" pins. Use a
  dedicated `Serial1` on free GPIOs instead of those labeled default pins, to
  avoid putting your target device on the same electrical nodes as that
  bridge chip.
- Native USB CDC doesn't auto-reset into bootloader for flashing — hold
  BOOT, tap RESET, release BOOT, then immediately retry the upload.
- **Soldering matters.** Holding bare wire tips onto the pads with tape
  worked in principle but was unreliable; real soldering fixed most signal
  issues. Watch for solder bridges between `TX2`/`RX2` — they're very close
  together.
- Residual line noise (likely RF/EMI from the camera's own active WiFi
  radio) can inject spurious keystrokes into the camera's shell during
  sustained sessions, even with nothing being sent from our side. Single
  short commands land fine; extended live sessions degrade. Effective
  workaround: send one command, capture ~10s to a file, then search the file
  for the expected output rather than reading it live.
- The camera goes into deep sleep after a while idle in AP mode (normal
  battery-saving behavior) — needs a fresh power-cycle to respond again.

Once connected, the camera exposes a full interactive RT-Thread `msh />`
shell with a real command set: `fal` (flash operations), `video`, filesystem
commands (`ls`/`cat`/`cp`/`mv`/`rm`/`mkdir`/`df`/`mkfs`), `mqtt_*`, `wifi`,
`reboot`, `printenv`/`setenv`/`saveenv`. Confirmed live that stock firmware
starts an MJPEG server thread at boot (`start web camerar` / `creat
mjpeg_server_thread!`).

> **Superseded / corrected — see "Static analysis breakthrough" below.** The
> full `__cmd_*` table has **69 commands**, not the handful listed here (notably
> `video open`/`video close`). And the MJPEG thread is **not** "tunneled through
> HTTP": there is no HTTP or RTSP server anywhere in the firmware. It hands JPEG
> frames to the AVSDK/pprpc stack on port 20190. The whole port-20190 protocol,
> including the offline key derivation, is now documented in
> [`protocol.md`](protocol.md).

## Flash layout

Read directly from the live boot log's automatic FAL partition table print.
**These are *logical* offsets** — the physical dump has an extra CRC layer, see
"Static analysis breakthrough" below.

| Partition  | Flash device       | Logical offset | Logical length |
|------------|--------------------|----------------|----------------|
| bootloader | `beken_onchip_crc` | `0x00000000`   | `0x0000f000` (~60KB)  |
| app        | `beken_onchip_crc` | `0x00010000`   | `0x00130000` (~1.2MB) |
| download   | `beken_onchip`     | `0x00154000`   | `0x000a9000` (~676KB) |

Total mapped range ≈ 0x1FD000 (~2MB), matching the 2MB SPI flash chip.

> ⚠️ `beken_onchip_crc` is **literal**: those two partitions are stored with a
> 2-byte CRC after every 32 data bytes. A raw dump is therefore CRC-interleaved
> and the logical→physical map is `phys = logical / 32 * 34`. Use
> `firmware/logical/` (produced by `tools/decrc.py`), not raw offsets, for any
> analysis. Details below.

## Firmware dump

Obtained with `bk7231tools` (installed via `pip install ltchiptool`, which
pulls it in as a dependency):

```
bk7231tools read_flash -d /dev/ttyACM0 -b 115200 -s 0x0 -l 0x200000 <file>.bin
```

Full 2MB dump completed and verified genuine via `strings` (contains
`beken_onchip`, `GC0307`/`GC0309`/`GC0310(a3)/GC0312(b3)`/`GC0311_DEV` —
matches the live boot log exactly, not corrupt garbage). Stored at
`firmware/cheapcam_full_2MB_dump.bin`
(sha256 `40d939ce3e02aaf51ee408d9e0db5c909a3e83181d4364eeabb0dfcd2a4cf26a`).

`bk7231tools dissect_dump`'s automatic RBL-container parsing failed (CRC
mismatches). The old `firmware/partitions/*.bin` were then `dd`'d at the raw
offsets above — **both mistakes have the same root cause, now understood**: the
flash is CRC-interleaved (next section). Those files are kept only as the
historical record; **do not use them for analysis** — use `firmware/logical/`.

## Static analysis breakthrough (2026-09-09)

Full writeup of the reversed protocol is in [`protocol.md`](protocol.md); the
load-and-analyze recipe is baked into `tools/ghidra-docker/`. Key results:

**1. The dump is CRC-interleaved.** `beken_onchip_crc` means flash physically
stores **32 data bytes + a 2-byte CRC, every 34 bytes**, aligned to physical 0.
CRC = **CRC-16, poly `0x8005`, init `0xFFFF`, non-reflected, xorout 0, stored
big-endian** (brute-forced, then verified over all 61 680 blocks — the only
mismatches are 14 raw, non-CRC records above `0x1A0000`). `tools/decrc.py`
strips this (and `--re-crc` reverses it byte-exactly, the safety net for
flashing). The raw `app.bin` was wrong twice over — cut at raw offsets *and*
still interleaved, which desyncs any disassembler every 32 bytes and is why the
"blank 4KB sector" and the `dissect_dump` CRC errors appeared.

De-interleaved artifacts (`firmware/logical/`, generated, reproducible):

| File | sha256 | notes |
|------|--------|-------|
| `flash_logical.bin` | `e3fb52f1…d86195c` | full `0x1E1E00` image — the Ghidra input (provisioning record scrubbed) |
| `app_logical.bin`   | `bb9bb8af…d92d8481` | app, logical `0x10000–0x178000` |
| `bootloader_logical.bin` | `1d685bd0…d75855ac` | logical `0x0–0xF000` |

Proof it worked: ARM-condition-field fraction in the code region **0.428 → 0.897**;
`strings` count **1218 → 8075**; symbols like `avsdk_cs_append_thumbnail_v2` are
whole again (were `avsdk_cs_append_thum7ubnail_v2`).

**2. Load base `0x00000000`, execute-in-place.** Both vector tables (`0x0`,
`0x10000`) hold handlers at their own flash offset. SRAM is at `0x00400000`.
Code is **mixed ARM and Thumb**: ARM `0x10000–0x80000`, **Thumb `0x80000–0x140000`**
(must be forced before auto-analysis), rodata `~0x140000–0x178000`.

**3. Port 20190 = XC Things `pprpc`/`avsdk`, and it is drivable offline.** No
HTTP/RTSP in the firmware. The two secrets a LAN client needs both derive from
device-local values (read from flash, never the cloud) — fully recovered from
our binary:
- **LanAuth password** = `"$L<idx>$" + MD5("<did>-<scode>-<idx>")`
  (`local_check_auth1` @ `0x2bf50`; `did`/`scode` are config fields read locally).
- **RPC AES-256 key** = `MD5("<PREKEY>,ID:<cmd>-SEQ:<seq>-RPC:<rpc>")`, IV = first
  16 bytes of that hex; **`PREKEY = A2r0i1m1a2M0a1x6toriQue`** (`0x139194`).

This is the decisive result: **Route A (talk to stock firmware over LAN) is
viable with no vendor app and no cloud account** — matching the independent
working bridge `IvanFogel/ino-a9-local-bridge`.

**Confirmed live (UART, camera in AP mode, no internet):** `did`/`scode` are the
factory JSON's `PRODUCT_KEY`/`PRODUCT_SECRET` (from the boot `[iot]` print,
`ut_xciot.c:640`), matching the values derived offline from the dump — so the
LanAuth password needs **no live read at all**. `netstat` shows TCP **20190
LISTEN** + UDP 20190; `fal` matches the partition table; `is_conn_plat(0)` with
endless failed cloud attempts confirms the device serves fine offline. Full
mapping and captured evidence in [`protocol.md`](protocol.md) (raw logs in
`docs/logs/`, gitignored — they contain device secrets).

**4. The MJPEG thread streams without cloud.** `mjpeg_server_thread`/`xc_jpeg_stream`
(`video.c`, `~0x7D800`) is created once a client connects (`conn_num>0`) and does
not require `is_conn_plat` (cloud); the firmware has an explicit
`AVSDK_CONN_ONLY_TCP, NO P2P!` mode.

**5a. Command IDs and field layouts, fully recovered (2026-09-10).** The
name-only string table (122 entries at `0x5a540`) carries no IDs, but the pprpc
**command registration table** does: a 126-entry array of 0x20-byte records at
`0x13bca8`, each `{id, flags, req_fields, sizes, resp_fields, …}` pointing at the
nanopb descriptors. Reading it off gives every command's id and wire fields.
Confirmed against the live IDs (`2650 LanAuth`, `2610 VideoPlay`, `2614 AudioPlay`,
`106 SyncConn`) and it resolves the two open questions: **`Discovery` is 2600**
(not the guessed 2606 — the id table jumps `2603 → 2610`, so 2604–2609 don't
exist), and the WiFi commands are **`2601 WifiAPGet`, `2602 WifiSet`, `2603
WifiGet`**. `DefaultDiscoveryCmd` updated to 2600. Full `WifiSet` spec (join a
router network as a station) is in [`protocol.md`](protocol.md); implemented as
`camera.Client.SetWiFi` + the `cheap-shot wifi-config` subcommand.

**5. The stock shell has 69 msh commands.** Full list (from the `__cmd_*` table):
`HD audio_dump cat cd cp date df dns echo fal free getvalue help ifconfig linkkey
list_device list_event list_fd list_mailbox list_memheap list_mempool list_msgqueue
list_mutex list_sem list_thread list_timer ls mac mic_aec_play mkdir mkfs mq_pub_test
mqtt_publish mqtt_start mqtt_stop mqtt_subscribe mqtt_unsubscribe mv netio_init netstat
ntp_sync ping printenv ps psram_mem_api_test pwd reboot resetenv rfcali_cfg_mode
rfcali_cfg_rate_dist rfcali_cfg_tssi_b rfcali_cfg_tssi_g rfcali_show_data rm rxsens
saveenv set_log setenv stack sysdump time txevm version video wdg_refresh wdg_start
wdg_stop wifi wifi_demo`. `video` usage is `video open` / `video close`.

**6. ⚠️ The dump contains device-unique secrets.** A 240-byte JSON provisioning
record sits at **physical `0x1F4000`** (raw, not CRC'd): `MAC_ADDR`, `PRODUCT_KEY`,
`PRODUCT_SECRET`, `DEVICE_NAME`, `DEVICE_SECRET`, `PRODUCT_ID`. Extracted to
`firmware/logical/factory_config.json` (**gitignored**). This changes the
"share the dump" plan — publish a **scrubbed** copy only (see below).

## Project status check (2026-09-09)

`daniel-dona/beken7252-opencam` is still very much alive. The `release_1`
branch has real commits into mid-October 2025 (past what an earlier check
saw) — MQTT support, Home Assistant discovery, LED/key/device-ID/SD-JSON
config work, more sensor captures. Still no tagged release published.

**Directly relevant: issue #31, "BK7252UQN48"** (opened by `divadiow`,
2025-12-07, no replies yet): "I've traced the GPIOs on BK7252UQN48 and they
appear to match the **BK7252NUQN481**, which we do have a datasheet for."
References https://www.elektroda.com/rtvforum/viewtopic.php?p=21773580#21773580
for detail (not yet read in full). **This is potentially huge** — if
confirmed, our exact chip's real pinout becomes fully documented via an
existing datasheet, removing the biggest remaining unknown for anyone doing
serial/JTAG work on this chip family. This is the natural place to
contribute our dump when that happens (see below) — it's an active,
unanswered thread about exactly our chip.

## ✅ SOLVED (2026-09-10) — full LAN video works

LanAuth is cracked and the bridge streams live video end-to-end (LanAuth →
VideoPlay → 640x480 MJPEG ~10fps → HTTP + `/dev/video*`). The blocker was that
the auth secret is **`deckey(lslat)`** (the config `lslat` field AES-decrypted at
load time), not `product_secret`. Full derivation and the live-confirmed values
are in [`protocol.md`](protocol.md); the bridge implements it in
`src/internal/camera/`. Config now takes `did` + `lslat` (from the factory
record's `PRODUCT_KEY` / `DEVICE_SECRET`).

**Device-recovery note:** the `linkkey` msh command's `device_name`/`device_secret`
args are **hex strings** (`0x7f0f4` = hex-decode). Restore with:
`linkkey <product_key> <device_name_hex> <device_secret_hex> <product_secret> <mac_addr>`
(the exact values for our unit are in the gitignored `firmware/logical/factory_config.json`).
An illegal-format `did` boot-loops the device.

## Where this stands, and what's not solved yet

The static analysis (see "Static analysis breakthrough" above) settled the
strategic question: **Route A — drive the stock firmware over the LAN — is the
path.** The protocol on port 20190 is reversed and both required secrets derive
offline; nothing needs the vendor cloud. `docs/protocol.md` has the full spec.

Chosen direction and remaining work, in order:

1. **Build the host-side bridge — done, in [`src/`](../src/).** Written in Go
   with zero external dependencies: LanAuth → `SyncConn` → heartbeat →
   `VideoPlay` → reassembled JPEG frames, re-served as **MJPEG over HTTP** and
   pushed into **`v4l2loopback` (`/dev/video*`)**. The bridge is the project's
   core component, so it lives in `src/`, not `tools/`.
   `IvanFogel/ino-a9-local-bridge` was the reference for the wire details we had
   not re-derived ourselves (handshake order, protobuf field numbers, fragment
   reassembly, the A/V IV convention); the implementation is our own.
   Containerized from the start (`Dockerfile` + `compose.example.yaml`, a
   `scratch` image holding just the static binary), with a plain host run
   equally supported. Remaining: point it at the powered camera.
2. **Live confirmation (done 2026-09-09 — stack validated; LanAuth blocked).**
   Ran the bridge against the powered camera. **Proven on hardware:** framing,
   `PREKEY`, AES-256-CBC and protobuf are all correct — the camera decrypts and
   cleanly `pb_decode`s our request (`on packet LanAuth_Req` → `==LanAuth(2650)req==`).
   `did`/`scode` read live from the `[iot]` boot print (redacted device values;
   the 18-char `did` is genuine; the firmware-embedded 16-char copy is the
   corrupted one). **Still blocked:** LanAuth returns `check LanAuth NO PASS!` even
   though the `$L0$md5(did-scode-0)` password is derived from those exact live values
   with the disassembly-confirmed formula. Exhaustively ruled out live (idx, hex
   case, username, field order, every did/scode/signkey/lslat pairing). The gap is
   narrow — the exact bytes the auth struct holds, or the auth MD5-hex veneer at
   `0x12e074` — and needs deeper Thumb RE or a pairing capture. Full writeup in
   [`protocol.md`](protocol.md); raw logs in `docs/logs/` (gitignored). Still worth
   doing over UART: capture the boot
   log to `docs/logs/boot.log` (none saved yet); read `did`/`scode` via
   `getvalue`/`linkkey`/`printenv`; `video open` then `netstat`; decrypt a
   captured RPC packet with the recovered PREKEY as end-to-end proof. Use the
   proven one-command-at-a-time UART discipline. `fal` gives the authoritative
   partition table.
3. **Fallback — custom firmware (`daniel-dona/beken7252-opencam`).** Only if
   Route A somehow fails. Note this is **weaker than previously assumed**: no
   prebuilt image exists (CI artifact expired), it runs on the **68-pin**
   BK7252UQN68, and the maintainer reports the **48-pin UQN48 did not work** when
   flashed; GC0310 support is still an open issue. Would mean building from source
   and porting to our part. Flash via `bk7231tools write_flash`; restore with
   `tools/decrc.py --re-crc` (round-trip verified byte-exact against the dump).
4. **Contributing back.** Publish only a **scrubbed** dump
   (`firmware/cheapcam_scrubbed.bin`, physical `0x1F4000` zeroed — it holds this
   unit's secrets, finding 6). The genuinely useful, secret-free contribution to
   opencam issue #31 is the **CRC parameters, load base, and Thumb boundary**.
5. **ESP32-S3 stays a control/console channel, not a video path** — 115200 baud
   (~11 KB/s) can't carry even 1 fps of JPEG. Useful for shell/reset/boot-log
   under any route.
6. The SD card remains the zero-effort footage fallback, independent of all the
   above.
