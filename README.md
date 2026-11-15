# Campaign Composer

An AI-powered marketing campaign composer. A marketer describes a campaign in natural
language; an LLM proposes audience segmentation rules and SMS/email content; people review,
edit and approve it; approved campaigns are sent asynchronously to synthetic users through
mock providers, and delivery status is tracked in real time.

> **Status — Phase 6 (frontend).** On top of the Phase 1 foundation (PostgreSQL, migrations,
> health probes, structured logs, graceful shutdown), the customer and commerce model holds
> 5,000 synthetic customers and their shopping activity. The segmentation engine turns a
> campaign's segment into the customers it reaches on each channel. A dispatched campaign is
> sent by a worker through mock SMS and email providers at 100 messages per second per
> channel, now or at a scheduled time, and its progress streams live.
>
> The web app, in English or Turkish:
> - lists campaigns;
> - composes them in three steps: an audience with a live estimate, messages with previews, and
>   a review;
> - sends or schedules them after a confirmation;
> - follows their delivery live.
>
> AI arrives in the next phase.

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

## Using the web app

Open http://localhost:3000. The header switches between English and Turkish; the choice is
remembered in the browser.

- **Campaigns** lists every campaign with its status and channels. Drafts can be edited or
  deleted; anything else can be viewed.
- **New campaign** opens the composer:
  1. **Audience.** Combine conditions with AND or OR, nest groups, and exclude any condition or
     group. Countries, cities, languages and categories are chosen from lists. The estimate
     beside the builder shows how many customers match, and how many each channel can reach.
  2. **Message.** Turn SMS and email on independently. Insert `{{first_name}}`,
     `{{last_name}}` and `{{city}}` at the cursor. Counters show the limits (SMS 160 characters,
     subject 80), and previews fill the placeholders with sample values.
  3. **Review and send.** A summary and the reach on the chosen channels. Save the draft, send
     it now, or schedule it; sending asks for confirmation first.

  A draft is saved only once it is complete: problems are shown next to their field, and the
  step that has them is marked.
- **A campaign's page** shows its details and, once dispatched, its delivery live. SMS and
  email are counted separately, by status: pending, accepted, delivered, failed and skipped.

## Development

Tools: Go 1.27 (an older Go downloads it automatically from `go.mod`'s `toolchain` line),
Node ≥ 24.12 (`nvm use` picks 26 from `.nvmrc`), GNU Make, Docker.

```sh
cp .env.example .env     # host settings: DATABASE_URL, text logs, ...
make db                  # PostgreSQL in Docker
make seed                # demo data, once (applies migrations; skips a seeded database)
make run                 # API on :8080 (applies migrations on startup)
make worker              # sends dispatched campaigns; run exactly one

make web-install         # once
make web-dev             # Vite on http://localhost:5173, proxying /api and /readyz to :8080
```

`make` lists every target. The most useful:

| Target | Purpose |
|---|---|
| `make up` / `make down` / `make logs` | Full stack in Docker, waiting until it is healthy |
| `make test` | Go tests with the race detector |
| `make test-integration` | Also runs the database tests (schema, stores, seeding, segmentation, delivery) against PostgreSQL, in throwaway databases (needs `make db` and a `.env`) |
| `make seed` | Fill an empty database with the demo data |
| `make worker` | The delivery worker. Exactly one may run: the per-channel rate limits live in its process |
| `make lint` / `make fmt` | golangci-lint v2 (built into `./bin` on first use) / formatting |
| `make migrate-new name=create_campaigns` | Create the next SQL migration |
| `make migrate-status` / `migrate-up` / `migrate-down` | Inspect or move the schema version |
| `make web-test` / `web-lint` / `web-build` | Frontend unit tests, lint + type-check, production build |
| `make check` | Every Go and frontend check that needs no database |

To time the segment queries and the preparation of a campaign on the seeded dataset, after
`make db`:

```sh
TEST_DATABASE_URL='postgres://campaign:campaign@localhost:5432/campaign?sslmode=disable' \
  go test -run '^$' -bench . ./internal/segmentation ./internal/delivery
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
  Other errors are `{"error": "<code>"}`: `not_found`, `not_draft`, `already_dispatched`,
  `malformed_json`, `body_too_large`.

### Dispatch and delivery progress

| Request | Result |
|---|---|
| `POST /api/campaigns/{id}/dispatch` | 202 and the campaign, now `sending` or `scheduled`, with `Location` pointing at its progress; 409 `already_dispatched` unless it is a draft; 422 for a bad `scheduled_at` or names that no longer exist |
| `GET /api/campaigns/{id}/delivery` | 200 and a progress snapshot |
| `GET /api/campaigns/{id}/delivery/events` | Server-sent events: the snapshot each time it changes, then a `done` event once it is final |

```sh
curl -X POST localhost:3000/api/campaigns/1/dispatch                       # send now
curl -X POST localhost:3000/api/campaigns/1/dispatch \
  -d '{"scheduled_at": "2026-11-20T09:00:00+03:00"}'                        # or schedule
curl -N localhost:3000/api/campaigns/1/delivery/events                     # follow it
```

- **Campaign statuses.**
  - `scheduled` until its time.
  - `sending`, then `sent` once every message has an outcome: accepted by its provider,
    failed or skipped. `sent` doesn't mean delivered.
  - `failed` if its segment no longer validates when its dispatch starts.

  The campaign shows `scheduled_at`, `started_at` and `finished_at`.
- **Message statuses.**
  - `pending`, then `accepted` by the provider, then `delivered` or `failed` when the
    provider's receipt arrives, possibly after the campaign is `sent`.
  - `failed` also covers rejected, too long, and the provider unavailable after 5 attempts.
  - `skipped` means the customer withdrew permission or lost the contact detail before
    sending.
- **The snapshot** counts messages by status, overall and per channel. Its `final` is true
  once nothing can change. Close the `EventSource` on `done`, or it reconnects.
- **Exactly once?** Retries carry an idempotency key, so a message reaches its provider once.
  [DECISIONS.md](DECISIONS.md) §25 explains the limits.

### Segment options and audience estimate

The composer's read-only segment API ([DECISIONS.md](DECISIONS.md) §27):

| Request | Result |
|---|---|
| `GET /api/segments/options` | 200 and what a segment can name: countries, cities, categories and languages |
| `POST /api/segments/audience` | 200 and `{matched, reachable, by_channel}` for the segment in the body; 422 `invalid_segment` with the same problems as a draft |

```sh
curl -X POST localhost:3000/api/segments/audience -d '{"segment": {"version": 1,
  "match": "all", "conditions": [{"type": "city", "in": ["İstanbul"]}]}}'
```

It answers with counts only, never with customers.

## Project layout

```
cmd/api/                  API entry point: wiring and graceful shutdown
cmd/seed/                 one-shot seeder: migrations, then demo data into an empty database
cmd/worker/               delivery worker: works the River jobs; exactly one may run
internal/campaign/        Campaign context: drafts, lifecycle, dispatch, segment DSL, REST API
internal/customer/        Customer context: customers, contact details, permissions, cities
internal/commerce/        Commerce context: products, baskets, orders
internal/segmentation/    segment engine: references, DSL → parameterised SQL, audience counts
                          and recipient batches per channel; the composer's read-only API
internal/delivery/        sending: River jobs, mock providers, rate limits, progress and SSE
internal/seed/            deterministic synthetic data: catalogue, people, personas
internal/platform/        cross-cutting technical code (no business rules)
  config/                 environment configuration, logger
  database/               GORM connection pool, batch inserts, migrations on startup (goose,
                          then River's); databasetest/ gives integration tests a throwaway database
  health/                 liveness and readiness probes
  httpserver/             router, middleware, JSON helpers, server lifecycle
migrations/               versioned SQL migrations (goose), embedded in the binary; schema tests
frontend/                 Vue 3 + TypeScript + Vite app, its Dockerfile and nginx config
  src/api.ts              every API request and its JSON types
  src/campaign.ts         the draft rules mirrored from internal/campaign; placeholders
  src/i18n.ts, locales/   English and Turkish texts; dates, numbers and money via Intl
  src/views/              campaign list, composer and campaign pages
  src/components/         wizard, segment builder, message editor, estimate, delivery monitor
compose.yaml              postgres, seed (one-shot), api, worker (exactly one), web
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
      characters: Turkish letters force UCS-2, which fits 70 characters per message. The
      composer would then show how many messages an SMS takes.
- [ ] **Optimistic locking for campaign drafts.** Concurrent edits are last-write-wins today.
- [ ] **Dispatch exactly what was reviewed.** Dispatch could take the version the reviewer
      approved (`If-Match` with the campaign's ETag) and answer 412 if the draft changed since.
      Today an edit made after review is sent as edited.
- [ ] **Unschedule and cancel,** in the API and on the campaign page. The lifecycle allows
      scheduled → draft, but no endpoint does it yet, and a dispatch in progress can't be
      stopped.
- [ ] **Receipt timeout.** A real provider can lose a receipt, which would leave its message
      `accepted` and the progress never final.
- [ ] **Cross-campaign delivery statistics,** for a dashboard; statistics are per campaign today.
- [ ] **Real providers,** with receipts by webhook instead of the mocks' scheduled jobs.
- [ ] **More than one worker,** with a rate limiter shared through PostgreSQL. Exactly one worker
      may run today, because each worker process holds its own limiters
      ([DECISIONS.md](DECISIONS.md) §24).
- [ ] **Batched preparation** with a checkpoint, for audiences in the hundreds of thousands. One
      transaction prepares a campaign today, about 60 µs per message (§25).
- [ ] **Campaign completion without a lock per message.** Each message's outcome is recorded in
      a transaction that locks its campaign and checks for pending messages, so one campaign's
      outcomes are recorded one at a time: enough for 100 messages a second per channel, a
      ceiling at much higher rates. One completion job per campaign, re-checking until nothing
      is pending, could take that work off every message without letting a campaign stay
      `sending` ([DECISIONS.md](DECISIONS.md) §25).
- [ ] **Metrics** (`/metrics`) for sending throughput, failures and queue depth; logs and
      River's job table are the only view today.
- [ ] **Authentication and authorization,** such as who may dispatch. They are outside the
      assessment's scope: anyone who reaches the API can send a campaign.
- [ ] **Pagination, filters and search for the campaign list,** in the API and the web app.
- [ ] **A shared error framework** with stable codes, translations and HTTP mapping at the API
      boundary. Error bodies are local to each capability's handlers today.
- [ ] **A preview of sample recipients** in the composer. It would show personal data, so it
      needs a decision on who may see it; the estimate shows counts only today (§27).
- [ ] **Browser end-to-end tests** in CI (e.g. Playwright). Component tests run in jsdom
      today, and the main flows were checked in Chrome by hand (PHASE_REVIEWS.md, Phase 6).
- [ ] **Live statuses in the campaign list.** It shows them as they were when it loaded;
      only a campaign's own page follows them live.
- [ ] **Dark mode.** The colours are tokens on `:root`, so a dark set could be added in one
      place.
