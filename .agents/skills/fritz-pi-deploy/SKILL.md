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

Target: 64-bit Raspberry Pi OS (Bookworm or newer) with the desktop and
auto-login, Pi 4B, HDMI display, **Logitech C922 USB webcam** (camera + mic)
and an **Echo Dot Bluetooth speaker**. Hardware specifics: `fritz-c922`.

## Two processes, run natively with systemd

| Service | Binary | Built by |
|---|---|---|
| `fritz-face.service` | `fritz_iot` | `iot/` CMake (on the Pi, or the existing Docker arm64 build) |
| `fritz.service` | `fritz` (Go) | Cross-compiled on the laptop |

Run the Go brain natively, not in Docker: PipeWire (mic + Bluetooth speaker)
lives in the desktop login session and is much simpler outside a container.
Both services run as **the desktop login user** (not a separate `fritz`
user) so they can reach PipeWire and the display. The face's Docker setup in
`iot/` can stay as an option.

## Go brain

```bash
cd api
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/fritz ./cmd/server
scp bin/fritz pi@fritz.local:/opt/fritz/
```

Keep `CGO_ENABLED=0` working: use pure-Go dependencies (`modernc.org/sqlite`,
`nhooyr.io/websocket` or `gorilla/websocket`). External tools are called as
subprocesses: `ffmpeg`, `v4l2-ctl`, `arecord`, `aplay`, `wpctl`,
`bluetoothctl`, `piper`.

## Pi packages

```bash
sudo apt install -y ffmpeg v4l-utils alsa-utils pipewire-alsa libsdl2-dev cmake g++
# Piper: download the arm64 release and a voice (.onnx + .onnx.json) into /opt/fritz/piper/
```

Check hardware before debugging code (full list in `fritz-c922`):

- Camera: `ls /dev/v4l/by-id/ | grep -i c922`, then grab one frame with ffmpeg.
- Mic: `arecord -D pipewire -f S16_LE -r 16000 -c 1 -d 3 /tmp/t.wav && pw-play /tmp/t.wav`.
- Speaker: `bluetoothctl info <echo MAC> | grep Connected`, `wpctl status`.
- Face: `./fritz_iot` then send `emotion happy` to UDP 127.0.0.1:7070.

The login user needs groups `video render input audio` (and `gpio` if GPIO
is used) — the default Pi user already has them.

## Secrets and config

`/etc/fritz/fritz.env` (mode 0600, owned by the service user), loaded with
`EnvironmentFile=`:

```
MODEL_API_KEY=...
FRITZ_TZ=<IANA timezone, e.g. Asia/Singapore>
FRITZ_CONFIG_DIR=/var/lib/fritz
FRITZ_MIC=pipewire
FRITZ_CAMERA=v4l2
FRITZ_CAMERA_DEVICE=/dev/v4l/by-id/usb-046d_C922_Pro_Stream_Webcam_<serial>-video-index0
FRITZ_SPEAKER_BT=<Echo Dot MAC>
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
# The desktop login user and its uid: PipeWire lives in that session.
User=<login user>
Environment=XDG_RUNTIME_DIR=/run/user/<uid>
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
- Camera: MJPEG passthrough (`-c:v copy`) at 1280×720 @ 10 fps costs
  almost no CPU; only decode/downscale the 1–2 frames/s sent to SAM.
- If the face stutters while Piper runs, lower Piper's priority
  (`Nice=5` on the brain service) rather than the face's.

## Demo-day checklist

- Wi-Fi fallback: phone hotspot pre-configured in NetworkManager.
- `systemctl is-active fritz fritz-face` both active after a cold boot.
- API key quota checked; Google token not expired (testing-mode tokens
  expire after 7 days — re-run `googleauth` the day before recording).
