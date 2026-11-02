---
name: fritz-hackathon
description: >
  Meta Global AI Hackathon rules, judging criteria, banned project categories,
  deadlines, and Fritz's scope and demo plan. Use when deciding what to build
  next, cutting scope, writing the README or submission, planning or scripting
  the demo video, or checking whether a feature idea is allowed.
---

# Fritz × Meta Global AI Hackathon

## Hard rules

- Deadline: **Nov 4, 2026, 9:00 PM ET** (Nov 5, 10:00 AM at UTC+8).
  Judging Nov 5–8, finals Nov 9.
- Must meaningfully use Meta Model API and/or Meta open-weight models.
- Repository must be **public**.
- Only show and claim features built during the hackathon (Oct 27 onward).
  All Fritz commits are from this period — keep the README's "Built during the
  hackathon" section accurate. Unclear contributions can disqualify.
- No code, data, or assets without permission. Credit third-party assets
  (e.g., the eye-expression reference in `iot/README.md`, Piper voices and
  their licenses).

## Banned categories — Fritz must not drift into these

- AI medical advice, mental-health advisors, skin-condition detection
- Basic RAG apps, simple image analyzers, generic education chatbots
- Job-application screeners, nutrition coaches, **personality analyzers**
- Sports analyzers/coaches; anything political or about war

Practical meaning: Fritz learns *routines and preferences*, never judges
personality or mood; vision always serves an action, never "describe this
photo" on its own; no "you seem stressed" features.

## Judging (scored equally after eligibility)

1. **Impact** — a real problem the builder has firsthand (overnight email
   from other timezones, missing what matters while heads-down).
2. **Use of Meta AI tooling** — Fritz runs on four: Muse Spark (agent,
   vision), Muse Wonder (memory consolidation), Muse Voice Transcribe
   (ears), SAM 3.1 (eyes that follow you, find objects). It was built
   with Muse Code running Muse Wonder, alongside Claude Code (these skills
   are the shared notes). Name each one on screen in the demo.
3. **Technical depth** — edge device, realtime streaming ASR, presence-gated
   listening, vision-driven gaze, enforced confirmation for actions,
   self-updating memory.
4. **Demo quality** — physical device, real data, no dead air.

## Scope (status Nov 2 — feature-complete; no new features)

1. Face protocol + voice loop end to end ✅
2. Gmail + Calendar tools with spoken confirmation ✅
3. Camera presence + eyes following the user ✅ (SAM rate-limit back-off ✅)
4. `find_object` ("where are my keys?") ✅ and visual questions (live
   frame attached to the Spark turn) ✅
5. Memory + web dashboard + Muse Wonder consolidation ✅
6. Proactive greeting on arrival ✅
7. Away mode over **WhatsApp** ✅ (replaces the Android app): important
   mail and sentry photos reach the user; replies run the same tools; a
   "yes" there approves.
8. Focus mode, interruption rules, "where was I" ✅ (Nov 2) — the core of
   story A. Measured with `api/cmd/triageeval`.

Not in the demo: the Android app and FCM pushes (`fritz-mobile`,
scaffold only), the servo head (parked), Facebook comments, aircon.
Out of scope: `ios/`. Muse Gadgets is not an option — the Muse app isn't
available in the Philippines.

## Plan to the deadline

- Nov 2: build done (focus features, dashboard, preflight, `--demo`,
  fast confirmations, README) ✅. Not yet run on the Pi.
- Nov 3: practice on the Pi (preflight, three full rehearsals), run
  `triageeval -runs 3` and put the score in the README, fix only what the
  logs show, **record a complete backup take**.
- Nov 4: final takes, edit, secret scan, make the repo public (check in a
  private window), submit with buffer before 9 PM ET.
- Story: **A, decided** — the builder's overnight cross-timezone email
  (London, Toronto, Tokyo) and getting interrupted while heads-down.

## Demo video (2:50–3:00, story A)

1. **Hook (0:00):** builder to camera: colleagues in London, Toronto and
   Tokyo; one email matters, the rest interrupts. "So I built Fritz."
2. **Arrival (0:15):** eyes find you; greeting names Sarah's overnight
   email (cut the wait — the slowest turn).
3. **Reply by voice (0:40):** "tell Sarah Thursday works" → fixed
   question → "yes" → sent. Leave uncut: the one place real latency shows.
4. **Focus (1:05):** "focus for 30 minutes, hold project updates unless
   there's a deadline" → a project update is held (dashboard shows it) →
   an urgent email interrupts.
5. **Keys (1:45):** eyes turn to the keys.
6. **Away and back (2:00):** "I'm stopping at the pricing section" → walk
   out → WhatsApp alert → reply "approved" → "yes" → walk back → greeting
   with where you stopped, the sent reply, the held update.
7. **Dashboard (2:30):** rule, activity with model badges, a Wonder
   memory deleted live.
8. **How it works + close (2:45):** diagram with Muse Spark, Voice
   Transcribe, SAM 3.1, Muse Wonder, and Muse Code (built it, running
   Muse Wonder, alongside Claude Code); the triageeval score.

Staged emails E1–E4, reset commands and fallbacks live on the user's
practice page. Record scene by scene, cut the waiting, keep one real-time
exchange.

## Submission checklist

- Public repo, README with: what it is, the problem, architecture diagram,
  every Meta model and where it's used, setup steps, "Built during the
  hackathon" list, third-party credits, and non-Meta parts named honestly
  (Piper TTS, Google APIs).
- Demo video link.
- No secrets in git history: `git log -p | grep -iE "api[_-]?key|token|secret"`
  before making the final push.
  (A full pattern scan on Nov 2 found nothing; re-run before going public.)
- The triageeval score in the README (the placeholder comment under
  "Measuring the interruption decisions").
