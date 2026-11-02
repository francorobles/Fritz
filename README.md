# Fritz

**A desk companion that guards your attention.** Fritz sits on your desk
with animated eyes, sees when you're there, listens when you talk to him,
and keeps track of what you'd otherwise miss: holding back email that can
wait while you focus, interrupting only for what can't, reminding you
where you stopped when you come back, and reaching you on WhatsApp when
you're away.

Built for the Meta Global AI Hackathon on a Raspberry Pi 5 with Meta's
models: Muse Spark, SAM 3.1, Muse Voice Transcribe and Muse Wonder.

## The problem

When you're heads-down, you either get pulled out by things that could
have waited, or you miss the one message that couldn't. Notification
settings don't know the difference, and they don't know you.

## What Fritz does

| You say or do | Fritz |
|---|---|
| "Let me focus for 30 minutes." | Holds email that can wait. Interrupts only for what can't wait until your focus ends ("sorry to interrupt — your card payment failed"). When time's up, one short spoken digest. |
| "Hold project updates unless there's a deadline." | Remembers the rule and applies it from the next email on. Rules show on the dashboard. |
| "I'm stopping at the pricing section." Then you leave. | When the camera sees you sit back down, his eyes turn to you: "Welcome back. You stopped at the pricing section, and Dana sent the weekly update while you were out." |
| An important email arrives while you're away | A WhatsApp message. Reply there ("what does it say?", "tell her Thursday works") and Fritz answers with the same tools he has at the desk. |
| "Reply to Dana that Thursday works." | Reads the reply back and sends it only after you say yes. This is enforced in code, not left to the model. |
| "Where are my keys?" | Looks with the camera, turns his eyes to them, or says where and when he last saw them. |
| "Sentry mode, I'm heading out." | Anyone who walks up to your desk gets you a WhatsApp photo. |

Everything Fritz did, what he's holding for you, and what he has learned
about your routines is on a live dashboard (`web/`), with each entry
tagged with the Meta model behind it.

## Meta models

| Model | What it does in Fritz | Code |
|---|---|---|
| **Muse Spark 1.3** | The agent: conversation, tool calling (email, calendar, focus, rules, sentry), deciding which email is important, urgent, or held by your rules, and answering questions about what the camera sees | `api/internal/agent`, `api/internal/alerts/mail.go`, `api/internal/chat` |
| **SAM 3.1** | Fritz's eyes: finds you in the camera frame about once a second so his eyes follow you and he knows when you arrive or leave, and finds named objects ("keys") anywhere on the desk | `api/internal/vision` |
| **Muse Voice Transcribe** | His ears: realtime streaming speech-to-text from the webcam mic | `api/internal/voice` |
| **Muse Wonder** | Overnight memory: folds the day's conversations into durable routines and preferences ("clears email around 9 AM") | `api/internal/memory/consolidate.go` |

## How it works

```mermaid
flowchart LR
  subgraph Pi["Raspberry Pi 5"]
    mic["C922 mic"] --> voice["voice loop (Go)"]
    cam["C922 camera"] --> vision["vision: presence, gaze, objects"]
    voice <--> agent["agent loop + tools"]
    vision --> agent
    alerts["alerts: mail triage, focus, sentry"] --> agent
    agent --> tts["Piper TTS"] --> echo["Echo Dot (Bluetooth)"]
    agent --> face["face (C++ / SDL2)<br/>animated eyes"]
    vision --> face
    db[("SQLite: memory, state,<br/>activity, pending actions")]
    agent <--> db
    alerts <--> db
    server["HTTP server"] <--> db
  end
  voice -. realtime .-> transcribe["Muse Voice Transcribe"]
  agent -. tools .-> spark["Muse Spark 1.3"]
  vision -. ~1 Hz .-> sam["SAM 3.1"]
  db -. nightly .-> wonder["Muse Wonder"]
  agent <--> google["Gmail + Calendar"]
  server <--> wa["WhatsApp Cloud API"]
  server <--> web["dashboard (Next.js)"]
```

- **Two processes, one database.** `voicecheck` owns the mic, camera,
  eyes and watchers; `fritz-server` serves the dashboard and the WhatsApp
  webhook. They share state (focus, sentry, held messages, the WhatsApp
  24-hour window) through SQLite.
- **The face never touches the network.** The C++ eyes only obey UDP
  commands from the Go side on `127.0.0.1:7070`.
- **Confirmation is code, not a prompt.** Tools that send email or
  create events only file a pending action; a separate confirmer executes
  it after an explicit yes (by voice, on WhatsApp, or in the phone app).
- **Built for a live demo.** Replies stream sentence by sentence into
  local TTS, a short spoken acknowledgement covers tool calls, and a pause
  mid-sentence doesn't cut you off. SAM rate limits pause all camera calls
  together instead of stalling tracking.

## Measuring the interruption decisions

`api/cmd/triageeval` runs 18 hand-labelled emails (a focus session plus
the rule "hold project updates unless there's a deadline") through the
real Muse Spark email check and scores each decision as interrupt, hold or
ignore:

```sh
cd api && MODEL_API_KEY=... go run ./cmd/triageeval -runs 3
```

<!-- Paste the latest score here, e.g. "Latest: 16/18, 0 missed interruptions". -->

## Privacy and safety

- No face recognition: Fritz knows *someone* is there, not who.
- Memories are routines and preferences only, never judgments about
  personality, health or mood. You can see and delete every one on the
  dashboard.
- Email text is treated as data: instructions inside an email are never
  followed.
- Personal API routes need an app token, and only the WhatsApp webhook
  path is exposed to the internet. Secrets live in `/etc/fritz/fritz.env`,
  never in the repo.

## Hardware

Raspberry Pi 5 · 3.5" display (the eyes) · Logitech C922 webcam (camera
and mic) · Amazon Echo Dot as a Bluetooth speaker. A 3D-printable desk body
is in `hardware/fritz-body`.

## Repository

| Folder | What |
|---|---|
| `api/` | Go: voice loop, agent and tools, vision, alerts, memory, HTTP server |
| `iot/` | C++17 / SDL2: the animated eyes |
| `web/` | Next.js dashboard |
| `hardware/` | Printable body (parametric, Python + manifold3d) |
| `scripts/` | `run-fritz.sh` (start everything on the Pi), `preflight.sh`, `check-hardware.sh` |
| `android/` | Companion app (push approvals). Optional, not part of the demo: WhatsApp covers away mode |
| `.agents/skills/` | Working notes for the coding agents that helped build Fritz |

## Run it

On the Pi, with `/etc/fritz/fritz.env` holding `MODEL_API_KEY`,
`FRITZ_APP_TOKEN` and the WhatsApp secrets, and Google OAuth set up
(`go run ./cmd/googleauth`; see `.agents/skills/fritz-integrations`):

```sh
./scripts/preflight.sh          # everything green before you start
./scripts/run-fritz.sh          # eyes, API server, then the voice loop
./scripts/run-fritz.sh --demo   # recording timings
cd web && npm ci && npm run build && FRITZ_APP_TOKEN=... npm start
```

Checks: `cd api && go vet ./... && go test ./...`,
`cmake -S iot -B iot/build && cmake --build iot/build`,
`cd web && npm run build`.

## Built during the hackathon

Fritz was built for the Meta Global AI Hackathon, with Claude Code as a
coding assistant.
