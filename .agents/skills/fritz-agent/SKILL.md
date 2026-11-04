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

Built so far: `agent.Agent.Run` streams Spark's reply (text is spoken
as it arrives, across tool rounds), runs tool calls, and returns tool
errors to the model as `{"error": ...}` instead of failing the turn.
`meta.Client.ChatStreamTools` assembles streamed `tool_calls` deltas.
`voicecheck` wires in the tools from `integrations.json`; a provider
that isn't connected means its tools aren't offered.
Add a tool: a `Tool{Name, Description, Parameters, Run}` with strict
argument decoding (`decodeArgs`) and a fake-backed test. A gated
(world-changing) tool also needs an executor registered in
`agent.Executors(reg)` — voice, WhatsApp and the /actions routes all share that
one map.

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
  Built: `api/internal/activity` (table `activity` in the shared db),
  fed by `Agent.AfterTool` (tool calls) and `Confirmer.Activity`/the
  `/actions` handlers (confirmed, declined, failed actions); served at
  `GET /activity?limit=`. Entries keep a ≤300-char detail, never full
  email bodies.
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
| `email_search(query, max)` | agent/mail.go | Built. All connected mailboxes merged newest-first; summaries only (from, subject, date, unread, snippet, account + id) |
| `email_read(account, id)` | agent/mail.go | Built. Full body, truncated to 4k chars |
| `email_reply(account, id, body)` | agent/reply.go | Built. **Needs confirmation**: summary names the recipient and quotes the body; sends in-thread on yes (Gmail only — `integrations.MailReplier`) |
| `calendar_list(from, to)` | agent/calendar.go | Built |
| `calendar_create(title, start, end)` | agent/calendar.go | Built. **Needs confirmation** |
| `look(question)` | — | Not a tool: `voicecheck` attaches the live frame to the user message when `looksVisual(request)` matches ("what am I holding?"), and Spark answers from the photo |
| `find_object(name)` | agent/vision.go | Built. SAM 3.1 live; eyes turn to it for 4 s; otherwise last-seen place/time from the watch-list memory |
| `sentry_mode(on)` | agent/sentry.go | Built. Flag in the shared DB (`alerts.KeySentry`); voice (needs camera + WhatsApp) and WhatsApp. Turning it off by voice also WhatsApps the owner |
| `focus_mode(action, minutes)` | agent/focus.go | Built. start/stop/status; `alerts.Focus` (`focus_until`, held queue in the shared DB). stop/status return the held messages |
| `interruption_rule(rule)` | agent/focus.go | Built. Stored as a `preference` memory prefixed `alerts.RulePrefix` (dashboard-visible, deletable); the mail judge reads rules at every check |
| `save_progress(task, next)` / `where_was_i()` | agent/focus.go | Built. `alerts.Progress` in the shared DB; the arrival greeting mentions it once |
| `set_face(emotion)` | face | Not built: `voicecheck` sets emotions itself (thinking during tools, happy when speaking) |
| `remember` / `recall` / `forget` | memory | Not built as tools: memories come from Wonder consolidation, `interruption_rule`, and the dashboard (`DELETE /memories/{id}`) |

Each tool: a Go func with a typed args struct, a JSON schema generated next
to it, and a unit test with a fake backend.

Fast confirmations: a tool may set `Confirm(out) string`. When every
call in a round succeeded and confirmed itself, the loop speaks those
lines and ends the turn without a second Spark round (about half the
latency). Used by `focus_mode start`, `interruption_rule`,
`save_progress`, `sentry_mode`, and the gated `calendar_create` /
`email_reply` (they only ask: their `spoken` question — the full reply
body, or the event with a spoken time from `spokenWhen` — is said as-is,
and the Go confirmer still decides on the yes); anything that returns data to
summarize (email, calendar, focus status, where_was_i) returns "" or has
no Confirm, so the model still phrases it.

WhatsApp replies (`internal/chat.Brain`, in `cmd/server`) run the same agent
loop with mail, email_reply, calendar and `sentry_mode` (no camera tools);
tool calls go to the activity log with source `whatsapp`.

## Confirmation rule (enforce in code)

Tools marked **Needs confirmation** don't execute. They return
`{"status":"needs_confirmation","summary":"Send reply to Sarah: 'Thursday works'"}`
and store a pending action. Fritz asks the tool's fixed `spoken` question
("Should I reply to Sarah: Thursday works? Just say yes…") without a
second model round. The pending action
runs only if the **user's next turn** is a clear yes ("yes", "send it", "do
it"), checked by Go code, not by the model, **or** a WhatsApp yes. A spoken non-yes cancels it. The model cannot bypass this by
calling the tool again. Pending actions live in the shared store in
`api/internal/actions` (atomic approve, 15 min expiry).

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
- The web dashboard (`web/`) lists every memory with delete (PATCH exists
  on the API, not in the UI). `GET/PATCH/DELETE /memories` need the app
  token when `FRITZ_APP_TOKEN` is set; the dashboard's server-side proxy
  (`web/app/api/fritz/[...path]/route.ts`) adds it.

## Proactive behavior

Built (arrival greeting, `voicecheck`): when the tracker reports present
after the user was away ≥ `FRITZ_GREET_AWAY_MS` (default 60 s; the first
sighting after startup also counts), Fritz speaks first via a *proactive*
turn: `arrivalPrompt` asks for a one-sentence hello plus the single most
important thing (the model checks unread mail from the last 12 h and the
next hour of calendar with its tools), and mentions actions finished
while the user was away (activity log: approved on WhatsApp/phone). At
most once per `FRITZ_GREET_EVERY_MS` (default 10 min); skipped mid-
conversation; `FRITZ_GREET=off` disables. Proactive turns skip the
confirmer, never attach a camera frame, and store only Fritz's line in
the conversation history. The follow-up window opens after it, so the
user can answer without the wake word.

Built (alerts, `internal/alerts`, run by `voicecheck`):

- **Important email.** `MailWatcher` polls new unread Gmail every
  `FRITZ_MAIL_POLL_MS` (default 2 min; promotions/social/forums skipped,
  only mail newer than startup) and asks Muse Spark to triage it
  (`judgePrompt`: real people needing a reply, deadlines, money and
  security problems; "when unsure, not important"; JSON only, ids
  checked against what was asked). Each email is judged once (`alerts`
  table). At the desk (tracker sees someone, sentry off) Fritz says it in
  a proactive turn; otherwise it goes to WhatsApp (`alerts.EmailAlert`).
  `FRITZ_MAIL_ALERTS=off` disables.
- **Sentry mode.** Armed by `sentry_mode` (voice or WhatsApp). Every
  arrival while armed sends a WhatsApp photo (20 s grace after arming,
  5 s minimum absence against tracking flicker, `FRITZ_SENTRY_COOLDOWN_MS`
  default 2 min), Fritz looks surprised and says one line to the visitor.
  While armed: no arrival greeting, and voice turns only get `sentry_mode`
  plus a prompt forbidding personal details, since the speaker may not be
  the owner. No face recognition, by design.
- **Focus mode and interruption rules.** The judge also returns `urgent`
  (can't wait an hour) and `hold` (an interruption rule says to hold it,
  exceptions applied). `Focus.ShouldHold`: rule-held mail is always held;
  during focus, anything not urgent is held. Held mail goes to a queue
  (never spoken or WhatsApped) and comes back as one digest when focus
  expires (`focusOverPrompt`, only if the user is at the desk) or in the
  arrival greeting. An urgent email during focus gets an "excuse the
  interruption" prompt. When the arrival greeting already has progress
  or held messages to say, it skips the slow email/calendar tool calls.
  Dry-run rules on real mail: `go run ./cmd/mailcheck -triage -rule "..."`.
  "Urgent" is judged against the real focus end (`MailWatcher.FocusEnds`),
  else one hour. Held messages and the progress reminder are cleared only
  after the reply was delivered (`respond` returns whether it was spoken;
  `Focus.Forget` removes exactly what was told), so a failed reply loses
  nothing. Quality is measured with `go run ./cmd/triageeval -runs 3`
  (18 labelled emails in `cmd/triageeval/interruptions.json`).
