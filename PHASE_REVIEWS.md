# Phase reviews

What each development phase built, newest first: its components, how they were tested and
verified, how the result differs from the plan, and what deserves a closer look. Entries are
snapshots taken when a phase is finished; later phases add entries rather than rewriting old
ones. The reasoning behind each choice is in [DECISIONS.md](DECISIONS.md), setup and commands
are in [README.md](README.md).

## Phase 2B: Persistence and synthetic data (2026-11-14)

**Scope.**

- Insert-only GORM persistence for the Customer and Commerce contexts.
- A deterministic, idempotent seeder that fills an empty database with 5,000 synthetic
  customers in TR, DE and GB, with their permissions, baskets and orders. Compose runs it as a
  one-shot service before the API.

Not built yet:

- reads and updates, which nothing calls yet;
- basket history, now documented as a limitation;
- Campaign, Segmentation, Delivery and AI.

**Result.**

- `make check` and `make test-integration` pass.
- Starting from an empty volume, `docker compose up --build --wait` seeds the data in 1.2 s,
  and the whole stack is healthy in about 15 s.
- A second start leaves the data alone.

### How the pieces fit

```text
postgres ──healthy──► seed ──exit 0──► api ──healthy──► web
                       │
                       ├─ migrate (idempotent, locked)
                       ├─ one transaction: advisory lock → customers exist? → stop
                       └─ generate and insert, in dependency order:
                          countries, cities → categories, products → customers, permissions
                          → orders, order lines → basket lines
```

### Components

| Component | Location | What it does | Tests |
|---|---|---|---|
| Customer store | `internal/customer/store.go` | Inserts countries, cities, customers and their permissions; checks whether customers exist | 2 integration tests |
| Commerce store | `internal/commerce/store.go` | Inserts categories, products, orders with their lines, and basket lines | 1 integration test |
| Batch insert | `internal/platform/database` (`Insert`) | GORM `CreateInBatches` in batches of 1,000, IDs returned | Through the stores |
| Seeder | `internal/seed` | Catalogue, people, personas, generator and `Run` | 15 cases, 88.2 % (74.0 % without a database) |
| Seed command | `cmd/seed` | Migrates, seeds with the default options, logs the counts | End to end in Compose |
| Test database | `internal/platform/database/databasetest` | A throwaway, migrated database per test | Used by four packages |
| Logger | `internal/platform/config` (`NewLogger`) | Shared by both commands | 2 cases |
| Compose and Makefile | `compose.yaml`, `Makefile` | `seed` service before `api`; `make seed` | End to end |

With PostgreSQL available, coverage is 94.6 % for `customer` and 95.0 % for `commerce`.

#### Persistence (`internal/customer/store.go`, `internal/commerce/store.go`)

- **Insert-only operations.** Customer: `CreateCountries`, `CreateCities`, `CreateCustomers`,
  `HasCustomers`. Commerce: `CreateCategories`, `CreateProducts`, `CreateOrders`,
  `CreateBaskets`. Nothing reads or updates yet.
- **Row structs.** Unexported row structs with explicit table names map the domain types.
  GORM's automatic timestamps are off, so generated times are stored as given, and a missing
  phone is stored as NULL.
- **IDs.** PostgreSQL returns them, and they are written back into the aggregates or reference
  structs. Saving a customer or order that already has an ID fails.
- **Transactions.** Stores take a `*gorm.DB` that may be a transaction; the seeder owns the
  transaction.

#### Seeder (`internal/seed`)

- **Geography and catalogue** (`catalog.go`):
  - 3 countries and 28 cities, with population-like weights;
  - 8 categories × 3 brands × 7 items = 168 products, priced in TRY and ending in ,90.
- **People** (`people.go`):
  - 20 first and 20 last names per language;
  - emails such as `ayse.yilmaz.42@example.com` on domains reserved for examples;
  - synthetic mobile numbers, made unique by the customer's sequence number.
- **Personas** (`generate.go`): new 8 %, browser 17 %, occasional 30 %, regular 20 %, loyal
  10 %, lapsed 15 %. Each has its own signup window, number and timing of orders, and chance of
  an open basket.
- **Determinism.**
  - One PCG random source with seed 20261114.
  - Times are whole seconds, relative to the seeding time.
  - The generator never iterates over a map.
- **`Run`.**
  - One transaction: take the advisory lock, then skip if customers exist.
  - `build` hands each batch to a `sink` that inserts it, so later batches get the IDs they
    refer to.
  - Tests use an in-memory sink that numbers the rows instead, so they exercise the same
    generation code.

#### Compose (`compose.yaml`)

- `seed` runs `/app/seed` from the backend image once postgres is healthy.
- `api` waits for `seed` to complete successfully.
- The database URL is defined once, in `x-database-url`.

### The dataset

| Rows | Count |
|---|---|
| Customers | 5,000 (designed split: TR 60 %, DE 25 %, GB 15 %) |
| Permission rows | 8,059 |
| Products | 168 |
| Orders | 18,791 (6.1 % cancelled, 3.7 % refunded) |
| Order lines | 35,488 |
| Basket lines | 3,560 |

The tests guarantee these segments. The counts are from the default data:

| Segment | Customers | Guaranteed |
|---|---|---|
| İstanbul, basket added in the last 30 days, no completed order since, SMS allowed | 56 | ≥ 25 |
| Never completed an order | 1,110 | ≥ 15 % |
| Lapsed: last completed order 180+ days ago | 1,404 | ≥ 10 % |
| Loyal: 10+ completed orders | 376 | ≥ 5 % |
| Signed up in the last 30 days | 398 | ≥ 5 % |
| German customers who prefer Turkish | 321 | ≥ 15 % of DE |
| No phone | 498 | ≥ 5 % |
| Withdrew a permission | 504 | ≥ 2 % |

Every city has customers, and every category has buyers in the last 90 days.

### Verification

| # | Check | Result |
|---|---|---|
| 1 | `make check` | Pass: Go tests with `-race`; lint with 0 issues; frontend tests (6) and build |
| 2 | `make test-integration` | Pass. The stores round-trip their rows. `Run` inserts exactly the generated dataset. The example segment as SQL matches the in-memory count (56). A second run skips, and of two concurrent runs, only one seeds |
| 3 | Fresh stack: `docker compose down -v && docker compose up --build --wait` | Pass. The seed applied migrations 1–3, seeded in 1.233 s and exited 0. api and web became healthy. `--wait` accepts the completed one-shot service. About 15 s in total |
| 4 | Second `docker compose up` | Pass: the seed logged "nothing to seed" and the row counts are unchanged |
| 5 | `make seed` on the host | Pass: it applies migrations and leaves the seeded database alone |
| 6 | EXPLAIN ANALYZE of the example segment | Pass: 56 customers in 1.5 ms. It uses `customers_city_id_idx`, the permissions primary key and `orders_customer_id_ordered_at_idx`; `basket_items` is small enough for a sequential scan |

### Deviations from the plan

- **Logging:** `Run` returns a `Result` and `cmd/seed` logs it; `Run` no longer takes a logger.
- **Batch insert:** both stores use a shared `database.Insert` in the platform package instead
  of each having its own copy.
- **Permission rows:** there are 8,059 instead of the estimated ~6,500, because declined
  answers are stored too, as rows with `granted = false`.
- **Further improvements:** the README list gained "refresh aged demo data without removing
  the volume".

### Worth a closer look

- **The API now depends on the seeder.** A failing seed blocks `docker compose up`. That is
  deliberate, so the demo is never empty, but it ties API availability to demo data in
  Compose. A production deployment wouldn't run the seed service.
- **Data ages in a persisted volume.** Everything is relative to the seeding time, so weeks
  later the "last 30 days" segments shrink. `docker compose down -v` refreshes the data.
- **Reproducibility across Go versions.** The dataset is reproducible with a given Go version.
  If a future release changes how `math/rand/v2` derives values such as `IntN` from the
  source, the exact rows could change. The tests compare two runs with each other rather than
  with fixed values.
- **Realistic-looking phone numbers.** They are synthetic but sit in plausible mobile ranges,
  so only mock providers may ever use them.
- **No read code yet.** The segmentation phase will read these tables directly (DECISIONS.md
  §13).

## Phase 2A: Customer and Commerce (2026-11-13)

**Scope.** The Customer and Commerce bounded contexts: their schema (two migrations) and domain
model, shaped for segmentation queries and for the seeder. Decisions taken while planning:

- customers in several countries (TR, DE, GB), speaking `tr`, `de` or `en`;
- an optional phone;
- orders that are `completed`, `cancelled` or `refunded`, refunds always in full;
- bigint identity keys;
- every price in TRY.

Not built yet: persistence code and synthetic data (Phase 2B), the segmentation engine,
campaigns, AI and delivery.

**Result.** `make check` and `make test-integration` pass, and the running API applied both
migrations on startup.

### How the pieces fit

```text
countries ─< cities ─< customers ─< notification_permissions
                         │  │
                         │  └─< orders ─< order_items ─┐
                         │                             ├─> products ─> categories
                         └─< basket_items ─────────────┘
```

`─<` means "has many" and `─>` means "refers to". The first row is the Customer context; the
rest is Commerce, which refers to customers by ID. Nothing writes these tables yet:

- the seeder (Phase 2B) will write them, through the domain types;
- the segmentation engine will read them directly, in single SQL statements (DECISIONS.md §13).

### Components

| Component | Location | What it does | Tests |
|---|---|---|---|
| Customer schema | `migrations/00002_customer.sql` | Countries, cities, customers and their marketing permissions | Schema tests |
| Commerce schema | `migrations/00003_commerce.sql` | Categories, products, basket lines, orders and order lines | Schema tests |
| Customer domain | `internal/customer` | Customer aggregate with its permissions; Email, Phone, Language, Channel; Country, City | 42 cases, 98.6 % |
| Commerce domain | `internal/commerce` | Product, Basket and Order; Money; Category | 34 cases, 100 % |
| Schema tests | `migrations/migrations_test.go` | Every constraint, the delete rules and the migration round trip, against real PostgreSQL | 32 cases, via `make test-integration` |
| Tooling and docs | `Makefile`, `DECISIONS.md`, `README.md`, `CLAUDE.md` | `make test-integration`; decisions 13–17; the further-improvements list | — |

#### Schema (`migrations/00002_customer.sql`, `migrations/00003_commerce.sql`)

- **Keys.** Every table has an identity key, except `countries`, which is keyed by its ISO code.
  Composite primary keys make two rules structural: one permission per channel, and one line
  per product in baskets and orders.
- **CHECK constraints.** They mirror the domain's closed sets and ranges:
  - E.164 phones;
  - languages, channels and order statuses;
  - positive quantities and prices;
  - no status change before its order.
- **Deletes.** They cascade only to what belongs to the parent: permissions, basket lines and
  order lines. Deleting a customer with orders, or a product in use, fails.
- **Indexes.** They serve the segment joins:
  - `customers (city_id)`;
  - `orders (customer_id, ordered_at)`;
  - `basket_items (product_id)` and `order_items (product_id)`;
  - `products (category_id)`;
  - the unique `cities (country_code, name)`, which doubles as the country filter.

#### Customer domain (`internal/customer`)

- **New customers.** `New(profile, createdAt)` checks the trimmed names, the email, the city
  and the language. A new customer has no permissions, so every channel starts as not permitted.
- **Permissions.** `Grant`, `Revoke`, `Permitted` and `Permissions` manage marketing
  permissions.
  - Granting SMS to a customer without a phone fails with `ErrUnreachable`.
  - No change can predate the signup.
- **Contact details.**
  - `ParseEmail` trims, lowercases and accepts only a single plain address.
  - `ParsePhone` strips formatting and requires E.164.
  - Their errors never repeat the input, and they log masked values (`***@example.com`,
    `+90********67`).
- **Closed sets and reference data.** `Language` and `Channel` match the CHECK constraints;
  `Country` and `City` are plain reference data.

#### Commerce domain (`internal/commerce`)

- **Products.** `NewProduct` requires a category, a name and a price above zero. `Category` is
  plain reference data.
- **Baskets.** `Basket.Add` keeps one line per product: adding again raises the quantity and
  keeps the latest add time.
- **Orders.**
  - `NewOrder` requires lines: one per product, each with a quantity above zero and a unit
    price of zero or more. A new order starts `completed`.
  - `Cancel` and `Refund` work only from `completed`, only once, and never before the order
    time.
  - `Total` is computed. Lines are copied in and out, so callers can't change them.
- **Money.** An int64 amount in kuruş.

#### Schema tests (`migrations/migrations_test.go`)

- **Skipped by default.** They run only when `TEST_DATABASE_URL` is set, so `make test` and
  `make check` still need no database.
- **Throwaway databases.** Each test creates a `campaign_test_*` database, migrates it with the
  application's own `database.Open` and `database.Migrate`, and drops it afterwards.
- **Coverage.**
  - 30 table-driven cases each break one constraint and assert its name.
  - One test checks the cascading deletes.
  - One test migrates down to version 0 and back up.

### Verification

| # | Check | Result |
|---|---|---|
| 1 | `make check` | Pass: Go tests with `-race`, `make lint` with 0 issues, frontend lint, type-check, 6 tests and build |
| 2 | `make test-integration` | Pass: all 30 constraint cases, the cascading deletes, and migrations down to 0 and back up. No test database was left behind |
| 3 | Migrations on startup | Pass: the rebuilt API logged versions 2 and 3 as applied, `make migrate-status` lists all three, and `/readyz` answers 200 through nginx |
| 4 | Example segment query | Pass: it runs on the real schema. On empty tables, EXPLAIN uses `customers_city_id_idx`, `basket_items_pkey`, `orders_customer_id_ordered_at_idx` and `notification_permissions_pkey` |
| 5 | Domain invariants | Pass: every invariant and status transition has a test case. The only uncovered statement is a defensive branch for unknown channels |

Not checked: query plans at realistic volumes, which need the seeder's data.

### Deviations from the plan

- **Additions:**
  - a CHECK that country codes are two capital letters;
  - the cascading-delete test;
  - a schema test case that a customer with orders can't be deleted.
- **Unplanned file:** `internal/commerce/commerce.go` holds the package documentation and errors.
- **No tests for reference data:** `Country`, `City` and `Category` are plain structs with no
  behaviour yet.
- **Personal data:** validation errors never repeat the rejected value, because it may be
  personal data.
- **CLAUDE.md:** two new conventions:
  - the no-personal-data rule now covers error messages;
  - domain code takes the current time as a parameter.

### Worth a closer look

- **Domain-only rules.** The database can't enforce these:
  - SMS permission needs a phone;
  - an order has lines;
  - the order status lifecycle.

  A writer that bypasses the domain types, such as raw SQL or psql, can break them, so the
  seeder must use the domain types.
- **Segmentation reads these tables directly** (DECISIONS.md §13). Renaming a column will break
  segment queries, so once those queries exist, their tests should run against this schema.
- **`make test-integration` isn't part of `make check`.** CI would need a PostgreSQL service.
  Until then, run it whenever migrations change.
- **Everything is priced in TRY,** including for customers in Germany and the UK. Spend segments
  are therefore only meaningful in TRY.
- **No persistence code yet.** The GORM mapping arrives with the seeder, and IDs stay 0 until
  then.

What this phase deferred is on README.md's further-improvements list. What "haven't purchased"
means in a segment is an open question in DECISIONS.md.

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
