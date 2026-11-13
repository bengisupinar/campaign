# Campaign Composer

An AI-powered marketing campaign composer. A marketer describes a campaign in natural
language; an LLM proposes audience segmentation rules and SMS/email content; people review,
edit and approve it; approved campaigns are sent asynchronously to synthetic users through
mock providers, and delivery status is tracked in real time.

> **Status — Phase 1 (foundation).** The API connects to PostgreSQL, applies migrations,
> exposes health probes, logs structured JSON and shuts down gracefully; the web app shows
> the API's readiness. Campaign features arrive in later phases.

## Quick start

Requires Docker with Compose v2.

```sh
docker compose up --build
```

| What | Where |
|---|---|
| Web app | http://localhost:3000 |
| API liveness / readiness | http://localhost:8080/healthz · http://localhost:8080/readyz |
| PostgreSQL | `localhost:5432`, user/password/database `campaign` (development only) |

No `.env` file is needed: every setting has a development default. To change ports or
credentials, `cp .env.example .env` and edit it. Stop with `docker compose down`
(add `-v` to also delete the database volume).

The first build downloads the base images and takes a few minutes.

## Development

Tools: Go 1.27 (an older Go downloads it automatically from `go.mod`'s `toolchain` line),
Node ≥ 24.12 (`nvm use` picks 26 from `.nvmrc`), GNU Make, Docker.

```sh
cp .env.example .env     # host settings: DATABASE_URL, text logs, ...
make db                  # PostgreSQL in Docker
make run                 # API on :8080 (applies migrations on startup)

make web-install         # once
make web-dev             # Vite on http://localhost:5173, proxying /api and /readyz to :8080
```

`make` lists every target. The most useful:

| Target | Purpose |
|---|---|
| `make up` / `make down` / `make logs` | Full stack in Docker, waiting until it is healthy |
| `make test` | Go tests with the race detector |
| `make lint` / `make fmt` | golangci-lint v2 (built into `./bin` on first use) / formatting |
| `make migrate-new name=create_campaigns` | Create the next SQL migration |
| `make migrate-status` / `migrate-up` / `migrate-down` | Inspect or move the schema version |
| `make web-test` / `web-lint` / `web-build` | Frontend unit tests, lint + type-check, production build |
| `make check` | Everything above that CI would run |

## Configuration

The API reads only environment variables and refuses to start if one is missing or invalid.

| Variable | Default | Description |
|---|---|---|
| `DATABASE_URL` | — (required) | PostgreSQL connection string |
| `HTTP_ADDR` | `:8080` | Listen address |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `LOG_FORMAT` | `json` | `json` or `text` |

`docker compose` additionally reads `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`,
`POSTGRES_PORT`, `API_PORT` and `WEB_PORT` (see [.env.example](.env.example)).

## Health probes

- `GET /healthz` (liveness): `200 {"status":"ok"}` while the process runs. It checks no dependencies.
- `GET /readyz` (readiness): `200` when every dependency answers within 2 s, otherwise `503`,
  e.g. `{"status":"unavailable","checks":{"postgres":"unavailable"}}`. Details are only logged.

Docker Compose uses `/readyz` as the API healthcheck.

## Project layout

```
cmd/api/                  API entry point: wiring and graceful shutdown
internal/platform/        cross-cutting technical code (no business rules)
  config/                 environment configuration
  database/               GORM connection pool, migrations on startup
  health/                 liveness and readiness probes
  httpserver/             router, middleware, server lifecycle
migrations/               versioned SQL migrations (goose), embedded in the binary
frontend/                 Vue 3 + TypeScript + Vite app, its Dockerfile and nginx config
compose.yaml              postgres, api, web
```

Business capabilities will live in `internal/<capability>` (e.g. `internal/campaign`), each
in a single package. See [DECISIONS.md](DECISIONS.md) for the reasoning behind the stack and
structure, [PHASE_REVIEWS.md](PHASE_REVIEWS.md) for what each phase built and how it was
verified, and [CLAUDE.md](CLAUDE.md) for the engineering principles and workflow.
