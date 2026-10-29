---
name: meta-model-api
description: >
  Reference for calling Meta's Model API from Fritz (Go code in api/): base URL,
  auth, model IDs, Chat Completions tool calling, image input, Muse Voice
  Transcribe, SAM 3.1 segmentation, Muse Image, and which open-weight models fit
  where. Use before writing or debugging any code that calls muse-spark,
  muse-wonder, muse-image, muse-voice-transcribe, or sam-3.1, or when choosing
  which Meta model to use for a feature.
---

# Meta Model API for Fritz

Source of truth: https://dev.meta.ai/docs/models (add `.md` to any docs URL
for a plain-text version). When this file and the docs disagree, the docs win
— update this file.

## Connection

- Base URL: `https://api.meta.ai/v1`
- Header: `Authorization: Bearer $MODEL_API_KEY`
- OpenAI-compatible: Chat Completions, Responses and Messages protocols.
- Env var in Fritz: `MODEL_API_KEY`. Read it once at startup and fail fast if
  it's missing. Never log it.

## Which model for which Fritz job

| Job | Model ID | Why |
|---|---|---|
| Conversation + tool calling (realtime) | `muse-spark-1.3` | Recommended model, tool calling, image input |
| Audio *input* to an LLM (rare; prefer transcription) | `muse-spark-1.2` | 1.3's audio input is not fully supported |
| Nightly memory consolidation ("studies me") | `muse-wonder-1.0` | Biggest reasoning model; preview; latency doesn't matter overnight |
| Speech → text | `muse-voice-transcribe-1.0` | File and realtime WebSocket. **No speech synthesis** |
| Find a person/object in a camera frame | `sam-3.1` | Text prompt → boxes + masks; tracks across video frames |
| Describe/understand a camera frame | `muse-spark-1.3` with `image_url` | General vision Q&A |
| Generate an image (optional, low priority) | `muse-image-1.0` | `/v1/images/generations` |

Open-weight: **Muse Glimmer** is 30B (edge-oriented but far too big for a Pi 4's
RAM) and **Muse Spark Open** is 816B MoE. Neither runs on the Pi. Only use
Glimmer if a GPU host is available on the LAN, as an optional local
email-redaction step; don't block the demo on it.

`-contributor` variants are cheaper but prompts may be used for training. Fritz
handles personal email — **use the standard (non-contributor) IDs.**

## Chat Completions + tool calling

`POST /v1/chat/completions`. Request shape (OpenAI format):

```json
{
  "model": "muse-spark-1.3",
  "messages": [{"role": "system", "content": "..."}, {"role": "user", "content": "..."}],
  "tools": [{
    "type": "function",
    "function": {
      "name": "gmail_search",
      "description": "Search the user's inbox.",
      "parameters": {"type": "object", "properties": {"query": {"type": "string"}}, "required": ["query"]}
    }
  }],
  "tool_choice": "auto"
}
```

Rules from the docs (violations return HTTP 400):

- `tool_choice` must be `"auto"`.
- When the reply has `finish_reason: "tool_calls"`, append **the whole
  assistant message including `tool_calls`**, then one
  `{"role":"tool","tool_call_id":...,"content":...}` per call, then call again.
- `parallel_tool_calls` defaults to true; handle several calls per turn.
- `arguments` is a JSON *string*. Parse and validate it before use.
- Streaming: accumulate `tool_calls` deltas by `index`.

Image input (camera frame) — a user message with content parts:

```json
{"role": "user", "content": [
  {"type": "text", "text": "What is the user holding?"},
  {"type": "image_url", "image_url": {"url": "data:image/jpeg;base64,<...>"}}
]}
```

Go: use plain `net/http` + `encoding/json` structs in `api/internal/meta/`
(one small client; no SDK needed). Set a timeout per call (30 s chat, 10 s
vision) and retry `429`/`5xx` once with backoff.

## Muse Voice Transcribe

File: `POST /v1/asr/transcribe`, multipart:

```bash
curl --fail-with-body "https://api.meta.ai/v1/asr/transcribe" \
  -H "Authorization: Bearer $MODEL_API_KEY" \
  -F 'request={"mode":"ENDPOINTING","model":"muse-voice-transcribe-1.0","audioEncoding":"WAV"};type=application/json' \
  -F "audio=@recording.wav"
```

WAV must be mono, 16-bit PCM, 16 or 24 kHz, ≤ 10 min, ≤ 32 MB. Response:
`{"sessionId","transcript","audioDurationMs","turns":[{"turnId","startMs","endMs","transcript"}]}`.

Realtime: `wss://api.meta.ai/v1/asr/realtime` (the HTTP auth header is
ignored here). First text frame within 10 s:

```json
{"authorization":{"accessToken":"Bearer <KEY>"},"audioEncoding":"PCM_16KHZ",
 "model":"muse-voice-transcribe-1.0","mode":"ENDPOINTING","partialMode":"CUMULATIVE"}
```

Wait for `{"sessionId":...}` (no `type` field), then send binary frames of raw
s16le mono PCM paced at real time (≤ 5 s ahead; send silence during silence).
Events have `type`: `speechStart`, `transcript` (`final`), `speechEnd`,
`speechComplete` (use this as end of utterance in ENDPOINTING), `error`.
Ignore unknown types. Sessions last ≤ 60 min — reconnect before that. Close
code `1013` = rate limited; back off. Finish with `{"type":"endStream"}`.

## SAM 3.1 segmentation

`POST /v1/responses` with `model: "sam-3.1"`, one user message holding an
`input_text` noun phrase (`"person"`, `"face"`, `"keys"`) and an `input_image`
(`image_url`, data URLs for camera frames). Output is special-token text with
one box and one mask per match.

**The box/mask token format is not documented in the page we read.** Before
writing the parser: send one real frame, log the raw output text, save it as
a test fixture under `api/internal/vision/testdata/`, and write the parser
plus a unit test against that fixture. Check
https://dev.meta.ai/docs/media-segmentation.md for the "reading segmentation
output" page first.

## Muse Image (optional)

`POST /v1/images/generations` with `model: "muse-image-1.0"`. Only build this
if core features are done.

## Pricing and limits

Check https://dev.meta.ai/docs/pricing-rate-limits.md before running anything
continuous (realtime ASR, camera loops). Rate-limit vision to ≤ 2 requests/s.
