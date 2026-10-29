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
- Draft reply: build an RFC 2822 message with `In-Reply-To`, `References`,
  `Subject: Re: ...`, same `ThreadId`; `Users.Drafts.Create`.
- Send: `Users.Drafts.Send` — only after the confirmation flow in
  `fritz-agent`.
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

## Social media — deferred

Instagram/Facebook APIs need app review and X charges for API access; none
fit the hackathon timeline. Don't start them unless the core loop, vision,
email and memory are done and demo-ready. If added later, follow the same
pattern: read-only summaries first, any posting behind confirmation.
