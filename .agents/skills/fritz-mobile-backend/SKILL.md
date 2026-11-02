---
name: fritz-mobile-backend
description: >
  Server side of the Fritz phone companion, in the Go API (api/): the mobile
  HTTP contract, app-token auth, the shared pending-actions store (approved by
  voice or by phone), sending pushes with Firebase Cloud Messaging HTTP v1
  from Go, when Fritz decides to push, push-to-talk transcription, and
  reaching the Pi from the phone over Tailscale. Use when working on
  api/internal/mobile, api/internal/actions, push notifications, or the
  phone ↔ Pi connection.
---

# Fritz mobile backend (Go)

Packages: `api/internal/mobile/` (routes, FCM sender, push policy) and
`api/internal/actions/` (pending actions shared with the agent). The Android
side is in `fritz-mobile`.

## API contract

Status (Nov 2): built are `/actions/*` (pending, approve, decline),
`/activity`, and a dashboard `GET /status`; `api/internal/mobile`, FCM
pushes, `/mobile/*` and `/voice/turn` are **not built**, and the Android
app is still a scaffold. WhatsApp is the away channel for the demo
(`fritz-integrations`). The table below is the plan; where it disagrees
with the code, the code wins.

All routes require `Authorization: Bearer $FRITZ_APP_TOKEN` (compare with
`crypto/subtle.ConstantTimeCompare`; 401 otherwise). JSON in/out, snake_case.

| Method & path | Body → Response |
|---|---|
| `GET /status` | Built for the dashboard (`httpapi/status.go`): `{"focus":{"on","until"},"held":[…],"progress":{…},"sentry":{"on","since"},"rules":[…]}`. The phone's presence fields would be added here, not as a second route |
| `POST /mobile/register` | `{"fcm_token":"..."}` → `204` |
| `POST /mobile/test-push` | → `204`, sends a test alert |
| `GET /actions/pending` | → `[{"id","summary","kind","created_at","expires_at"}]` |
| `POST /actions/{id}/approve` | → `{"id","status":"executed","result":"Sent to Sarah"}` |
| `POST /actions/{id}/decline` | → `{"id","status":"declined"}` |
| `GET /activity?limit=50` | → `[{"id","at","source","kind","name","detail","ok"}]` (built; same log as the web dashboard). Needs the app token when `FRITZ_APP_TOKEN` is set, like `/memories`, `/turns`, `/status` and `/memory/consolidate` |
| `POST /voice/turn` | multipart `audio` (WAV 16 kHz mono s16) → `{"transcript","reply"}` |

Errors: `{"error":"message"}` with a proper status (`404` unknown action,
`409` action no longer pending, `502` upstream model error).

Add the routes to `httpapi.NewRouter()` in a sub-router with the auth
middleware; keep `/healthz` public. Token from env `FRITZ_APP_TOKEN`
(generate with `openssl rand -hex 32`); refuse to start mobile routes if it's
empty.

## Pending actions (one store for voice and phone)

This extends the confirmation rule in `fritz-agent`: an action can be
confirmed by the user's next spoken turn **or** by the app.

- SQLite table `actions(id, kind, summary, payload_json, status,
  created_at, expires_at, result)`; status `pending → approved → executed`
  or `pending → declined | expired | failed`.
- Approve does an atomic `UPDATE ... SET status='approved' WHERE id=? AND
  status='pending'`; if 0 rows changed, return `409`. This prevents double
  sends when voice and phone approve at the same time.
- Then execute (e.g., the `email_reply` executor), store the result, set
  `executed` or `failed`, and log to activity (built for `/actions` and
  the voice/WhatsApp `Confirmer`).
- Expire after 15 minutes.
- When an action is created while the user is away (per vision presence),
  push it to the phone immediately.
- After an app approval, Fritz at the desk says the outcome next time the
  user is present ("I sent your reply to Sarah"), via the agent's proactive
  greeting.

## Sending pushes: FCM HTTP v1

- Endpoint: `POST https://fcm.googleapis.com/v1/projects/{project_id}/messages:send`
- Auth: a Firebase service-account JSON in `FRITZ_CONFIG_DIR`
  (`firebase-sa.json`, mode 0600, never committed). Use
  `google.CredentialsFromJSON(ctx, data, "https://www.googleapis.com/auth/firebase.messaging")`
  and an `oauth2` HTTP client; `project_id` comes from the JSON.
- Send **data-only** messages with Android high priority, so the app builds
  the notification with action buttons:

```json
{"message": {
  "token": "<fcm token>",
  "android": {"priority": "HIGH"},
  "data": {"type": "action", "action_id": "a_123",
           "title": "Fritz needs your OK",
           "body": "Send reply to Sarah: 'Thursday works'?"}
}}
```

  `type` is `alert` or `action`. All `data` values must be strings.
- On `404`/`UNREGISTERED`, drop the stored token. Retry `429`/`5xx` once.
- Without a registered token or service account, log and skip — pushes are
  optional and must never break the voice loop.

## When to push (policy)

Push only when the user is **away** (vision presence false for > 2 min) and
one of:

- an action needs approval;
- an email the agent flags as urgent arrives (from a `person` memory, or
  Gmail `IMPORTANT` + unread), summarized in one line by Muse Spark;
- a calendar event starts within 15 min.

Max one `alert` push per 10 min (actions are never rate-limited). Never push
while the user is at the desk — Fritz speaks instead.

## Push-to-talk

`POST /voice/turn`: forward the WAV to `POST /v1/asr/transcribe` with
`mode: "PUSH_TO_TALK"` (see `meta-model-api`), run the transcript through the
same agent as a voice turn with source `phone`, and return the reply text.
Don't speak it at the desk and don't change the face to `speaking` (the user
is away). Cap uploads at 2 MB.

## Phone ↔ Pi connection: Tailscale

The phone must reach the Pi from anywhere without port forwarding.

1. Install Tailscale on the Pi and the phone, same tailnet.
2. On the Pi: `sudo tailscale serve --bg 8080` — this serves the Go API at
   `https://<pi-hostname>.<tailnet>.ts.net` with a valid certificate (enable
   HTTPS certificates in the Tailscale admin console first).
3. Put that URL in the app's Settings.

The API is then only reachable inside the tailnet; the app token is a second
layer, not the only one. Never expose port 8080 to the public internet:
`tailscale serve` (tailnet) is fine, but `tailscale funnel` (public) must
be limited to `/webhooks/whatsapp` with `--set-path`.

## Testing

- `go test ./internal/actions/...`: concurrent approve (voice + phone) results
  in exactly one execution; expiry; decline after approve returns 409.
- `go test ./internal/mobile/...`: auth middleware, push policy (away/present,
  rate limit) with a fake clock and fake sender; FCM payload golden test.
- Manual: `curl -H "Authorization: Bearer $FRITZ_APP_TOKEN" -X POST https://<pi>.ts.net/mobile/test-push`.
