"use client";

// Fritz's dashboard: what he's doing right now (focus, held
// messages, where you stopped, sentry), what he did (activity, each
// entry tagged with the Meta model behind it), and what he remembers
// (deletable). Polls the Pi through /api/fritz every few seconds.

import { useCallback, useEffect, useState } from "react";

type Held = { at: string; text: string };
type Status = {
  focus: { on: boolean; until?: string };
  held: Held[];
  progress?: { task: string; next?: string; at: string; recalled?: boolean };
  sentry: { on: boolean; since?: string };
  rules: string[];
};
type Entry = { id: number; at: string; source: string; kind: string; name: string; detail: string; ok: boolean };
type Memory = { id: number; kind: string; text: string; source: string; confidence: number; updated_at: string };

const RULE_PREFIX = "Interruption rule: ";

// The Meta models behind Fritz, and how an activity entry maps to
// them. Voice entries started as speech, so they also credit Muse
// Voice Transcribe.
const MODELS = {
  spark: "Muse Spark 1.3",
  sam: "SAM 3.1",
  transcribe: "Muse Voice Transcribe",
  wonder: "Muse Wonder",
} as const;
type Model = keyof typeof MODELS;

function modelsFor(e: Entry): Model[] {
  const out: Model[] = [];
  if (e.source === "voice") out.push("transcribe");
  if (e.name === "find_object" || e.name === "sentry") out.push("sam");
  if (e.name !== "sentry" && !(e.name === "focus_mode" && e.detail === "focus time over")) out.push("spark");
  return out;
}

const NAMES: Record<string, string> = {
  important_email: "Important email",
  sentry: "Someone at the desk",
  sentry_mode: "Sentry mode",
  focus_mode: "Focus mode",
  interruption_rule: "New rule",
  save_progress: "Saved your place",
  where_was_i: "Where you were",
  find_object: "Looked for something",
  email_search: "Searched email",
  email_read: "Read an email",
  email_reply: "Email reply",
  calendar_list: "Checked calendar",
  calendar_create: "Calendar event",
};

const SOURCES: Record<string, string> = { voice: "voice", whatsapp: "WhatsApp", app: "phone", alerts: "on his own" };

function clock(iso?: string) {
  return iso ? new Date(iso).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" }) : "";
}

function minutesLeft(iso?: string) {
  if (!iso) return 0;
  return Math.max(0, Math.ceil((new Date(iso).getTime() - Date.now()) / 60000));
}

// Tool details are "args → result" JSON: show something readable.
function describe(e: Entry) {
  if (e.kind !== "tool") return e.detail;
  const [args] = e.detail.split(" → ");
  try {
    const parsed = JSON.parse(args);
    const vals = Object.values(parsed).filter((v) => typeof v === "string" || typeof v === "number");
    return vals.join(" · ");
  } catch {
    return args.slice(0, 120);
  }
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`/api/fritz/${path}`, { cache: "no-store" });
  if (!res.ok) throw new Error(`${path}: ${res.status} ${await res.text()}`);
  return res.json();
}

export default function Dashboard() {
  const [status, setStatus] = useState<Status | null>(null);
  const [activity, setActivity] = useState<Entry[]>([]);
  const [memories, setMemories] = useState<Memory[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [, tick] = useState(0);

  const load = useCallback(async () => {
    try {
      const [s, a, m] = await Promise.all([
        getJSON<Status>("status"),
        getJSON<Entry[]>("activity?limit=40"),
        getJSON<Memory[]>("memories"),
      ]);
      setStatus(s);
      setActivity(a);
      setMemories(m);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    }
  }, []);

  useEffect(() => {
    load();
    const poll = setInterval(load, 3000);
    const redraw = setInterval(() => tick((n) => n + 1), 15000); // countdowns
    return () => {
      clearInterval(poll);
      clearInterval(redraw);
    };
  }, [load]);

  async function forget(m: Memory) {
    const what = m.text.startsWith(RULE_PREFIX) ? "this rule" : "this memory";
    if (!confirm(`Make Fritz forget ${what}?\n\n${m.text}`)) return;
    const res = await fetch(`/api/fritz/memories/${m.id}`, { method: "DELETE" });
    if (!res.ok) setError(`delete failed: ${res.status}`);
    load();
  }

  const rules = memories.filter((m) => m.text.startsWith(RULE_PREFIX));
  const facts = memories.filter((m) => !m.text.startsWith(RULE_PREFIX));
  const usage: Record<Model, number> = { spark: 0, sam: 0, transcribe: 0, wonder: 0 };
  activity.forEach((e) => modelsFor(e).forEach((m) => usage[m]++));
  usage.wonder = facts.filter((m) => m.source.startsWith("consolidation")).length;

  return (
    <main>
      <header className="top">
        <div className="brand">
          <div className="eyes" aria-hidden>
            <span />
            <span />
          </div>
          <div>
            <h1>Fritz</h1>
            <p className="sub">your desk companion, right now</p>
          </div>
        </div>
        <div className="pills">
          <span className={`pill ${status?.focus.on ? "on focus" : ""}`}>
            {status?.focus.on ? `Focus · ${minutesLeft(status.focus.until)} min left` : "Not focusing"}
          </span>
          <span className={`pill ${status?.sentry.on ? "on sentry" : ""}`}>
            {status?.sentry.on ? `Sentry on since ${clock(status.sentry.since)}` : "Sentry off"}
          </span>
        </div>
      </header>

      {error && <p className="error">Can't reach Fritz: {error}</p>}

      <section className="grid">
        <div className="card">
          <h2>Held for later</h2>
          {status?.held.length ? (
            <ul className="list">
              {status.held.map((h, i) => (
                <li key={i}>
                  <span className="time">{clock(h.at)}</span>
                  {h.text}
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">
              {status?.focus.on ? "Nothing yet. Fritz is keeping things quiet." : "Nothing held. Start focus by saying “let me focus for 30 minutes.”"}
            </p>
          )}
        </div>

        <div className="card">
          <h2>Where you stopped</h2>
          {status?.progress ? (
            <>
              <p className="big">{status.progress.task}</p>
              {status.progress.next && <p>Next: {status.progress.next}</p>}
              <p className="muted">
                Saved {clock(status.progress.at)}
                {status.progress.recalled ? " · reminded you when you came back" : ""}
              </p>
            </>
          ) : (
            <p className="muted">Say “I'm stopping at…” before you step away.</p>
          )}
        </div>

        <div className="card">
          <h2>What may interrupt you</h2>
          {rules.length ? (
            <ul className="list">
              {rules.map((m) => (
                <li key={m.id} className="row">
                  <span>{m.text.slice(RULE_PREFIX.length)}</span>
                  <button onClick={() => forget(m)} aria-label="Delete rule">
                    ×
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="muted">No rules yet. Try “hold project updates unless there's a deadline.”</p>
          )}
        </div>
      </section>

      <section className="card">
        <h2>What Fritz did</h2>
        {activity.length ? (
          <ul className="feed">
            {activity.map((e) => (
              <li key={e.id} className={e.ok ? "" : "failed"}>
                <span className="time">{clock(e.at)}</span>
                <div className="what">
                  <strong>{NAMES[e.name] ?? e.name}</strong>
                  <span className="via"> · {SOURCES[e.source] ?? e.source}</span>
                  {!e.ok && <span className="bad"> · failed</span>}
                  <div className="detail">{describe(e)}</div>
                </div>
                <div className="badges">
                  {modelsFor(e).map((m) => (
                    <span key={m} className={`badge ${m}`}>
                      {MODELS[m]}
                    </span>
                  ))}
                </div>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">Nothing yet today.</p>
        )}
      </section>

      <section className="card">
        <h2>What Fritz remembers</h2>
        {facts.length ? (
          <ul className="list">
            {facts.map((m) => (
              <li key={m.id} className="row">
                <span>
                  <span className="kind">{m.kind}</span> {m.text}
                  {m.source.startsWith("consolidation") && <span className="badge wonder">learned by {MODELS.wonder}</span>}
                </span>
                <button onClick={() => forget(m)} aria-label="Forget">
                  ×
                </button>
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">Fritz learns your routines overnight with Muse Wonder.</p>
        )}
      </section>

      <footer>
        <span>Powered by Meta — uses on this page:</span>
        {(Object.keys(MODELS) as Model[]).map((m) => (
          <span key={m} className={`badge ${m}`}>
            {MODELS[m]} · {usage[m]}
          </span>
        ))}
      </footer>
    </main>
  );
}
