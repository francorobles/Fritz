# Fritz — project instructions for coding agents

Fritz is a desk companion for the Meta Global AI Hackathon (submission
deadline **Nov 4, 2026, 9 PM ET**). It runs on a Raspberry Pi 4B with a
display (animated EVE-style eyes), a USB microphone, a speaker, and a Pi
camera. You talk to it by voice; it sees you, follows you with its eyes,
reads your email and calendar, and remembers your routines.

## Architecture (decided — don't redesign)

```
            Raspberry Pi 4B
 ┌──────────────────────────────────────────────────────┐
 │ iot/  fritz_iot (C++/SDL2)   ◄── UDP 127.0.0.1:7070 ──┤ face commands
 │       fullscreen eyes                                 │
 │                                                       │
 │ api/  fritz (Go)  ── the brain                        │
 │   mic (arecord) → Muse Voice Transcribe (realtime WS) │
 │   agent loop    → Muse Spark 1.3 tool calling         │
 │   camera (rpicam-still) → SAM 3.1 / Muse Spark vision │
 │   speech out    → Piper TTS (local) → aplay           │
 │   Gmail + Calendar, memory store (SQLite)             │
 │   HTTP :8080 for web/ and android/                    │
 │   FCM pushes to the phone when the user is away       │
 └──────────────────────────────────────────────────────┘
 web/      Next.js dashboard: what Fritz remembers, activity log
 android/  Kotlin companion: push alerts, Approve/Decline, push-to-talk
           (reaches the Pi over Tailscale HTTPS)
```

- `ios/` is out of scope for the hackathon. Don't touch it.
- The Muse app isn't available in the Philippines, so Muse Gadgets isn't
  used; the Android app is Fritz's link to the user away from the desk.
- All Meta Model API calls happen in Go (`api/`). The C++ face never calls
  the network; it only obeys face commands.

## Skills — load the one that matches your task

| Skill | Use for |
|---|---|
| `meta-model-api` | Any call to Muse Spark, Muse Wonder, Muse Image, Muse Voice Transcribe, SAM 3.1 |
| `fritz-eyes` | The C++ face, the UDP face protocol, emotion ↔ state mapping |
| `fritz-vision` | Pi camera, presence detection, eyes following you, "where is X" |
| `fritz-voice` | Mic capture, transcription, wake phrase, TTS |
| `fritz-agent` | The Muse Spark tool-calling loop, tools, memory, guardrails |
| `fritz-integrations` | Gmail and Google Calendar (OAuth, tools) |
| `fritz-mobile` | The Android app in android/: push alerts, Approve/Decline, push-to-talk |
| `fritz-mobile-backend` | Go side for the phone: mobile API, pending actions, FCM pushes, Tailscale |
| `fritz-pi-deploy` | Cross-compiling, systemd, devices, secrets on the Pi |
| `fritz-hackathon` | Rules, banned topics, judging, scope cuts, demo and submission |

## Ground rules

- Secrets come from the environment (`MODEL_API_KEY`, Google OAuth files).
  Never commit keys, tokens, or recordings. The repo must stay public.
- Read the existing code before editing and match its style: C++17 with
  `fritz::` namespace and comments explaining *why*; Go with stdlib +
  chi, small packages under `api/internal/`.
- Build checks: `cmake -S iot -B iot/build && cmake --build iot/build`,
  `cd api && go vet ./... && go test ./...`, `cd web && npm run build`,
  `cd android && ./gradlew :app:assembleDebug :app:testDebugUnitTest`.
- Anything that sends an email or creates an event must be confirmed by
  the user out loud first, enforced in code (see `fritz-agent`).
