# Phase reviews

What each development phase built, newest first: its components, how they were tested and
verified, how the result differs from the plan, and what deserves a closer look. Entries are
snapshots taken when a phase is finished; later phases add entries rather than rewriting old
ones. The reasoning behind each choice is in [DECISIONS.md](DECISIONS.md), setup and commands
are in [README.md](README.md).

## Phase 1: Project foundation (2026-11-13)

**Scope.** A runnable skeleton without business features. The Go API connects to PostgreSQL,
applies migrations, serves health probes, logs structured JSON and shuts down gracefully. The
Vue app shows whether the API is ready. `docker compose up --build` starts everything on a fresh
clone without a `.env`. Not built yet, by design: domain entities, campaign logic, AI
integration, queues and workers, notification channels, seed data and mock providers.

**Result.** All 12 acceptance criteria pass, and `make check` is green.

### How the pieces fit

```text
browser
  │
  ▼
web        nginx on host port 3000: serves the built Vue app, proxies /api/* and /readyz
  │
  ▼
api        Go on host port 8080: applies migrations on startup, serves the health probes
  │
  ▼
postgres   PostgreSQL 18 on host port 5432, data in the pgdata volume
```

Compose starts them bottom-up, each once the one below is healthy. In development, Vite on
:5173 replaces nginx and the API runs on the host (`make run`). To read the Go code, start at
`cmd/api/main.go`: it calls every other component in start-up order.

### Components

| Component | Location | What it does | Tests |
|---|---|---|---|
| API entry point | `cmd/api` | Wires everything together; refuses to start on bad config or an unreachable database | Manual (verification 5–6) |
| Configuration | `internal/platform/config` | Typed settings from environment variables | 9 cases, 100 % |
| HTTP server | `internal/platform/httpserver` | Router, request IDs, access log, panic recovery, timeouts, graceful shutdown | 10 cases, 93 % |
| Health probes | `internal/platform/health` | `/healthz` (liveness) and `/readyz` (readiness) | 5 cases, 100 % |
| Database and migrations | `internal/platform/database`, `migrations/` | Connection pool, start-up ping, versioned schema migrations | Manual (verification 3–4, 6) |
| Web app | `frontend/src` | Vue shell with an API status badge | 6 Vitest tests |
| Web server | `frontend/Dockerfile`, `frontend/nginx.conf` | Serves the built app and proxies API calls | Manual (verification 1) |
| Containers | `Dockerfile`, `compose.yaml`, `.dockerignore` | One-command stack with health-ordered start-up | Manual (verification 1, 3, 12) |
| Developer tooling | `Makefile`, `.golangci.yml`, `.env.example`, `.nvmrc`, `.gitignore` | Development loop, linting, migrations, `make check` | Manual (verification 8–11) |
| Documentation | `README.md`, `DECISIONS.md`, `CLAUDE.md`, this file | Setup, reasoning, working rules, phase reviews | — |

Go test cases count each table-test row once; percentages are statement coverage from
`go test -cover`.

#### API entry point (`cmd/api/main.go`)

- `main` turns SIGINT and SIGTERM into context cancellation and exits 1 on error. `run` does all
  the wiring, so every dependency is explicit and there is no global state.
- Start-up order: configuration → logger → database (5 s ping) → migrations → probes → listener →
  server. Any failure stops the process before it accepts traffic.
- The configuration is logged at start-up without the database URL.

#### Configuration (`internal/platform/config`)

- `Load` parses `DATABASE_URL` (required), `HTTP_ADDR` (default `:8080`), `LOG_LEVEL` (default
  `info`) and `LOG_FORMAT` (`json` or `text`, default `json`) with caarlos0/env. It reports every
  missing or invalid variable in one error.
- It reads only the variables passed in (`main` passes the process environment) and never a
  `.env` file, so tests never depend on the process environment.
- `Config` implements `slog.LogValuer`, which leaves the database URL out of logs.

#### HTTP server (`internal/platform/httpserver`)

- `NewRouter` returns a chi router with three middlewares, outermost first: request ID (reusing
  an incoming `X-Request-Id`), access log and panic recovery.
- The access log writes one line per request with `request_id`, `method`, `path`, `status`,
  `bytes` and `duration_ms`: 5xx at error level, successful health probes at debug level.
- Panic recovery logs the panic and its stack as structured JSON and answers 500. It runs inside
  the access log, so the failed request is still logged. `http.ErrAbortHandler` is re-panicked,
  as net/http expects.
- `Serve` applies server timeouts (header 5 s, read 15 s, write 30 s, idle 60 s). When its
  context is cancelled, it stops accepting connections and gives in-flight requests up to 10 s.
  Request contexts don't derive from the signal, so a shutdown never cancels them.

#### Health probes (`internal/platform/health`)

- `GET /healthz` always answers 200 and checks nothing, so a database outage never gets a
  healthy process restarted.
- `GET /readyz` runs every registered check (today, a PostgreSQL ping) under a 2 s timeout. It
  answers 200, or 503 with the state of each check, e.g.
  `{"status":"unavailable","checks":{"postgres":"unavailable"}}`. Error details are logged,
  never returned.
- A check is a plain `func(ctx) error`, so a new dependency is one more entry in `cmd/api`.

#### Database and migrations (`internal/platform/database`, `migrations/`)

- `Open` connects through GORM (pgx driver) with explicit pool limits (20 open and idle
  connections, 30 min lifetime, 5 min idle time), pings within 5 s and closes the pool if the
  ping fails.
- GORM logs through slog at warn level and above: errors and queries slower than 200 ms, as
  parameterised SQL, so bind values (personal data) never reach the logs. Database errors are
  translated, e.g. a unique violation becomes `gorm.ErrDuplicatedKey`.
- `Migrate` applies the embedded goose SQL migrations under a PostgreSQL advisory lock, so several
  instances can start at once, and logs each applied file and the resulting version.
- `00001_baseline.sql` is a deliberate no-op that starts version tracking; the first real tables
  arrive with the first business capability.

#### Web app (`frontend/src`)

- Vue 3.5, TypeScript 6 and Vite 8, scaffolded by create-vue with Vitest, ESLint, Oxlint and
  Prettier. No router, store or component library yet.
- `ApiStatus.vue` calls `/readyz` once when it mounts and shows "API: ready", "API: not ready"
  (503) or "API: unreachable" (any other status or a network error, including the development
  proxy's 502 when the API is down).
- `vite.config.ts` proxies `/api` and `/readyz` to `localhost:8080`, so the browser stays on one
  origin and the API needs no CORS.

#### Web server (`frontend/Dockerfile`, `frontend/nginx.conf`)

- Node 26 builds the app; `nginx-unprivileged` 1.30 serves it as a non-root user.
- Unknown paths fall back to `index.html` for client-side routing. `/api/` and `/readyz` are
  proxied to the API with `X-Request-Id`, `X-Forwarded-For` and `X-Forwarded-Proto`.
- nginx looks `api` up in Docker's DNS on each request, so it still reaches a recreated API
  container with a new IP (verified).

#### Containers (`Dockerfile`, `compose.yaml`, `.dockerignore`)

- The backend image builds every `./cmd/*` binary and runs on `alpine:3.24` as uid 10001. Later
  programs (seed, worker, mock provider) become new Compose services reusing this image.
- Compose starts postgres, then api, then web, each once the previous one is healthy
  (`pg_isready`, then `/readyz`). Every setting has a development default, so a `.env` is
  optional and only overrides ports and credentials.
- `stop_grace_period: 15s` leaves room for the API's 10 s shutdown. PostgreSQL 18 keeps its data
  under `/var/lib/postgresql/18/docker`, so the volume is mounted at `/var/lib/postgresql`.
- The root `.dockerignore` keeps `.env`, `.git` and the frontend out of the API build;
  `frontend/.dockerignore` keeps the host's `node_modules` and `dist` out of the web build.

#### Developer tooling (`Makefile`, `.golangci.yml`, `.env.example`, `.nvmrc`, `.gitignore`)

- `make` lists every target: the stack (`up`, `down`, `logs`, `db`, `run`), Go (`test`, `lint`,
  `fmt`), migrations (`migrate-*`), frontend (`web-*`) and `check`, which runs all checks.
- Host runs load `.env` and export only its keys. Targets stop with a hint when `.env` is missing
  or Node is too old. Migration commands aren't echoed, so the database password stays off the
  terminal.
- `.env.example` documents every variable with development values; `.nvmrc` pins Node 26.
- golangci-lint v2.15.0 is pinned and built into `./bin`. It runs the standard linters plus
  `bodyclose`, `errorlint`, `gosec`, `noctx` and `revive`, and formats with `gofmt` and
  `goimports`.
- `.gitignore` now also applies its editor and test-output rules inside `frontend/`, and ignores
  Vite's temporary files.

### Verification

| # | Criterion | Result |
|---|---|---|
| 1 | Fresh clone without `.env`: `docker compose up --build` | Pass: postgres and api healthy, web running; the page, `/readyz` and the SPA fallback answer through nginx |
| 2 | Liveness | Pass: `/healthz` answers 200 `{"status":"ok"}` |
| 3 | Readiness follows the database | Pass: with PostgreSQL stopped, `/readyz` 503 and `/healthz` 200, api unhealthy after ~22 s; 200 again ~2 s after the restart |
| 4 | Migrations | Pass: v1 applied once and skipped on restart; `migrate-status`, `migrate-down`, `migrate-up` and `migrate-new` work |
| 5 | Graceful shutdown | Pass: `docker compose stop api` exits 0; the in-flight request test passes |
| 6 | Fail-fast configuration | Pass: missing `DATABASE_URL` exits 1 and all errors are reported together; an unreachable database fails within 5 s |
| 7 | Structured logs | Pass: JSON lines with the access-log fields; request IDs match nginx's |
| 8 | Go quality | Pass: `make test` with `-race`, `make lint` with 0 issues; no `frontend/` code in `go list ./...` |
| 9 | Development loop | Pass: `make db`, `make run`, `make web-dev`; with the API stopped, the proxy's 502 maps to "unreachable" |
| 10 | Frontend quality | Pass: lint, type-check, 6 tests and production build |
| 11 | Nothing secret or generated is tracked | Pass: `.env`, `bin/`, `node_modules/`, `dist/` and local editor settings ignored; `.env.example` tracked |
| 12 | Non-root containers | Pass: api runs as uid 10001, web as uid 101 |

Not checked: the page in a real browser. Its rendering is covered by the Vitest tests.

### Deviations from the plan

- Go 1.27.4 comes from the `toolchain` line in `go.mod`, not a system-wide install.
- golangci-lint is pinned and built into `./bin` rather than installed globally.
- `httpserver.Serve` takes a listener (the plan had `Run(ctx)`), so tests can use a random port
  and the log shows the real address.
- The access-log field is `duration_ms`, not `duration`.
- `.gitignore` changed, although the plan expected no change: nested-directory rules were fixed
  and Vite's temporary files added.
- Not in the plan: migration targets don't echo their commands, and nginx sets `X-Request-Id`
  so web and API logs share request IDs.
- create-vue now also sets up Oxlint.

### Worth a closer look

- **GORM** was chosen for speed, with guardrails ([DECISIONS.md](DECISIONS.md) §5). Query
  mistakes surface at runtime rather than at compile time, so every repository will need
  integration tests.
- **No automated tests for `database` and `cmd/api`** (0 % coverage). Both were verified by
  running the stack; integration tests against a real PostgreSQL should arrive with the first
  repository.
- **Migrations run at API start-up**, so the API's database role needs DDL rights (§6). That is
  fine here; in production, migrations would run as a separate job.
- **Server-sent events**, if chosen for live delivery status, need the 30 s write deadline
  extended per stream (`http.ResponseController`), streams ended on shutdown, and
  `proxy_buffering off` in nginx.

Open questions for later phases, including the queue choice, are recorded in
[DECISIONS.md](DECISIONS.md).
