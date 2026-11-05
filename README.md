# Fritz

<p align="center">
  <img src="docs/fritz.jpg" alt="Fritz on a desk: a white 3D-printed robot with a screen showing two happy blue eyes, the Logitech C922 webcam on a stand beside him and the Echo Dot behind" width="380">
</p>

**A desk companion that guards your attention.** Fritz sits on your desk
with animated eyes, sees when you're there, listens when you talk to him,
and keeps track of what you'd otherwise miss: holding back email that can
wait while you focus, interrupting only for what can't, reminding you
where you stopped when you come back, and reaching you on WhatsApp when
you're away.

Built for the Meta Global AI Hackathon on a Raspberry Pi 4 Model B with Meta's
models: Muse Spark, SAM 3.1, Muse Voice Transcribe and Muse Wonder, and
coded with Muse Code and Claude Code.

**[Watch the 3-minute demo](https://youtu.be/18yHkounti0)**

## The problem

When you're heads-down, you either get pulled out by things that could
have waited, or you miss the one message that couldn't. Notification
settings don't know the difference, and they don't know you.

## What Fritz does

| You say or do | Fritz |
|---|---|
| "Let me focus for 30 minutes." | Holds email that can wait. Interrupts only for what can't wait until your focus ends ("sorry to interrupt — your card payment failed"). When time's up, one short spoken digest. |
| "Hold project updates unless there's a deadline." | Remembers the rule and applies it from the next email on. Rules show on the dashboard. |
| You sit down at your desk | His eyes find you and he says hello right away, then checks your email and calendar and mentions the one thing that matters ("Sarah from London needs an answer on Thursday today"). |
| You step away for a moment and come back | A one-line check-in: "All good? Anything I can help with?" You can just answer, no wake phrase needed. Never during focus time. |
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
| **Muse Wonder** | Overnight memory: folds the day's conversations into durable routines and preferences ("clears email around 9 AM"). Also the model behind Muse Code, one of the two coding assistants that built Fritz | `api/internal/memory/consolidate.go` |

## How it works

```mermaid
flowchart LR
  subgraph Pi["Raspberry Pi 4B"]
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
  it after an explicit yes (by voice or on WhatsApp).
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

Raspberry Pi 4 Model B · 3.5" GPIO display (the eyes) · Logitech C922 webcam
(camera and mic) · Amazon Echo Dot as a Bluetooth speaker.

**The body** (in the photo above) is white 3D-printed PLA: a head that
holds the Pi + screen behind a window, with ears and vents on its lid,
on a square base with wheels. Its final version was printed by a
third-party 3D printing service, which also reworked my round base into
the square one at the last minute (outside printing services are allowed,
confirmed with the organizers).

My original printable design is in `hardware/fritz-body/`, generated from
code (`build_body.py`, Python + manifold3d), with STLs in `stl/`: the
head and lid, plus the round base, floor and neck that the square base
replaced. PLA, 0.2 mm layers, no supports. The
Pi's USB-C power runs under the Pi (use a right-angle plug) and out a
slot low on the back of the head. Measure your Pi + screen before
printing: the sizes at the top of `build_body.py` are the only thing
that differs between builds. To regenerate: `pip install manifold3d
numpy && python build_body.py`.

## Repository

| Folder | What |
|---|---|
| `api/` | Go: voice loop, agent and tools, vision, alerts, memory, HTTP server |
| `iot/` | C++17 / SDL2: the animated eyes |
| `web/` | Next.js dashboard |
| `hardware/` | My original printable body design (parametric, Python + manifold3d) |
| `scripts/` | `run-fritz.sh` (start everything on the Pi), `preflight.sh`, `check-hardware.sh` |
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

## What's next

- **A turning head.** A servo in the base (a Hitec HS-422, driven from
  the Pi's hardware PWM on GPIO12) would turn the whole head toward you
  as you arrive. The original round base in `hardware/fritz-body` already
  has a mount for it, but there wasn't time to tune the motion for this
  submission.
- **Muse Gadgets** once the Muse app is available in the Philippines, as a
  second way to reach you away from the desk.

## Credits

Fritz uses these under their own licenses; everything else in this repo
was written during the hackathon.

- **Piper** text-to-speech with the `en_US-lessac-medium` voice (runs
  locally on the Pi).
- Open-source libraries: SDL2 (the eyes), chi, gorilla/websocket,
  Google's API client for Go, Next.js and React (the dashboard),
  manifold3d (the printable body). Full lists in `api/go.mod`,
  `web/package.json` and `iot/CMakeLists.txt`.
- The nine eye expressions are drawn in code after
  [easydrawingtips.com's eye-expressions tutorial](https://www.easydrawingtips.com/how-to-draw-eye-expressions-step-by-step/);
  no images from it are included.

## Built during the hackathon

Every commit in this repo is from the hackathon window (first commit
Oct 27, 2026): the code, the eyes, the dashboard and the body
design are all new. The final body was printed by an outside 3D printing
service (see Hardware). Fritz was built with two AI coding
assistants: **Muse Code** (running Muse Wonder) and **Claude Code**. Muse
Code worked on the code and reviewed the build as it grew. The agent skills in
`.agents/skills/` are the working notes both assistants read and kept up
to date.
