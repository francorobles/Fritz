---
name: fritz-pi-deploy
description: >
  Building and running Fritz on the Raspberry Pi 4B: cross-compiling the Go
  brain for arm64, building the C++ face, systemd services, camera/audio/display
  device access, secrets, and on-device troubleshooting. Use when deploying to
  the Pi, writing systemd units or Docker config, setting up the camera, mic or
  speaker, or when something works on the desktop but not on the Pi.
---

# Deploying Fritz to the Pi 4B

Target: 64-bit Raspberry Pi OS (Bookworm or newer), Pi 4B, Camera Module on
the CSI port, USB mic, speaker on the 3.5 mm jack or USB, HDMI display.

## Two processes, run natively with systemd

| Service | Binary | Built by |
|---|---|---|
| `fritz-face.service` | `fritz_iot` | `iot/` CMake (on the Pi, or the existing Docker arm64 build) |
| `fritz.service` | `fritz` (Go) | Cross-compiled on the laptop |

Run the Go brain natively, not in Docker: libcamera, ALSA and the camera
device nodes are much simpler outside a container. The face's existing Docker
setup in `iot/` can stay as an option.

## Go brain

```bash
cd api
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/fritz ./cmd/server
scp bin/fritz pi@fritz.local:/opt/fritz/
```

Keep `CGO_ENABLED=0` working: use pure-Go dependencies (`modernc.org/sqlite`,
`nhooyr.io/websocket` or `gorilla/websocket`). External tools are called as
subprocesses: `rpicam-vid`, `rpicam-still`, `arecord`, `aplay`, `piper`.

## Pi packages

```bash
sudo apt install rpicam-apps alsa-utils libsdl2-dev cmake g++
# Piper: download the arm64 release and a voice (.onnx + .onnx.json) into /opt/fritz/piper/
```

Check hardware before debugging code:

- Camera: `rpicam-hello --list-cameras`, then `rpicam-still -o /tmp/t.jpg`.
- Mic: `arecord -l`, then `arecord -f S16_LE -r 16000 -c 1 -d 3 /tmp/t.wav && aplay /tmp/t.wav`.
- Face: `./fritz_iot` then `echo "emotion happy" | nc -u -w0 127.0.0.1 7070`.

The service user needs groups `video render input audio` (and `gpio` if
GPIO is used).

## Secrets and config

`/etc/fritz/fritz.env` (mode 0600, owned by the service user), loaded with
`EnvironmentFile=`:

```
MODEL_API_KEY=...
FRITZ_TZ=<IANA timezone, e.g. Asia/Singapore>
FRITZ_CONFIG_DIR=/var/lib/fritz
FRITZ_MIC=default
FRITZ_CAMERA=rpicam
FRITZ_PIPER_VOICE=/opt/fritz/piper/en_US-lessac-medium.onnx
```

(Set `FRITZ_TZ` to the user's real IANA timezone.) The Google token lives in
`FRITZ_CONFIG_DIR`. Commit an `api/fritz.env.example` with blank values, never
the real file.

## systemd units (sketch)

```ini
# /etc/systemd/system/fritz.service
[Unit]
Description=Fritz brain
After=network-online.target sound.target fritz-face.service
Wants=network-online.target

[Service]
User=fritz
EnvironmentFile=/etc/fritz/fritz.env
ExecStart=/opt/fritz/fritz
Restart=always
RestartSec=2

[Install]
WantedBy=multi-user.target
```

`fritz-face.service` is the same with `ExecStart=/opt/fritz/fritz_iot` and,
for desktop mode, `Environment=DISPLAY=:0`. Kiosk (no desktop) uses KMSDRM
directly; see `iot/README.md`.

Logs: `journalctl -u fritz -f`.

## Performance on a Pi 4

- Everything heavy is remote (Meta Model API) or small (Piper, SDL).
- Keep camera capture at 640×480 @ 5 fps for tracking; it's plenty.
- If the face stutters while Piper runs, lower Piper's priority
  (`Nice=5` on the brain service) rather than the face's.

## Demo-day checklist

- Wi-Fi fallback: phone hotspot pre-configured in NetworkManager.
- `systemctl is-active fritz fritz-face` both active after a cold boot.
- API key quota checked; Google token not expired (testing-mode tokens
  expire after 7 days — re-run `googleauth` the day before recording).
