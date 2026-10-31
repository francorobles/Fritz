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
2. **Use of Meta AI tooling** — Fritz uses five: Muse Spark (agent, vision),
   Muse Wonder (memory consolidation), Muse Voice Transcribe (ears), SAM 3.1
   (eyes that follow you, find objects), and Muse Code (built with it — these
   skills are evidence). Name each one on screen in the demo.
3. **Technical depth** — edge device, realtime streaming ASR, presence-gated
   listening, vision-driven gaze, enforced confirmation for actions,
   self-updating memory.
4. **Demo quality** — physical device, real data, no dead air.

## Scope (in priority order — finish one before the next)

1. Face protocol + voice loop end to end (talk → reply → eyes react)
2. Gmail + Calendar tools with spoken confirmation
3. Camera presence + eyes following the user
4. `find_object` ("where are my keys?") and `look`
5. Memory + web dashboard + Muse Wonder consolidation
6. Proactive greeting on arrival
7. Away mode over **WhatsApp** (decided Nov 1, replaces the Android app
   for the demo): pending actions and urgent mail reach the user on
   WhatsApp, a "yes" there approves (`fritz-integrations`). The Android
   app (`fritz-mobile`) only if everything else is done.
8. Anything else (Muse Image, social media, Glimmer) — only if 1–7 are solid

Out of scope: `ios/`. Muse Gadgets is not an option — the Muse app isn't
available in the Philippines; the Android app replaces it.

## Plan to the deadline (set Nov 1)

- Nov 1: email reply + send ✅, activity log ✅, start WhatsApp Business
  setup (user; template approval takes time).
- Nov 2: `find_object` + last-seen memory for keys/wallet/glasses,
  proactive greeting on arrival.
- Nov 3: WhatsApp away mode (pending action → WhatsApp), simple web
  dashboard (memories + activity), README.
- Nov 4: rehearse and record; submit with buffer before 9 PM ET.
- Open decision: the impact story — (A) the builder's overnight
  cross-timezone email, or (B) a companion for parents living alone,
  connected to family abroad.

## Demo video (aim for 2–3 min)

1. **Hook (15 s):** the problem, told by the builder, in one sentence.
2. **Arrival (20 s):** walk to the desk; Fritz's eyes find you; it greets you
   with the one email that matters.
3. **Action (40 s):** "Fritz, reply that Thursday works." Draft read back →
   "send it" → happy eyes. Show the sent email.
4. **Vision (25 s):** "Fritz, where are my keys?" Eyes turn to the keys.
5. **Away (25 s):** walk off; eyes go to standby; an urgent email arrives;
   Fritz messages you on WhatsApp ("Sarah asked if Thursday works — reply
   yes?"); you answer "yes". Walk back: "Welcome back — I sent your reply
   to Sarah."
6. **Memory (20 s):** dashboard shows what Fritz learned; delete one live.
7. **How it works (25 s):** architecture diagram labelled with each Meta
   model.
8. **Close (10 s).**

Record several takes, cut the waiting, and keep real latency visible at
least once so it's clearly not faked.

## Submission checklist

- Public repo, README with: what it is, the problem, architecture diagram,
  every Meta model and where it's used, setup steps, "Built during the
  hackathon" list, third-party credits, and non-Meta parts named honestly
  (Piper TTS, Google APIs).
- Demo video link.
- No secrets in git history: `git log -p | grep -iE "api[_-]?key|token|secret"`
  before making the final push.
