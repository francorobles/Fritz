# Fritz — Web dashboard (Next.js)

What Fritz is doing right now (focus mode, held messages, where you
stopped, sentry, your interruption rules), what he did (activity log,
each entry tagged with the Meta model behind it), and what he remembers
(deletable). Polls the Go API every 3 s.

## Run

```sh
npm ci
npm run build
FRITZ_APP_TOKEN=... npm start   # http://localhost:3000
```

- `FRITZ_API_URL` — the Go API (default `http://127.0.0.1:8080`).
- `FRITZ_APP_TOKEN` — same value as the Go server's. The browser never
  sees it: `app/api/fritz/[...path]/route.ts` adds it server-side and
  only lets through the routes the page uses.

Run it on the Pi or on a laptop on the same tailnet
(`FRITZ_API_URL=http://<pi>:8080`). Never expose it with Tailscale
Funnel: it shows your memories and activity.

## Layout

- `app/page.tsx` — the dashboard (client component)
- `app/api/fritz/[...path]/route.ts` — proxy to the Go API
- `app/globals.css` — styles (light and dark)
