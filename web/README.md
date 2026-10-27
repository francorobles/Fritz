# Fritz — Web (Next.js)

Next.js (App Router) + React + TypeScript frontend.

## Run

```sh
npm install
npm run dev    # http://localhost:3000
npm run build  # production build
npm start      # serve the production build
```

## Layout

- `app/page.tsx` — home page
- `app/layout.tsx` — root layout / metadata
- Talks to the Go API in `../api` (default `http://localhost:8080`).
