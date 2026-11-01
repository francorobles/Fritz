---
name: fritz-integrations
description: >
  Connecting Fritz to the user's Gmail and Google Calendar from Go: OAuth
  setup, token storage, scopes, and implementing the email/calendar agent
  tools. Also why social media integrations are deferred. Use when working on
  api/internal/integrations, Google OAuth, Gmail or Calendar tools, or when
  asked to add another account/service.
---

# Fritz integrations

Lives in `api/internal/integrations/`. Tool contracts are in `fritz-agent`.

## Google (Gmail + Calendar)

Libraries: `golang.org/x/oauth2`, `golang.org/x/oauth2/google`,
`google.golang.org/api/gmail/v1`, `google.golang.org/api/calendar/v3`.

Setup (one time, by the user):

1. Google Cloud console → new project → enable Gmail API and Google Calendar
   API.
2. OAuth consent screen: External, testing mode, add the user's own address as
   a test user.
3. Create an OAuth client of type **Desktop app**; download the JSON to
   `~/.config/fritz/google-client.json`.
4. Run `go run ./cmd/googleauth` (create it): loopback-redirect flow, opens
   the consent URL (print it too, for a headless Pi — do the auth on the
   laptop and copy the token file over), saves the token to
   `~/.config/fritz/google-token.json` with mode 0600.

Scopes (minimum needed):

- `gmail.readonly` — search/read
- `gmail.compose` — create drafts and send them
- `calendar.events` — list/create events

Config paths come from `FRITZ_CONFIG_DIR` (default `~/.config/fritz`). Never
put client or token files in the repo; add `*.token.json`,
`google-client*.json` to `.gitignore` if not already covered.

## Gmail implementation notes

- Search: `Users.Messages.List("me").Q(query)` then `Get(id).Format("metadata")`
  with `From`, `Subject`, `Date` headers. Batch to ≤ 10 messages.
- Read: `Format("full")`, walk MIME parts, prefer `text/plain`, decode
  base64url, strip quoted replies (lines after `On ... wrote:`) before sending
  to the model.
- Reply (built, `integrations/reply.go`, `MailReplier`): `PrepareReply`
  reads From/Reply-To/Subject/Message-ID/References (so the confirmation
  names the recipient); `SendReply` builds an RFC 2822 plain-text message
  with `In-Reply-To`, `References`, one `Re:`, and sends it with
  `Users.Messages.Send` in the same `ThreadId`. No drafts: a declined
  draft would linger in the user's Gmail. Called **only** from the
  confirmed `email_reply` executor. CR/LF are stripped from every header
  value (they come from someone else's email and from the model —
  header injection otherwise). `gmail.compose` covers sending.
- "Important" for proactive alerts: unread AND (from a person in memory with
  kind `person`, OR Gmail's `IMPORTANT` label). Keep it simple.

## Calendar implementation notes

- `Events.List("primary").TimeMin/TimeMax.SingleEvents(true).OrderBy("startTime")`.
- Times: always RFC 3339 with the user's timezone (`FRITZ_TZ`, IANA name).
  Say times aloud in the user's local time.

## Demo data

Use the developer's real account only with mail they're comfortable showing
on screen, or a dedicated demo Gmail account seeded with realistic threads.
Never fake API responses in the demo without saying so.

## WhatsApp (Cloud API) — Fritz's link when the user is away

Code: `integrations/whatsapp.go` (send text/template),
`whatsapp_webhook.go` (inbound, mounted by `cmd/server` at
`/webhooks/whatsapp`), chat replies via `internal/chat` with the same
Go-checked `Confirmer` as voice (a WhatsApp "yes" approves the pending
action). Replaces the Android app for the demo's "away" scene; WhatsApp
is also a Meta product, which counts for the judging.

Setup (by the user, once):

1. developers.facebook.com → Create app → use case "Connect with customers
   through WhatsApp" (needs a Meta Business portfolio).
2. WhatsApp → API Setup: note the test number's **Phone number ID**; add
   the user's own number as a recipient (verify the code).
3. Permanent token: Business Settings → System users → add (Admin) →
   assign the app → generate a token with `whatsapp_business_messaging` +
   `whatsapp_business_management`. Temporary tokens die in 24 h.
4. App secret: App settings → Basic. Verify token: any random string
   (`openssl rand -hex 16`).
5. Secrets go in `/etc/fritz/fritz.env` (`FRITZ_WA_TOKEN`,
   `FRITZ_WA_APP_SECRET`, `FRITZ_WA_VERIFY_TOKEN`); `integrations.json`
   only names them:
   `"whatsapp": {"enabled": true, "phone_number_id": "...", "token_env":
   "FRITZ_WA_TOKEN", "app_secret_env": "FRITZ_WA_APP_SECRET",
   "verify_token_env": "FRITZ_WA_VERIFY_TOKEN", "allow": ["63917..."],
   "alert_template": "hello_world"}`.
6. Test sending: `go run ./cmd/mailcheck -whatsapp`.
7. Inbound: run `cmd/server` on the Pi, expose it with
   `sudo tailscale funnel --bg 8080`, then in the app's WhatsApp →
   Configuration set the callback to `https://<pi>.<tailnet>.ts.net/webhooks/whatsapp`
   with the verify token, and subscribe to `messages`.
8. Alerts outside the 24 h window need an approved template: submit a
   Utility template early (approval can take hours), e.g. `fritz_alert`,
   body `Hi, it's Fritz. {{1}} Reply here to talk to me.` — exactly one
   variable, not at the start or end (Meta rejects that). The alert text
   (important email, someone at the desk) fills `{{1}}`. Until it's
   approved, message Fritz first so free-form messages are allowed.

### Alerts (`internal/alerts`)

`Notifier` sends to the first `allow` number. Inside the 24 h window
(your last message, recorded by the webhook's `OnInbound` in the shared
DB) it sends free text and photos; outside it, the `alert_template`
(`hello_world` sends no variables, for wiring tests). WhatsApp *accepts*
a free-form send outside the window and fails it later on the status
webhook, so the window is tracked, never guessed from errors. A sentry
photo can't go out with the window closed: the text goes as a template
("Reply here and I'll send the photo") and the photo waits next to the
DB (`sentry/`, gitignored) until you reply. Every alert is also stored
as one of Fritz's turns, so "read it to me" has context.

The voice loop runs the watchers; `cmd/server` answers replies. They
must share `FRITZ_DB` (`scripts/run-fritz.sh` sets it and starts the
server in the background; log `~/.cache/fritz/server.log`).

Test without waiting for real mail:
`go run ./cmd/mailcheck -triage` (what Spark would flag, sends nothing),
`go run ./cmd/mailcheck -alert "Test from Fritz"` (one real alert).

## Social media — deferred

Instagram/Facebook APIs need app review and X charges for API access; none
fit the hackathon timeline. Don't start them unless the core loop, vision,
email and memory are done and demo-ready. If added later, follow the same
pattern: read-only summaries first, any posting behind confirmation.
