---
name: fritz-agent
description: >
  Fritz's brain: the Muse Spark tool-calling loop in Go, the system prompt and
  persona, the tool registry (email, calendar, vision, face, memory), the
  confirmation rule for actions, the memory store that learns the user's
  routines, and the nightly Muse Wonder consolidation. Use when working on
  api/internal/agent, api/internal/memory, adding a tool, changing Fritz's
  personality, or anything about what Fritz says or does.
---

# Fritz agent

Lives in `api/internal/agent/` and `api/internal/memory/`. Load
`meta-model-api` for request formats.

## Loop

```
turn text (from voice or web)
  → messages = system + memory snapshot + recent history + user turn
  → muse-spark-1.3 chat completion with tools
  → while finish_reason == "tool_calls":
        face: thinking
        run each tool (validate args!), append assistant msg + tool msgs
        call again
  → reply text → voice (TTS) and face (state from the reply)
```

- Max 6 tool rounds per turn; then answer with what you have.
- Keep the last ~10 turns as history; older context comes from memory.
- Every tool call and result goes to an activity log (SQLite) that the web
  dashboard shows. This is the "what did Fritz do" view in the demo.
- Ask the model to end its reply with a face hint so the face matches the
  words: prefer a structured `set_face(emotion)` tool over parsing text.

## System prompt essentials

- Fritz is a desk companion: brief, warm, a little playful. Replies are
  spoken, so 1–3 short sentences, no markdown, no lists, no URLs.
- It knows the time, the user's timezone, whether they're present, and the
  memory snapshot.
- It never gives medical, mental-health, legal, or financial advice, and
  never describes the user's personality, mood, or mental state. (These are
  hackathon-banned categories *and* the wrong product.) It may notice
  routines: "you usually clear email around 9".

## Tools

| Tool | Package | Notes |
|---|---|---|
| `gmail_search(query, max)` | integrations | Summaries only: from, subject, date, snippet |
| `gmail_read(id)` | integrations | Full body, truncated to ~4k chars |
| `gmail_draft_reply(id, body)` | integrations | Creates a draft; returns `draft_id` |
| `gmail_send_draft(draft_id)` | integrations | **Needs confirmation** |
| `calendar_list(from, to)` | integrations | |
| `calendar_create(title, start, end)` | integrations | **Needs confirmation** |
| `look(question)` | vision | Camera frame + Muse Spark |
| `find_object(name)` | vision | SAM 3.1; eyes turn to it |
| `set_face(emotion)` | face | |
| `remember(fact, kind)` | memory | kind: `routine`, `preference`, `person`, `task` |
| `recall(query)` | memory | |
| `forget(id)` | memory | |

Each tool: a Go func with a typed args struct, a JSON schema generated next
to it, and a unit test with a fake backend.

## Confirmation rule (enforce in code)

Tools marked **Needs confirmation** don't execute. They return
`{"status":"needs_confirmation","summary":"Send reply to Sarah: 'Thursday works'"}`
and store a pending action. Fritz reads the summary aloud. The pending action
runs only if the **user's next turn** is a clear yes ("yes", "send it", "do
it"), checked by Go code, not by the model, **or** the user taps Approve in
the Android app. A spoken non-yes cancels it. The model cannot bypass this by
calling the tool again. Pending actions live in the shared store described in
`fritz-mobile-backend` (atomic approve, 15 min expiry).

## Memory ("studies me")

- SQLite (`modernc.org/sqlite`, pure Go so cross-compiling stays
  CGO-free). Table `memories(id, kind, text, source, confidence,
  created_at, updated_at)`.
- Learn routines and preferences from interactions and email/calendar
  patterns: who matters, when the user works, how they like replies worded.
  Never store personality, health, or mood judgments.
- Snapshot injected into the system prompt: top ~30 memories by
  confidence and recency.
- Nightly job (or `POST /memory/consolidate` for the demo): send the day's
  activity log + current memories to `muse-wonder-1.0` asking for a JSON
  diff (add/update/remove) using structured output
  (https://dev.meta.ai/docs/structured-output.md). Apply it and log it.
- The web dashboard lists every memory with edit/delete. Expose
  `GET/PATCH/DELETE /memories` on the API.

## Proactive behavior

When presence goes from absent → present and there are unread important
emails or a meeting within 30 min, Fritz speaks first (once per
arrival): face `surprised` for urgent, `normal` otherwise. A rate limit of
one unprompted message per 15 min keeps it from nagging.
