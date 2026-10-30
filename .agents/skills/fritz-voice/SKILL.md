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
C922 mic → arecord -D pipewire (16 kHz s16le mono, stdout)
  → realtime WS wss://api.meta.ai/v1/asr/realtime  (ENDPOINTING)
  → speechComplete turns
  → wake-phrase filter → agent → reply text
  → Piper TTS → aplay -D pipewire → Echo Dot (Bluetooth)
                                    (face: speaking on/off)
```

Hardware details (device names, levels, Bluetooth reconnects) are in
`fritz-c922`.

## Capture

- `arecord -D "$FRITZ_MIC" -f S16_LE -r 16000 -c 1 -t raw -q -` and read
  stdout in 100 ms chunks (3,200 bytes).
- `FRITZ_MIC` defaults to `pipewire` (the C922 is the default source).
- Same command works on a Linux laptop with PipeWire.

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
  aplay -D pipewire -r 22050 -f S16_LE -c 1 -t raw -q` (the sample rate must
  match the voice's `.onnx.json`). Output goes to the Echo Dot.
- Speak sentence by sentence as the agent's reply arrives, so the first words
  start sooner; the prompt asks for a short first sentence. Splitting the
  first chunk at a comma (`FRITZ_FIRST_CLAUSE=1`) is off by default: it
  stalls mid-sentence and sounds choppy.
- Every sentence goes through `voice.Speakable` (strips markdown, emoji,
  URLs, email addresses) before piper, so symbols are never read aloud.
- Tune the voice without a rebuild: `FRITZ_PIPER_ARGS="--length_scale 0.95
  --noise_w 0.9 --sentence_silence 0.15"`.
- `[pause 0.6s before "..."]` in the log = playback ran dry mid-reply
  (voice too heavy for the Pi, or a slow tool).
- `voice.Engine` keeps ONE piper (`--output_dir`, one WAV per line) and ONE
  aplay stream open for the whole session, writing silence between replies
  so the Bluetooth sink never sleeps. Don't go back to a piper per reply:
  that reloads the voice model and wakes the Echo every answer (~1 s).
  `FRITZ_PIPER_PERSIST=0` falls back to per-reply Piper for comparison.
- If Spark's first sentence takes longer than `FRITZ_ACK_AFTER_MS` (default
  600, `0` = off), a pre-rendered "Mm-hm." / "One sec." plays first.
- Local endpointing waits `FRITZ_STALL_MS` (default 900) of unchanged
  partials. Text that looks mid-thought (trailing "…coming from",
  "…check if there's") waits 3x as long, floor 2.5 s
  (`voice.StallWait`) — a paused sentence is not a finished one, and
  answering the fragment feels like being interrupted.
- `voicecheck` prints a `[timing]` line per reply (end of speech → request,
  Spark → first sentence, Piper → first audio). Measure before tuning.
- Face: `speaking on` before the first chunk, `speaking off` after the last.
- While speaking, and for 700 ms after (Bluetooth delay), send silence
  instead of mic audio so Fritz doesn't hear itself through the C922 — see
  "Echo avoidance" in `fritz-c922`. Stopping Fritz mid-sentence is done from
  the phone app, not by voice.

## Testing

- Unit-test the wake-phrase filter and the turn state machine with fake
  events; no network in `go test`.
- Manual: `go run ./cmd/voicecheck` (create it) that streams the mic and
  prints transcripts — the first thing to get working on the Pi.
