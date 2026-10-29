---
name: fritz-voice
description: >
  Fritz's voice pipeline: microphone capture on the Pi, streaming
  speech-to-text with Muse Voice Transcribe (realtime WebSocket), the "Fritz"
  wake phrase, turn-taking, and spoken replies with local Piper TTS. Use when
  working on api/internal/voice, audio devices, transcription, the wake phrase,
  barge-in, or text-to-speech.
---

# Fritz voice

Lives in `api/internal/voice/`. Load `meta-model-api` for the exact
transcription protocol.

## Pipeline

```
arecord (16 kHz s16le mono, stdout)
  → realtime WS wss://api.meta.ai/v1/asr/realtime  (ENDPOINTING)
  → speechComplete turns
  → wake-phrase filter → agent → reply text
  → Piper TTS → aplay      (face: speaking on/off)
```

## Capture

- `arecord -D "$FRITZ_MIC" -f S16_LE -r 16000 -c 1 -t raw -q -` and read
  stdout in 100 ms chunks (3,200 bytes).
- `FRITZ_MIC` defaults to `default`; list devices with `arecord -l`.
- Desktop fallback: same command works with PipeWire/PulseAudio.

## Transcription

- Handshake `audioEncoding: "PCM_16KHZ"`, `mode: "ENDPOINTING"`,
  `partialMode: "CUMULATIVE"`. Wait for the `sessionId` ack before sending
  audio.
- Pace frames at real time. While muted (e.g., Fritz is speaking) keep sending
  silence rather than stopping — the docs require it on a live source.
- Show partials on the face: on `speechStart` while awake → `focused`.
- A turn is done on `speechComplete` for its `turnId`.
- Reconnect before 60 min, and on `1013`/errors with exponential backoff
  (1 s → 30 s).

## When to listen

Streaming all day is costly and a privacy problem. Only open the stream when
`fritz-vision` reports the user is present; close it ~30 s after they leave.
This presence-gated listening is a feature to mention in the demo.

## Wake phrase

No separate wake-word model: Fritz reacts to a `speechComplete` transcript
that starts with or contains "Fritz" (case-insensitive; also accept common
mis-hearings like "Fritts", "Frits" — collect real ones from logs). Pass
`keywords: ["Fritz"]` in the handshake if the API reference supports it to
bias recognition.

- Wake → face `blink` + `focused`. Strip the wake word and send the rest to
  the agent. If the turn was only "Fritz", play a short ack and treat the next
  turn as the request.
- Follow-up window: for 8 s after Fritz finishes speaking, accept turns without
  the wake word.

## Speaking (Piper, local)

Meta's API has no speech synthesis, so Fritz uses Piper (offline, runs on the
Pi 4). Say so in the README and demo — it isn't a Meta model.

- `echo "$TEXT" | piper --model "$FRITZ_PIPER_VOICE" --output-raw |
  aplay -r 22050 -f S16_LE -c 1 -t raw -q` (the sample rate must match the
  voice's `.onnx.json`).
- Speak sentence by sentence as the agent's reply arrives, so the first words
  start sooner.
- Face: `speaking on` before the first chunk, `speaking off` after the last.
- While speaking, drop incoming turns to avoid Fritz hearing itself; if the
  user says "Fritz, stop", kill the aplay process (barge-in).

## Testing

- Unit-test the wake-phrase filter and the turn state machine with fake
  events; no network in `go test`.
- Manual: `go run ./cmd/voicecheck` (create it) that streams the mic and
  prints transcripts — the first thing to get working on the Pi.
