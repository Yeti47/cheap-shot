# cheap-shot

Turning a cheap "A9-style" mini WiFi spy camera into a Linux-usable webcam,
without ever creating an account with the vendor's app/cloud ecosystem.

## Status (2026-09-09)

**Working end-to-end.** LanAuth is cracked and the bridge streams live 640x480
MJPEG (~10 fps) from the stock camera over pure LAN — no vendor app, no cloud —
serving it over HTTP and into a `/dev/video*` device. The port-20190 protocol is decoded and both secrets it needs derive
**offline, with no vendor app or cloud account**. The bridge in [`src/`](src/)
implements it in Go: LanAuth → VideoPlay → reassembled JPEG frames, re-served as
MJPEG-over-HTTP and pushed into a `v4l2loopback` `/dev/video*` node.

Confirmed on real hardware: the camera authenticates the bridge and delivers a
valid session key, then streams JPEG frames the bridge reassembles and re-serves.
The auth secret derives fully offline via `deckey` (an AES-decrypted config
field). See [`docs/findings.md`](docs/findings.md) for the story and
[`docs/protocol.md`](docs/protocol.md) for the wire protocol.

Quick facts:
- Chip: **Beken BK7252UQN48** (48-pin QFN, ARM968E-S core, RT-Thread OS)
- Camera sensor: **GC0310/GC0312**
- Full 2MB stock firmware dump (device provisioning record **scrubbed** for
  publication): [`firmware/cheapcam_full_2MB_dump.bin`](firmware/cheapcam_full_2MB_dump.bin)
  (sha256 `40d939ce3e02aaf51ee408d9e0db5c909a3e83181d4364eeabb0dfcd2a4cf26a`)
- Key community resource: [`daniel-dona/beken7252-opencam`](https://github.com/daniel-dona/beken7252-opencam)
  — an active open-firmware project explicitly targeting this exact chip
  package, with real HTTP video streaming on its roadmap

## Running the bridge

Prerequisite for the `/dev/video*` output (the container cannot load a kernel
module, so this is a host step either way):

```sh
sudo modprobe v4l2loopback exclusive_caps=1 card_label=cheap-shot
```

Then configure and run. The camera needs two device-local values — `did`
(factory `PRODUCT_KEY`) and `lslat` (factory `DEVICE_SECRET`, a base64 string) —
both in `firmware/logical/factory_config.json` (gitignored). Supply them either
via environment variables or a JSON file.

**Docker (env-based, recommended):**

```sh
cp .env.example .env       # .env is gitignored; fill in did + lslat
docker compose up --build
```

**Host binary with env vars (no config file):**

```sh
go build -o cheap-shot ./src/cmd/cheap-shot
CHEAPSHOT_CAM1_HOST=192.168.9.252 \
CHEAPSHOT_CAM1_DID=<PRODUCT_KEY> CHEAPSHOT_CAM1_LSLAT=<DEVICE_SECRET> \
./cheap-shot serve            # falls back to env when config.json is absent
```

**Or a JSON config file:**

```sh
cp config.example.json config.json     # config.json is gitignored
# fill in did + lslat, then:
./cheap-shot serve --config config.json
```

Then open `http://127.0.0.1:8080/` for the **live dashboard** — a tile per camera
with its feed, a connected/fps/last-frame-age badge, and MJPEG auto-reconnect, so
you can see at a glance whether the cameras are running. The raw stream is at
`/cam1/stream.mjpeg`, a still frame at `/cam1/snapshot.jpg`, `/healthz` returns
JSON status, and the camera also appears as an ordinary webcam on the configured
`/dev/video*` node.

Other subcommands:

- `cheap-shot discover` — slow, deliberate probe of port 20190 across the LAN
  (a *fast* sweep wedges this device's TCP stack).
- `cheap-shot derive --did … --scode …` — print the LanAuth password offline.
- `cheap-shot healthcheck` — used by the container's `HEALTHCHECK`.

Device secrets never belong in the image: `did`/`lslat` come from `.env`, the
`CHEAPSHOT_<CAM>_DID` / `CHEAPSHOT_<CAM>_LSLAT` environment variables, or a
mounted config file — all of which stay local (gitignored).

## Repo layout

- `src/` — **the bridge** (Go, zero external dependencies). `src/cmd/cheap-shot`
  is the binary; `src/internal/pprpc` the wire format, `src/internal/camera` the
  LAN client, `src/internal/v4l2` the `/dev/video*` sink.
- `Dockerfile`, `compose.yaml`, `.env.example`, `config.example.json` — containerized
  deployment; the image is the static binary and nothing else.
- `docs/findings.md` — the detailed technical writeup: device identification,
  dead ends (don't repeat these), the physical access procedure that worked,
  the static-analysis breakthrough, and prioritized next steps.
- `docs/protocol.md` — the reversed port-20190 (`pprpc`/`avsdk`) protocol:
  framing, LanAuth, and the offline AES/password key derivations.
- `firmware/cheapcam_full_2MB_dump.bin` — the 2MB flash dump, with the 240-byte
  device provisioning record (`0x1F4000`) scrubbed. The unredacted originals are
  kept locally in `firmware/unredacted/` (gitignored).
- `firmware/logical/` — the **usable**, de-CRC'd images (`flash_logical.bin`
  etc.), produced from the raw dump by `tools/decrc.py`. Use these for analysis.
  `factory_config.json` (device secrets) is generated here and gitignored.
- `firmware/partitions/` — **deprecated.** The original `dd`-extracted partitions;
  wrong because the flash is CRC-interleaved (see findings). Kept as history only.
- `docs/photos/` — teardown photos (PCB, chip markings, board silkscreen).
- `tools/decrc.py` — strips/re-inserts the Beken `beken_onchip_crc` interleave
  (32 data + 2 CRC bytes per 34); `--re-crc` round-trips byte-exactly for reflash.
- `tools/ghidra-docker/` — a Docker analysis environment (Ghidra + radare2 +
  arm-none-eabi + capstone) with a headless import/setup script for the dump.
- `tools/esp32-uart-bridge/` — a PlatformIO project that turns a spare
  ESP32-S3 dev board into a 3.3V USB-serial bridge, used to talk to the
  camera's UART console and (via `bk7231tools`) its ROM bootloader.

## Hard constraint

Never create any account, anywhere in the camera's app/cloud ecosystem — not
even a throwaway one. This is why the project takes the physical/local-only
route instead of using the vendor app.
