# Campaign Composer

An AI-powered marketing campaign composer. A marketer describes a campaign in natural
language; an LLM proposes audience segmentation rules and SMS/email content; people review,
edit and approve it; approved campaigns are sent asynchronously to synthetic users through
mock providers, and delivery status is tracked in real time.

> **Status — Phase 4 (segmentation engine).** On top of the Phase 1 foundation (PostgreSQL,
> migrations, health probes, structured logs, graceful shutdown, and a web app showing the
> API's readiness), the customer and commerce model holds 5,000 synthetic customers and their
> shopping activity. Marketers can manage campaign drafts through the REST API. The
> segmentation engine compiles a draft's segment into parameterised SQL and counts or lists
> the customers it reaches on each channel. Saving a draft checks that the cities, categories
> and countries it names exist. AI, sending and scheduling arrive in later phases.

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

The first start also seeds demo data in a second or two:

- 5,000 synthetic customers in Türkiye, Germany and the UK;
- their permissions, baskets and orders;
- a 168-product catalogue.

Later starts keep the data. Timestamps are relative to the seeding time, so for fresh data
remove the volume with `docker compose down -v`. Every email address is on a domain reserved
for examples, and every phone number is synthetic.

The first build downloads the base images and takes a few minutes.

## Development

Tools: Go 1.27 (an older Go downloads it automatically from `go.mod`'s `toolchain` line),
Node ≥ 24.12 (`nvm use` picks 26 from `.nvmrc`), GNU Make, Docker.

```sh
cp .env.example .env     # host settings: DATABASE_URL, text logs, ...
make db                  # PostgreSQL in Docker
make seed                # demo data, once (applies migrations; skips a seeded database)
make run                 # API on :8080 (applies migrations on startup)

make web-install         # once
make web-dev             # Vite on http://localhost:5173, proxying /api and /readyz to :8080
```

`make` lists every target. The most useful:

| Target | Purpose |
|---|---|
| `make up` / `make down` / `make logs` | Full stack in Docker, waiting until it is healthy |
| `make test` | Go tests with the race detector |
| `make test-integration` | Also runs the database tests (schema, stores, seeding, segmentation) against PostgreSQL, in throwaway databases (needs `make db` and a `.env`) |
| `make seed` | Fill an empty database with the demo data |
| `make lint` / `make fmt` | golangci-lint v2 (built into `./bin` on first use) / formatting |
| `make migrate-new name=create_campaigns` | Create the next SQL migration |
| `make migrate-status` / `migrate-up` / `migrate-down` | Inspect or move the schema version |
| `make web-test` / `web-lint` / `web-build` | Frontend unit tests, lint + type-check, production build |
| `make check` | Every Go and frontend check that needs no database |

To time the segment queries on the seeded dataset, after `make db`:

```sh
TEST_DATABASE_URL='postgres://campaign:campaign@localhost:5432/campaign?sslmode=disable' \
  go test -run '^$' -bench . ./internal/segmentation
```

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

## Campaign API

Campaign drafts live under `/api/campaigns`: through nginx at http://localhost:3000, or
directly on :8080.

| Request | Result |
|---|---|
| `POST /api/campaigns` | 201 and the new draft, with a `Location` header |
| `GET /api/campaigns` | 200 and every campaign, newest first |
| `GET /api/campaigns/{id}` | 200, or 404 |
| `PUT /api/campaigns/{id}` | 200 after replacing the name, segment and content; 409 unless it is a draft |
| `DELETE /api/campaigns/{id}` | 204; 409 unless it is a draft |

```sh
curl -X POST localhost:3000/api/campaigns -d '{
  "name": "Abandoned baskets in İstanbul",
  "segment": {"version": 1, "match": "all", "conditions": [
    {"type": "city", "in": ["İstanbul"]},
    {"type": "basket", "within_days": 30},
    {"type": "purchased", "within_days": 30, "negate": true}
  ]},
  "content": {"sms": {"body": "Merhaba {{first_name}}, sepetin seni bekliyor!"}}
}'
```

- **Channels.** A campaign uses SMS, email (`"email": {"subject": …, "body": …}`) or both.
- **Limits.** An SMS holds at most 160 characters and an email subject at most 80.
- **Placeholders.** Text may contain `{{first_name}}`, `{{last_name}}` and `{{city}}`.
- **Segments.** The DSL is described in [DECISIONS.md](DECISIONS.md) §20 and its meaning in
  §22. Every city, category and country code a segment names must exist, spelled exactly as
  stored (`İstanbul`, not `Istanbul`), or the draft gets an `unknown` problem. `total_spent`
  bounds are in kuruş: `{"type": "total_spent", "within_days": 90, "min": 100000}` means at
  least 1,000 TRY in the last 90 days.
- **Errors.** An invalid draft gets 422 with every problem, e.g.
  `{"error": "invalid_campaign", "problems": [{"field": "content.sms.body", "problem": "too_long"}]}`.
  Other errors are `{"error": "<code>"}`: `not_found`, `not_draft`, `malformed_json`,
  `body_too_large`.
- **No sending or scheduling yet.** Nothing schedules or sends a campaign until those
  capabilities exist.

## Project layout

```
cmd/api/                  API entry point: wiring and graceful shutdown
cmd/seed/                 one-shot seeder: migrations, then demo data into an empty database
internal/campaign/        Campaign context: drafts, lifecycle, segment DSL, REST API
internal/customer/        Customer context: customers, contact details, permissions, cities
internal/commerce/        Commerce context: products, baskets, orders
internal/segmentation/    segment engine: references, DSL → parameterised SQL, audience counts
                          and recipient batches per channel
internal/seed/            deterministic synthetic data: catalogue, people, personas
internal/platform/        cross-cutting technical code (no business rules)
  config/                 environment configuration, logger
  database/               GORM connection pool, batch inserts, migrations on startup;
                          databasetest/ gives integration tests a throwaway database
  health/                 liveness and readiness probes
  httpserver/             router, middleware, JSON helpers, server lifecycle
migrations/               versioned SQL migrations (goose), embedded in the binary; schema tests
frontend/                 Vue 3 + TypeScript + Vite app, its Dockerfile and nginx config
compose.yaml              postgres, seed (one-shot), api, web
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
- [ ] **Basket history.** Today `basket_items` holds only open lines: a product that was bought
      or removed leaves no trace. So "added X, then bought it", "abandoned, then recovered" and
      "removed from the basket" can't be segmented, and abandonment is inferred from when open
      lines were added. Needs add and remove events.
- [ ] **Refresh aged demo data** without removing the volume, such as a reset option for the
      seeder. Today only `docker compose down -v` reseeds.
- [ ] **Product price history,** for price-drop campaigns.
- [ ] **Category hierarchy.** Categories are flat today.
- [ ] **More customer attributes,** such as birth date or gender, if segments need them. They
      are more personal data to protect.
- [ ] **A precomputed segmentation profile** (materialized view), if segment queries become slow.
- [ ] **Indexes for larger data** ([DECISIONS.md](DECISIONS.md) §23): a covering index on order
      lines, and partial indexes for completed orders and granted permissions. Today's indexes
      serve 5,000 customers.
- [ ] **Suggestions for unknown names,** such as "İstanbul" for "Istanbul", in the `unknown`
      problem, for marketers and the AI repair loop. Names must match exactly today.
- [ ] **A separate migration job in production,** with a DDL role, so the API can run with a
      DML-only role ([DECISIONS.md](DECISIONS.md) §6).
- [ ] **Segment-aware SMS limits.** Count GSM-7 and UCS-2 message segments instead of 160
      characters: Turkish letters force UCS-2, which fits 70 characters per message.
- [ ] **Optimistic locking for campaign drafts.** Concurrent edits are last-write-wins today.
- [ ] **Pagination for the campaign list.**
- [ ] **A shared error framework** with stable codes, translations and HTTP mapping at the API
      boundary. Error bodies are local to each capability's handlers today.
