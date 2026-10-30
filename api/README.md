# Fritz — API (Go)

Go HTTP API using [chi](https://github.com/go-chi/chi). Serves the Android,
iOS, IoT, and web clients.

## Run

```sh
go run ./cmd/server        # listens on :8080 (override with ADDR=:9000)
go test ./...              # tests
go build -o bin/fritz-api ./cmd/server
```

## Voice test (camera + mic + speaker, on the Pi)

`cmd/voicetest` is a manual end-to-end hardware test: it grabs a C922
frame, records from the mic, transcribes with Muse Voice Transcribe,
and if you said "Hi Fritz" it replies with a canned line through Piper
→ the Echo Dot while the face turns happy. No agent/Muse Spark reply
is involved — it tests the chain, not the brain.

```sh
MODEL_API_KEY=... \
FRITZ_PIPER_VOICE=/opt/fritz/piper/en_US-lessac-medium.onnx \
  go run ./cmd/voicetest          # flags: -listen 5, -reply "...", -skip-camera
```

Run `scripts/check-hardware.sh` first if a step fails. Exit code is 0
only when every step passes.

For the realtime pipeline, `cmd/voicecheck` is the full voice loop:
it streams the mic to Muse Voice Transcribe live, and on
"Hi Fritz, ..." it asks Muse Spark for a reply and speaks it with
Piper through the speaker (face: thinking → happy, mic muted while
speaking). Follow-ups within 8 s need no wake phrase:

```sh
MODEL_API_KEY=... FRITZ_PIPER_VOICE=/opt/fritz/piper/<voice>.onnx \
  go run ./cmd/voicecheck   # Ctrl-C to stop
```

## Endpoints

| Method | Path       | Description    |
| ------ | ---------- | -------------- |
| GET    | `/`        | Service info   |
| GET    | `/healthz` | Health check   |

## Layout

- `cmd/server/main.go` — entry point (`ADDR` env, default `:8080`)
- `cmd/voicetest/main.go` — manual camera/mic/speaker test (above)
- `cmd/voicecheck/main.go` — realtime transcription check (above)
- `internal/voice/` — wake-phrase matching, ASR event parsing, the
  turn tracker (wake-only and 8 s follow-up window), the realtime
  Muse Voice Transcribe WebSocket client (muted = silence, for
  speaking without self-hearing), and Piper speech
- `internal/meta/` — Meta Model API client (Muse Spark chat; one retry
  on 429/5xx)
- `internal/httpapi/` — chi router and handlers
- `internal/face/` — UDP client for the C++ face (`face.Client`:
  `Emotion`/`Standby`/`Gaze`/`GazeOff`/`Speaking`/`Blink`, plus the
  assistant-state → expression mapping in `states.go`); fire-and-forget,
  send errors are logged once and never returned
