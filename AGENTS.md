# Fritz — project instructions for coding agents

Fritz is a desk companion for the Meta Global AI Hackathon (submission
deadline **Nov 4, 2026, 9 PM ET**). It runs on a Raspberry Pi 5 with a
display (animated EVE-style eyes), a Logitech C922 USB webcam (camera and
microphone), and an Echo Dot as a Bluetooth speaker. You talk to it by voice; it sees you, follows you with its eyes,
reads your email and calendar, guards your focus (holds what can wait,
interrupts for what can't), remembers your routines, and reaches you on
WhatsApp when you're away.

## Architecture (decided — don't redesign)

```
            Raspberry Pi 5
 ┌──────────────────────────────────────────────────────┐
 │ iot/  fritz_iot (C++/SDL2)   ◄── UDP 127.0.0.1:7070 ──┤ face commands
 │       fullscreen eyes                                 │
 │                                                       │
 │ api/  voicecheck (Go) ── the brain, + fritz-server    │
 │   C922 mic      → Muse Voice Transcribe (realtime WS) │
 │   agent loop    → Muse Spark 1.3 tool calling         │
 │   C922 camera (ffmpeg) → SAM 3.1 / Muse Spark vision  │
 │   speech out    → Piper TTS (local) → Echo Dot (BT)   │
 │   Gmail + Calendar, memory store (SQLite)             │
 │   alerts: mail triage, focus mode, sentry             │
 │   HTTP :8080 (fritz-server) for web/ + WhatsApp hook  │
 └──────────────────────────────────────────────────────┘
 web/      Next.js dashboard: focus, held messages, rules, activity
           (with Meta model badges), memories
 WhatsApp  away channel: alerts out, replies in (webhook via Tailscale
           Funnel, webhook path only)
 android/  Kotlin companion — scaffold only, not in the demo
```

- `ios/` is out of scope for the hackathon. Don't touch it.
- The Muse app isn't available in the Philippines, so Muse Gadgets isn't
  used; WhatsApp is Fritz's link to the user away from the desk.
- Run on the Pi with `scripts/run-fritz.sh` (`--demo` for recordings);
  check with `scripts/preflight.sh` (`fritz-pi-deploy`).
- All Meta Model API calls happen in Go (`api/`). The C++ face never calls
  the network; it only obeys face commands.

## Skills — load the one that matches your task

| Skill | Use for |
|---|---|
| `meta-model-api` | Any call to Muse Spark, Muse Wonder, Muse Image, Muse Voice Transcribe, SAM 3.1 |
| `fritz-eyes` | The C++ face, the UDP face protocol, emotion ↔ state mapping |
| `fritz-c922` | The C922 webcam (camera + mic) and Echo Dot speaker: devices, capture/playback commands, echo avoidance |
| `fritz-vision` | Camera frames → presence detection, eyes following you, "where is X" |
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
