# Campaign Composer

An AI-powered marketing campaign composer. A marketer describes a campaign in natural
language; an LLM proposes audience segmentation rules and SMS/email content; people review,
edit and approve it; approved campaigns are sent asynchronously to synthetic users through
mock providers, and delivery status is tracked in real time.

> **Status — Phase 2A (customer and commerce model).** On top of the Phase 1 foundation
> (PostgreSQL, migrations, health probes, structured logs, graceful shutdown, and a web app
> showing the API's readiness), the schema and domain model for customers, permissions,
> products, baskets and orders are in place. Synthetic data (Phase 2B) and campaign features
> arrive later.

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
| `make test-integration` | Also runs the schema tests against PostgreSQL, in throwaway databases (needs `make db` and a `.env`) |
| `make lint` / `make fmt` | golangci-lint v2 (built into `./bin` on first use) / formatting |
| `make migrate-new name=create_campaigns` | Create the next SQL migration |
| `make migrate-status` / `migrate-up` / `migrate-down` | Inspect or move the schema version |
| `make web-test` / `web-lint` / `web-build` | Frontend unit tests, lint + type-check, production build |
| `make check` | Every Go and frontend check that needs no database |

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
internal/customer/        Customer context: customers, contact details, permissions, cities
internal/commerce/        Commerce context: products, baskets, orders
internal/platform/        cross-cutting technical code (no business rules)
  config/                 environment configuration
  database/               GORM connection pool, migrations on startup
  health/                 liveness and readiness probes
  httpserver/             router, middleware, server lifecycle
migrations/               versioned SQL migrations (goose), embedded in the binary; schema tests
frontend/                 Vue 3 + TypeScript + Vite app, its Dockerfile and nginx config
compose.yaml              postgres, api, web
```

Each business capability is a single package, `internal/<capability>`. See
[DECISIONS.md](DECISIONS.md) for the reasoning behind the stack and structure,
[PHASE_REVIEWS.md](PHASE_REVIEWS.md) for what each phase built and how it was verified, and
[CLAUDE.md](CLAUDE.md) for the engineering principles and workflow.

## Further improvements

Deliberately left out so far; each phase review adds what it defers.

- [ ] **Multi-currency:** a currency per country, per-currency prices, orders in the currency
      paid, and spend segments per currency or via exchange rates. Every amount is in TRY today.
- [ ] **Partial refunds,** by line or by amount. Orders are refunded in full today.
- [ ] **Consent history:** every permission change with its source (signup, unsubscribe link,
      SMS STOP), as proof of consent. Only the current state is stored today.
- [ ] **Basket history:** add and remove events, and baskets abandoned, then recovered.
- [ ] **Product price history,** for price-drop campaigns.
- [ ] **Category hierarchy.** Categories are flat today.
- [ ] **More customer attributes,** such as birth date or gender, if segments need them. They
      are more personal data to protect.
- [ ] **A precomputed segmentation profile** (materialized view), if segment queries become slow.
- [ ] **A separate migration job in production,** with a DDL role, so the API can run with a
      DML-only role ([DECISIONS.md](DECISIONS.md) §6).
