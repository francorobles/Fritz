---
name: fritz-c922
description: >
  Fritz's audio/video hardware on the Raspberry Pi: the Logitech C922 Pro
  Stream USB webcam as both camera (V4L2 + ffmpeg MJPEG capture) and
  microphone (PipeWire), and the Echo Dot Bluetooth speaker for Fritz's voice.
  Device discovery, exact capture/playback commands, camera controls, mic
  levels, echo avoidance, Bluetooth reconnects, and hardware checks. Use when
  writing camera or audio capture/playback code, choosing device paths or
  formats, or debugging "no video", "no audio", "too quiet", or "Fritz hears
  itself".
---

# Fritz hardware: C922 webcam + Echo Dot speaker

| Role | Device | Linux path |
|---|---|---|
| Camera | Logitech C922 Pro Stream (USB, `046d:085c`) | V4L2 `/dev/video*` |
| Microphone | C922's built-in stereo mic | PipeWire source (ALSA card) |
| Speaker | Amazon Echo Dot over Bluetooth (A2DP) | PipeWire sink `bluez_output.*` |

The C922 is a **USB webcam, not a Pi Camera Module** — never use `rpicam-*` /
`libcamera-*` tools for it. Audio goes through **PipeWire**, which runs in the
desktop login session, so the Go brain must run as that login user (see
`fritz-pi-deploy`).

## Camera

### Find the device (don't hardcode /dev/video0)

The C922 creates two nodes (video + metadata) and numbers change with USB
order. Use the stable by-id path:

```sh
ls /dev/v4l/by-id/ | grep -i c922
# → usb-046d_C922_Pro_Stream_Webcam_<serial>-video-index0   ← use this one
```

`FRITZ_CAMERA_DEVICE` holds that path. If unset, the Go code globs
`/dev/v4l/by-id/*C922*-video-index0` and falls back to the first
`*-video-index0`.

### Formats

```sh
sudo apt install -y v4l-utils ffmpeg
v4l2-ctl -d "$FRITZ_CAMERA_DEVICE" --list-formats-ext
```

The C922 outputs **MJPEG** natively at up to 1080p, so the Pi never has to
encode JPEGs. Use MJPEG and `-c:v copy` — this is the most important CPU
saving on a Pi 4. Confirm the frame rates offered at your chosen size in the
`--list-formats-ext` output before picking one.

### Capture (one long-running ffmpeg, frames on stdout)

```sh
ffmpeg -hide_banner -loglevel error \
  -f v4l2 -input_format mjpeg -video_size 1280x720 -framerate 10 \
  -i "$FRITZ_CAMERA_DEVICE" \
  -c:v copy -f mjpeg -
```

- Read stdout and split frames on JPEG markers (`FF D8` … `FF D9`). Keep only
  the latest frame in an atomic slot; drop the rest.
- **Only one process can open the camera.** The Go brain owns it; every
  consumer (gaze tracking, `look`, `find_object`) reads the shared latest
  frame. Never start a second ffmpeg or a test tool while Fritz runs.
- 1280×720 frames go to Muse Spark (`look`) as-is. For SAM tracking, decode
  and downscale to 640 px wide in Go (`image/jpeg` + `golang.org/x/image/draw`,
  re-encode quality 80) — at 1–2 Hz this is cheap on a Pi 4.
- If ffmpeg exits (USB unplug), log once, show `skeptical` briefly, and
  restart with backoff (1 s → 10 s). Vision is optional; voice keeps working.

### Camera controls

Set these at startup (control names differ by kernel; list them with
`v4l2-ctl -d "$FRITZ_CAMERA_DEVICE" --list-ctrls` and skip ones that
don't exist):

```sh
v4l2-ctl -d "$FRITZ_CAMERA_DEVICE" -c power_line_frequency=2   # 60 Hz mains (Philippines) — stops flicker
v4l2-ctl -d "$FRITZ_CAMERA_DEVICE" -c focus_automatic_continuous=1   # older kernels: focus_auto=1
v4l2-ctl -d "$FRITZ_CAMERA_DEVICE" -c auto_exposure=3               # aperture priority (auto)
```

### Orientation

V4L2 gives the un-mirrored image. With the C922 clipped centered on top of
the display and facing the user, the gaze mapping in `fritz-vision` applies:
`gaze_x = (0.5 - cx) * 2`. Verify once live by moving left/right.

### Quick checks

```sh
ffmpeg -f v4l2 -input_format mjpeg -video_size 1280x720 -i "$FRITZ_CAMERA_DEVICE" -frames:v 1 /tmp/c922.jpg
v4l2-ctl --list-devices
```

## Microphone

PipeWire already exposes it as the default source ("C922 Pro Stream Webcam
Analog Stereo"). Capture 16 kHz mono s16le for Muse Voice Transcribe —
PipeWire downmixes and resamples:

```sh
arecord -D pipewire -f S16_LE -r 16000 -c 1 -t raw -q -
```

- Needs `pipewire-alsa` (`sudo apt install -y pipewire-alsa`); then
  `arecord -L | grep pipewire` lists it.
- `FRITZ_MIC` defaults to `pipewire`. To pick the C922 explicitly even if the
  default source changes: `wpctl status` → note the C922 source ID →
  `wpctl set-default <id>` once (WirePlumber remembers it).
- Level: `wpctl set-volume @DEFAULT_AUDIO_SOURCE@ 1.0`. The C922 mic is good
  at desk distance (≤ 1 m). Test:
  `arecord -D pipewire -f S16_LE -r 16000 -c 1 -d 4 /tmp/t.wav && pw-play /tmp/t.wav`.
- Fallback without PipeWire (e.g. a service outside the session):
  `arecord -l` → use `-D plughw:CARD=<card id>,DEV=0`. Don't mix both
  paths at once.

## Speaker: Echo Dot over Bluetooth

- It's the default PipeWire sink after pairing and `wpctl set-default`.
  Store the MAC in `FRITZ_SPEAKER_BT` in `/etc/fritz/fritz.env` — don't
  commit it.
- Playback of Piper output:
  `aplay -D pipewire -r 22050 -f S16_LE -c 1 -t raw -q` (rate must match the
  Piper voice's `.onnx.json`).
- WirePlumber config lives in `scripts/wireplumber/`; install it with
  `scripts/install-audio-config.sh`. It limits the Pi's Bluetooth roles to
  sending audio. Without that, the Echo can connect to the Pi as its
  speaker: the profile shows as `audio-gateway` and no `bluez_output` sink
  exists. It also stops the idle Echo sink from suspending.
- Volume: the Pi sink volume caps the Echo's. At startup run
  `wpctl set-volume @DEFAULT_AUDIO_SINK@ 1.0`. Don't go above 1.0
  (distortion).
- Latency: Bluetooth adds ~0.2–0.5 s. Send the face `speaking on` when
  playback starts; don't try to lip-sync.
- Reconnects: the Echo drops the link when someone uses Alexa. Every 30 s
  check `bluetoothctl info $FRITZ_SPEAKER_BT | grep "Connected: yes"`; if
  not connected, `bluetoothctl connect $FRITZ_SPEAKER_BT`. Before speaking,
  if the Echo sink is missing, fall back to the built-in audio sink rather
  than going silent, and log it.
- The Echo's microphones do **not** pass through Bluetooth. The C922 is the
  only mic.

## Echo avoidance (Fritz hearing itself)

The C922 mic will pick up Fritz's voice from the Echo. Half-duplex:

- While Fritz speaks, keep streaming to transcription but send **silence**
  instead of mic audio (the realtime API needs continuous frames).
- Keep muting for **700 ms after playback ends** — Bluetooth delay means
  the Echo is still playing after `aplay` exits.
- Barge-in ("Fritz, stop") then can't be heard while speaking; accept that
  for the hackathon.

## Hardware check script

Create `scripts/check-hardware.sh` that prints PASS/FAIL for: C922 present
in `/dev/v4l/by-id`, one MJPEG frame captured, PipeWire running
(`wpctl status`), C922 source present, 2 s recording non-silent, Echo
connected, Echo sink present. Run it first whenever something "doesn't
work" on the Pi.
