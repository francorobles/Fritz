# Fritz — API (Go)

Go HTTP API using [chi](https://github.com/go-chi/chi). Serves the Android,
iOS, IoT, and web clients.

## Run

```sh
go run ./cmd/server        # listens on :8080 (override with ADDR=:9000)
go test ./...              # tests
go build -o bin/fritz-api ./cmd/server
```

## Endpoints

| Method | Path       | Description    |
| ------ | ---------- | -------------- |
| GET    | `/`        | Service info   |
| GET    | `/healthz` | Health check   |

## Layout

- `cmd/server/main.go` — entry point (`ADDR` env, default `:8080`)
- `internal/httpapi/` — chi router and handlers
- `internal/face/` — UDP client for the C++ face (`face.Client`:
  `Emotion`/`Standby`/`Gaze`/`GazeOff`/`Speaking`/`Blink`, plus the
  assistant-state → expression mapping in `states.go`); fire-and-forget,
  send errors are logged once and never returned
