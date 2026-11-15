# Architecture decisions

Each entry records the context, the decision and its consequences. Entries are appended as
the project evolves; a superseded decision is marked as such rather than deleted.
Phase 1 (project foundation, entries 1–12) and Phase 2A (customer and commerce, 13–17)
decisions were recorded on 2026-11-13; Phase 2B (seeding, 18), Phase 3 (campaigns, 19–21),
Phase 4 (segmentation, 22–23) and Phase 5 (delivery, 24–26) on 2026-11-14, and Phase 6
(frontend, 27–28) and Phase 7 (AI assistant, 29) on 2026-11-15.

## 1. One repository: Go module at the root, Vue app in `frontend/`

**Context.** The repository holds the Go API and the Vue app. The module path
`github.com/bengisupinar/campaign` is only valid if `go.mod` sits at the repository root.

**Decision.** `go.mod` lives at the root; `frontend/` is self-contained (its own
`package.json`, Dockerfile and nginx config). `go.mod` declares `ignore ./frontend`
(Go 1.25+), because `frontend/node_modules` contains Go source — the npm package `flatted`,
an ESLint dependency, ships `golang/pkg/flatted/flatted.go` — which `./...` would otherwise
build, vet and lint.

**Consequences.** Idiomatic import paths and Go tooling run from the root. Requires Go ≥ 1.25.

## 2. Packages by business capability

**Context.** Code must be organised by business capability, not by technical layer.
Phase 1 contains no business capability yet.

**Decision.** Each capability is one package, `internal/<capability>` (e.g. `campaign`,
`audience`, `delivery`), holding its HTTP handlers, domain logic and persistence as files.
Cross-cutting technical code lives in `internal/platform/` (`config`, `database`, `health`,
`httpserver`) and contains no business rules. There are no `handlers/`, `services/`,
`repositories/` or `models/` packages and no per-layer sub-packages. DDD's tactical patterns
are applied where invariants exist (the campaign lifecycle), not to plain CRUD.

**Consequences.** A feature changes in one place, and dependencies between capabilities stay
explicit. A capability is split into sub-packages only when its size demands it.

## 3. Go 1.27 with `net/http` and chi

**Decision.** Go 1.27, the latest stable release (`go 1.27.0`, `toolchain go1.27.4`; an older
local Go downloads that toolchain automatically). chi provides routing, route groups and
request IDs while handlers stay plain `http.HandlerFunc`s. Access logging and panic recovery
are small custom middleware, because chi's recoverer prints unstructured stack traces.

**Consequences.** One small dependency without transitive dependencies; no framework-specific
context type; every handler is testable with `httptest`.

## 4. Configuration from the environment only

**Decision.** `caarlos0/env` parses a typed `Config` with defaults and required fields. Every
missing or invalid variable is reported at once and the process exits non-zero. The binary
never reads `.env`: the Makefile loads it for host runs and Compose sets each service's
environment explicitly. `Config` implements `slog.LogValuer` and omits the database URL.

**Consequences.** Twelve-factor configuration; tests inject an environment instead of
mutating the process; secrets never end up in images or logs.

## 5. PostgreSQL 18 with GORM

**Context.** Implementation speed was prioritised over hand-written SQL; DDD and reliability
still matter.

**Decision.** GORM over pgx, with guardrails:

- the schema changes only through migrations (decision 6), never `AutoMigrate`;
- no `gorm.Model` embedding (it adds implicit soft deletes);
- partial updates via `Select` or maps, because struct `Updates` silently skips zero values;
- LLM output is never interpolated into `Where` strings — segment rules map through a
  whitelist of columns and operators;
- GORM stays inside each capability's persistence code;
- error translation is on (e.g. `gorm.ErrDuplicatedKey`), and SQL is logged parameterised
  at warn level with a 200 ms slow-query threshold, so bind values (personal data) are never logged;
- explicit pool limits (20 open/idle connections, 30 min lifetime, 5 min idle time) and a
  5 s ping at startup.

Alternatives: sqlc (compile-time checked SQL, but a code-generation step and weak support for
the dynamic filters segmentation needs) and hand-written pgx (most control, most code).

**Consequences.** Faster CRUD development. Query mistakes surface at runtime rather than at
compile time, so persistence code needs integration tests. Some Go reviewers consider GORM
non-idiomatic; that is accepted in exchange for delivery speed.

## 6. Versioned SQL migrations (goose), applied on startup

**Decision.** SQL migrations in `migrations/` are embedded in the binary. The API applies
pending migrations before it starts listening, serialised by a PostgreSQL advisory lock so
concurrent instances are safe. Developers use the goose CLI at the version pinned in `go.mod`
through `go run` (`make migrate-*`). `00001_baseline.sql` is a deliberate no-op that starts
version tracking before the first table exists.

Alternatives: GORM `AutoMigrate` (no rollback, cannot rename or drop columns, implicit DDL
derived from struct tags) and a separate migration job (cleaner privileges, one more moving part).

**Consequences.** `docker compose up` needs no extra step. The API's database role needs DDL
rights; in production, migrations would run as a separate job with a privileged role and the
API would get a DML-only role. A long migration delays readiness.

## 7. Observability: structured logs, request IDs, health probes

**Decision.** `log/slog` writes JSON (text locally). Every request produces one access-log line
with `request_id`, `method`, `path`, `status`, `bytes` and `duration_ms`; 5xx responses log at
error level and successful health probes at debug level, so the 10-second healthcheck does
not drown real traffic. nginx sets `X-Request-Id`, which chi adopts, so web and API logs
correlate. `GET /healthz` (liveness) checks no dependencies, so a database outage never
restarts a healthy process; `GET /readyz` (readiness) pings PostgreSQL within 2 s and answers
503 on failure, logging the details instead of returning them.

**Consequences.** Outages show up as "unhealthy", not as restart loops; probe responses
leak no internals.

## 8. Graceful shutdown

**Decision.** On SIGINT/SIGTERM the server stops accepting connections, waits up to 10 s for
in-flight requests, then closes the database pool and exits 0. Request contexts are not
derived from the signal context, so a shutdown never cancels requests already in progress.
Compose's `stop_grace_period` is 15 s. There is no readiness "drain" flag: without a load
balancer's pre-stop delay it has no effect, because the listener closes immediately.

**Consequences.** Restarts and deploys do not cut requests short. Long-lived streams (SSE)
must watch for shutdown explicitly when they are added.

## 9. Single-command startup with Docker Compose

**Context.** The assessment requires `docker compose up` to start the application, and the
final system will also include the frontend, seed data and mock providers.

**Decision.** `compose.yaml` runs `postgres`, `api` and `web`. Every variable has a development
default, so no `.env` is needed. Healthchecks order startup: postgres → api (`/readyz`) → web;
since decision 18, a one-shot `seed` service runs between postgres and api.
The backend image builds every `./cmd/*` binary, so future one-shot or long-running services
(seed, mock provider, worker) are new Compose services reusing the image with a different
`command`. The runtime image is `alpine` running as uid 10001 rather than distroless: busybox
`wget` serves the healthcheck and a shell helps debugging. PostgreSQL 18 images keep their data
under `/var/lib/postgresql/18/docker`, so the volume is mounted at `/var/lib/postgresql`.

**Consequences.** Reviewers run one command. The credentials in `compose.yaml` are
development-only and must never be reused elsewhere.

## 10. Frontend: Vue 3 + TypeScript + Vite, served by nginx

**Decision.** Scaffolded with create-vue (TypeScript, Vitest, ESLint with Oxlint, Prettier).
Vue Router, Pinia and a UI component library are added with the first real screens. In
development, Vite proxies `/api` and `/readyz` to the API on :8080; in Compose, Node 26 builds
the app and `nginx-unprivileged` 1.30 serves it with an SPA fallback, proxies `/api/` and
`/readyz` to the API, and resolves `api` through Docker DNS at request time (verified by
recreating the API container on a new IP). Both paths are same-origin, so the API needs no CORS.
Node 26 (LTS) is pinned in `.nvmrc`; Vite requires Node ^22.18 or ≥ 24.12.

Alternative: embedding the built SPA in the Go binary (a single artifact, but frontend and
backend builds become coupled).

**Consequences.** Frontend and backend build independently. Server-sent events through nginx
will need `proxy_buffering off` on their location.

*Revised by decision 28:* only Vue Router was added; state stays local and the styling is
plain CSS. Decision 26 settled the event streams with `X-Accel-Buffering: no`.

## 11. Redis deferred; queue technology still open

**Context.** Redis Streams was the initial choice for asynchronous sending, but Phase 1 has no
producer or consumer.

**Decision.** No Redis in Phase 1; the queue is chosen in the asynchronous-sending phase between:

- **a PostgreSQL-backed queue** (River, or a `FOR UPDATE SKIP LOCKED` table): jobs are enqueued
  in the same transaction as the campaign state, so there is no dual write or outbox; scheduled
  jobs are native (scheduled campaigns); one fewer component to run;
- **Redis Streams:** higher throughput and consumer groups, but enqueueing needs a
  transactional outbox, and delayed delivery needs a separate mechanism.

**Consequences.** Either way delivery is at-least-once, so each send needs an idempotency key
per (campaign, recipient).

*Settled by decision 24:* the PostgreSQL-backed option, with River.

## 12. Tooling

**Decision.** The Makefile is the development entry point and works with GNU Make 3.81 (the
macOS default); it exports only the keys defined in `.env`, and command-line values win.
golangci-lint v2.15.0 is built with the module's Go toolchain into `./bin` (pinned, so global
installs stay untouched — golangci-lint must be built with a Go at least as new as the code it
lints). The goose CLI runs through `go run` at the library version in `go.mod`. `make web-lint`
checks without auto-fixing, unlike the scaffold's `npm run lint`.

## 13. Customer and Commerce are read models for segmentation

**Context.** Nothing in this system edits customers or places orders. The seeder writes the
data, and opt-outs from the delivery phase will be the only later writes. Segments read both
contexts at once.

**Decision.** `internal/customer` and `internal/commerce` own every write, through thin domain
types: value objects and three aggregates (Customer, Basket, Order) whose constructors and
methods guard the invariants. They carry no GORM tags, take the current time as a parameter
and get persistence code with their first caller, the seeder. The segmentation phase will read
both contexts' tables directly in single SQL statements: a deliberate, read-only coupling that
keeps a segment to one query instead of joins in memory.

**Consequences.** Schema changes in these contexts must be checked against segmentation
queries. Commerce refers to customers by ID only, while foreign keys cross the two contexts
because they share one database.

## 14. One open basket per customer; orders keep the price paid

**Decision.** A basket has no table of its own: its lines are `basket_items` rows keyed by
(customer, product), and "abandoned" is derived from `added_at` and later orders. Orders and
their lines are separate tables but one aggregate. Lines never change and record the unit price
paid, so products need no price history. A new order is `completed`; it can be cancelled or
refunded in full, and both are final. Only completed orders count as purchases or spend.
Totals are computed, never stored.

**Consequences.** No basket history (abandoned, then recovered), partial refunds or price-drop
campaigns; all three are on the README's further-improvements list.

## 15. Marketing permissions belong to the Customer aggregate

**Decision.** `notification_permissions` holds one row per customer and channel (`sms`,
`email`): granted or not, and since when. A missing row means "not permitted", because
commercial messages need prior opt-in (Turkish e-commerce law and İYS, GDPR). Permissions are
part of the Customer aggregate because one rule spans them and the contact points: SMS can only
be granted to a customer with a phone. A table rather than a column per channel keeps the
channel a bind parameter.

**Consequences.** Only the current state is kept, not a consent history. A segment is
evaluated at one moment, so delivery must re-check the permission just before sending.

## 16. Countries, languages and money

**Decision.** Countries are keyed by their ISO 3166-1 alpha-2 code (`TR`, `DE`, `GB`); each
city belongs to one country, and its name is unique there. A customer has one preferred
language, `tr`, `de` or `en`, checked by a Go type and a CHECK constraint rather than a lookup
table, because supporting another language needs content generation anyway. Money is an int64
amount in kuruş, and every price is in TRY.

**Consequences.** Customers in Germany and the UK pay in TRY, a simplification; multi-currency
is on the further-improvements list.

## 17. Identity keys, and where business rules live

**Decision.** Primary keys are `bigint GENERATED ALWAYS AS IDENTITY`. The database enforces
what it can state declaratively and every writer must respect: uniqueness, foreign keys,
closed sets and ranges. The domain enforces rules that need context or span rows: a
permission needs a reachable channel, an order needs lines and allowed status changes. Small
closed sets are checked in both. `make test-integration` checks every constraint against a real
PostgreSQL, in throwaway databases.

Alternative: UUIDv7 keys (`uuidv7()` is built into PostgreSQL 18) would let the domain create
IDs before saving, at 16 bytes per key.

**Consequences.** New aggregates have ID 0 until they are saved. A writer that bypasses the
domain (raw SQL, psql) can break the domain-only rules, so the seeder must go through the
domain types.

## 18. Synthetic data seeding

**Context.** Segmentation and campaigns need realistic customers and shopping activity, and
`docker compose up` must start everything, seed data included.

**Decision.** A separate `cmd/seed` binary, run by a one-shot Compose service between postgres
and api, applies migrations and fills an empty database with 5,000 customers in TR, DE and GB,
their permissions, baskets and orders, and a 168-product catalogue.

- **Idempotent:** one transaction under a PostgreSQL advisory lock that does nothing when
  customers exist, so it runs on every start and concurrent runs seed once. Removing the volume
  (`docker compose down -v`) reseeds.
- **Deterministic:** one PCG random source with a fixed seed, and every timestamp relative to
  the seeding time, so the same seed and time give the same data and "the last 30 days" always
  has activity.
- **Representative:** six personas (new, browser, occasional, regular, loyal, lapsed) guarantee
  every segment worth demonstrating, and tests assert a minimum size for each. Names, cities
  and products are hand-written lists. Emails use domains reserved for examples (RFC 2606) and
  phone numbers are synthetic.
- **Through the domain:** every row is built by the domain constructors, so the Phase 2A
  invariants hold, and written by insert-only GORM stores in `customer` and `commerce`, in
  batches of 1,000. About 70,000 rows take a second or two.

Alternatives: seeding inside the API on startup (demo code in the production binary, slower
starts); a faker library (another dependency, poor Turkish data, no guaranteed segment
coverage); `COPY` (bypasses GORM, unnecessary at this size).

**Consequences.** The API starts only after seeding succeeds, so a failed seed stops
`docker compose up` instead of serving an empty demo. Data relative to the seeding time ages
in a persisted volume; recreating the volume refreshes it. `basket_items` holds only open
lines, so basket history can't be segmented (README, further improvements).

## 19. Campaigns: a minimal lifecycle and a draft-only API

**Context.** Marketers write campaigns by hand now and with AI later. Nothing can schedule or
send a campaign yet.

**Decision.**

- **Lifecycle.** A campaign is `draft`, `scheduled` or `sending`. The allowed moves are:
  - draft → scheduled (approved with a send time);
  - draft → sending (approved to send now);
  - scheduled → draft (unscheduled, to edit again);
  - scheduled → sending (its time arrived).

  How a campaign finishes is decided with delivery, which will extend the transitions and the
  CHECK constraint.
- **Only drafts change.** Only a draft can be updated or deleted, and deletes are hard deletes.
  Writes are conditional (`WHERE status = 'draft'`), so they can't race a later status change.
- **One `Service`.** It holds the use cases; HTTP handlers are thin adapters around it, and AI
  proposals will build the same `Draft`. The service depends on a small repository interface,
  which the GORM store implements and the API tests fake.
- **The REST API under `/api/campaigns`** creates, lists, gets, replaces (`PUT`) and deletes
  drafts. No endpoint schedules or sends until those capabilities exist.
- **Every save must be complete and valid.**
- **Errors.** Error bodies carry stable codes and live in the campaign handlers; the shared
  error framework is deferred.

**Consequences.** Concurrent edits of a draft are last-write-wins; optimistic locking arrives
with approval. Lists aren't paginated yet.

## 20. Segment rules: a typed JSON DSL, version 1

**Context.** Segments come from marketers and, later, from an LLM. They are untrusted input
that the segmentation engine must turn into safe SQL.

**Decision.** A segment is `{"version": 1, "match": "all"|"any", "conditions": [...]}`.

- **Typed conditions.** Each condition has a `type` from a closed set: `country`, `city`,
  `language`, `signed_up`, `basket`, `purchased`, `order_count`, `total_spent` and `group`.
  Each type takes its own parameters (`in`, `within_days`, `categories`, `min`/`max`,
  `match`/`conditions`), and every condition accepts `negate`. *Amended by decision 22:*
  `total_spent` took `min_try`/`max_try` in whole lira until Phase 4. It now takes
  `min`/`max` in kuruş, like `order_count`'s bounds.
- **Strict validation.** Unknown types and parameters are rejected.
- **Limits.** Depth 3, 20 conditions, 50 values per list, windows of 1–3,650 days.
- **One SQL pattern per condition.** Each condition maps to one parameterised SQL fragment from
  the Phase 2A query patterns.
- **Consent is implicit:** the engine always requires permission for the campaign's channels.
- **Names, not IDs.** Cities and categories are referenced by name, which is readable and
  stable across reseeds. Phase 3 checks their format; the segmentation phase checks that they
  exist.
- **Storage.** Segments are stored as jsonb.

Alternatives:

- generic field/operator/value triples, which need a compatibility table of fields and
  operators and still need parameters for time windows;
- SQL fragments, never, because LLM output is untrusted.

**Consequences.** "Haven't purchased" is `purchased` (with or without a window) negated. "Not
since the basket add" can't be expressed in version 1.

## 21. Campaign content and its validation

**Decision.**

- **Content blocks.** Content has an optional block per channel: `sms` (body) and `email`
  (subject, body). The campaign's channels follow from the blocks. They are stored in nullable
  columns with CHECK constraints.
- **Every problem at once.** Validation reports every problem together, each with a field path
  and a stable code (`required`, `too_long`, `unknown_placeholder`…). Forms can show every
  error, and the AI phase can feed the list back to the model.
- **Length limits.** They count Unicode characters of the text as written:
  - name, at most 100;
  - SMS, at most 160;
  - email subject, at most 80.
- **Placeholders.** Only `{{first_name}}`, `{{last_name}}` and `{{city}}` are allowed. Anything
  else, or unbalanced braces, is rejected.

**Consequences.**

- **Segmentation of long texts.** 160 characters is the GSM-7 single-message limit. Turkish
  letters force UCS-2, with 70 characters per message, so real messages may be split.
  Segment-aware limits are on the further-improvements list.
- **Rendered length.** It depends on the customer, so delivery checks it.
- **Multilingual campaigns** will move content into a table keyed by channel and language.

## 22. Segmentation engine: the DSL compiled to parameterised SQL

**Context.** Segments are untrusted input, from marketers now and from an LLM later. Two
callers must agree on who a segment reaches: audience estimates (the AI agent's tools, review)
and dispatch (Phase 5).

**Decision.** `internal/segmentation` evaluates `campaign.Segment`; `campaign` keeps owning the
DSL and its structural validation.

- **A pure compiler.** Each condition type maps to one fixed SQL fragment over `customers c`.
  Values (names, codes, bounds, times) only travel as bind arguments, with lists expanded by
  GORM, so nothing from a segment ever becomes SQL text. Every fragment is non-null (`EXISTS`,
  `IN` over non-null columns, `COALESCE`d sums), so `NOT` behaves. An empty segment compiles to
  `TRUE`: every customer, subject to eligibility.
- **Same-row filters.** A basket condition's window and categories hold for the same basket
  line, and a purchase condition's for the same order.
- **Completed orders only.** Cancelled and refunded orders never count for `purchased`,
  `order_count` or `total_spent`. `total_spent` sums quantity × unit price over completed
  orders' lines.
- **One pair of bounds, money in kuruş.**
  - `order_count` and `total_spent` both take `min` and `max`, inclusive, neither negative,
    and `min` not above `max`.
  - For `total_spent` they are kuruş, the unit of `commerce.Money` and of the prices paid, so
    the compiler compares them with the sum as stored, without conversion: `"min": 100000` is
    1,000 TRY.
  - The DSL's earlier `min_try`/`max_try` in whole lira are gone.
- **`AsOf` bounds windows; it is not a snapshot.** The caller passes the reference time, and
  "the last 30 days" means `AsOf − 30 × 24h` to `AsOf`. Everything else is read as it is when
  the query runs, so the same query can find a different audience later, and rows dated after
  `AsOf` still count for conditions without a window.
- **Eligibility per channel.**
  - Email needs an address and a granted email permission; SMS needs a phone and a granted
    SMS permission.
  - Channels are independent: a two-channel campaign reaches each customer on the channels
    they allow and never requires both.
  - Formats are the domain's job (decision 17), so only presence is tested. A granted SMS
    permission without a phone, which the domain refuses but the table can hold, is not
    trusted.
  - One fixed SQL fragment selects both channels' eligibility; the campaign's channels are
    bind parameters, so the query text never depends on them.
- **One service, two questions.**
  - `CountAudience` returns, in one statement, the customers matched, those reachable on at
    least one channel and those reachable on each.
  - `Recipients` returns up to 1,000 reachable customers after a customer ID, by keyset
    pagination on the primary key. Each carries contact details only for the channels they
    can receive.
  - Both run the same validation, reference check and compiled WHERE clause, so estimates and
    dispatch agree; a test pages through every batch of the seeded data and compares with the
    counts.
- **References.**
  - Every country code, city and category a segment names must exist, matched exactly (case
    included). Unknown ones are problems with code `unknown` at their field path.
  - `campaign` owns the DSL, so it lists the names with their field paths
    (`Segment.References`). The segmentation service looks them all up in one query.
  - Campaign drafts are checked on create and update through a `referenceChecker` interface
    that `campaign` declares and the segmentation service implements (wired in `cmd/api`), so
    `campaign` doesn't import the engine.
  - Unknown references are reported with the draft's other problems. A segment with problems
    of its own gets no reference report, so a malformed name is never also called unknown.
- **No REST API.** The AI agent's tools and delivery will call the service directly.
  *Amended by decision 27:* the composer gets a read-only estimate and the names a segment can
  use.

**Consequences.**

- **Permissions are read when the query runs.** Delivery must check each recipient's
  permission again immediately before sending (Phase 5).
- **Batches see changes made between them.** A customer who revokes mid-dispatch drops out of
  later batches, and new customers above the cursor join them.
- **Saving a draft queries the database:** one reference lookup, when the segment names
  anything.
- **Segment authors write amounts in kuruş, the AI agent included.** The AI phase must say so
  in the tool's schema and examples: a model that writes lira targets spends 100 times
  smaller.
- **DSL version 1 changed in place.** No stored or released campaign used `min_try` or
  `max_try`, and the API now rejects them as unknown fields.

## 23. Segment query performance: measured, no new indexes

**Context.** Phase 4 decided to measure query plans before adding indexes.

**Decision.** No new indexes. EXPLAIN ANALYZE on the seeded dataset (5,000 customers, about
18,800 orders) shows every per-customer probe using an existing index:

- cities and countries: `customers (city_id)`;
- basket lines: the `basket_items` primary key (customer first), or `basket_items (product_id)`
  when the planner starts from a few categories;
- orders: `orders (customer_id, ordered_at)`, with the window in the index condition; the
  status filter removes almost nothing;
- order lines: the `order_items` primary key;
- permissions: the `notification_permissions` primary key per customer, or one hashed scan of
  the table;
- recipient batches: a range scan of the `customers` primary key that the `LIMIT` stops. Its
  cost doesn't grow with the page: 3.4 ms for the first, 3.2 ms after customer 4,000.

`BenchmarkAudience`, on an Apple M2 with PostgreSQL 18 in Docker and statistics gathered,
measures whole service calls, validation and reference checks included (medians of five
runs; repeated runs vary by about 10 %):

| Segment | `CountAudience` | First batch of 1,000 recipients |
|---|---|---|
| Every customer | 2.6 ms | 4.3 ms |
| The Phase 2A example | 1.6 ms | 4.0 ms |
| Nested: two countries; a basket line in two categories in 14 days, or 3+ orders and 5,000+ TRY in a year; no purchase in 7 days | 53 ms | 25 ms |

Measuring changed one query: the count materialises the audience in a CTE. Inlined,
PostgreSQL tested every permission twice, and counting every customer took about 7 times
longer (17 ms against 2.4 ms, side by side).

Candidates for larger data, all deferred:

- a covering index `order_items (order_id) INCLUDE (quantity, unit_price_minor)`, since the
  nested segment's cost is per-customer order aggregation;
- a partial index on completed orders;
- a partial index on granted permissions;
- a precomputed per-customer profile (README, further improvements).

**Consequences.** Aggregation conditions over long windows (`order_count`, `total_spent`)
cost time in proportion to the customers that reach them; at 100 times the data they would
take seconds and justify the candidates. To repeat the measurement, run
`go test -run '^$' -bench . ./internal/segmentation` with `TEST_DATABASE_URL` set.

## 24. Job queue: River on PostgreSQL, worked by one process

**Context.** Approved campaigns are sent asynchronously, scheduled ones at their time.
Decision 11 left the queue open between a PostgreSQL-backed queue and Redis Streams. The
providers allow 100 requests per second per channel.

**Decision.**

- **River** (riverqueue/river v0.51.2) runs on the existing PostgreSQL, through its
  `database/sql` driver on GORM's pool.
  - That driver can't LISTEN, so River runs in poll-only mode; it polls every 200 ms.
  - The API inserts a dispatch job in the same transaction as the campaign's status change
    (`InsertTx` on GORM's `*sql.Tx`), so there is no dual write and no outbox.
  - A scheduled campaign is a River job with `ScheduledAt`; nothing polls for due campaigns.
    River's scheduler promotes due jobs every 5 s, so a campaign starts within about 5 s of
    its time.
- **River's own migrator migrates its tables** (`rivermigrate`), right after goose, in
  `database.Migrate`; seed and api call it.
  - **Compared with copying River's SQL into goose files:** there is no SQL to copy and keep in
    step, and River's migrations are tested and versioned. The cost is one documented exception
    to "only goose", and River's own `river_migration` table.
  - **The worker doesn't migrate.** It checks River's schema at start (`CheckQueueSchema`) and
    refuses to run on an outdated one.
  - **Upgrading River:**
    1. bump `river` and `riverdatabasesql` together;
    2. read River's changelog for migration notes;
    3. run `make test-integration`, whose throwaway databases run the migrations;
    4. deploy: seed and api migrate before the worker restarts and checks.
- **One worker process.** `cmd/worker`, its own Compose service, works the jobs; the API only
  inserts them. There are three queues: `email` and `sms` with 20 workers each, and `default`
  (dispatches and receipts) with 10.
- **Rate limits live in the worker.**
  - There is one token bucket per channel (`golang.org/x/time/rate`) at 100 per second with a
    burst of 1. Calls are at least 10 ms apart, so no second holds more than 100.
  - The worker waits on it immediately before each provider call, retries included.
  - Concurrency (calls in flight) and rate (calls started per second) are separate settings.
- **Exactly one worker may run.** Each process has its own limiters, so a second replica would
  double every limit. Multiple active replicas are not supported. They wouldn't add
  throughput either, since the rate limit is the bottleneck.
- **The worker owns the retry budget.**
  - **River's behaviour, read in its source:**
    - a job whose last attempt errs or panics is discarded, after its error handler and
      outside any transaction;
    - a job rescued after a crash with no attempts left is discarded without any hook.

    So no hook can record a failure together with the discard.
  - **The budget.** Each message gets 5 provider attempts (`maxSendAttempts`), while River
    keeps its default of 25.
  - **Attempt 5.** A transient failure on attempt 5 fails the message in its completion
    transaction and ends the job normally.
  - **Later runs.** Any later run, which only follows a crash or panic during attempt 5,
    records that failure without calling the provider.
  - **The guarantee.** A send job therefore ends only after its message has an outcome. River
    could discard one only after 20 further failed attempts to record it, spread over weeks of
    backoff.
  - **Rescue.** A crashed worker's jobs are rescued after 10 minutes, longer than any job's
    timeout; a graceful stop returns interrupted jobs at once.

**Consequences.**

- **No new infrastructure.** The queue, the schedule and the delivery records live in
  PostgreSQL, and jobs commit with the data they act on.
- **Sending beyond one process** would need a shared limiter, such as a token bucket in
  PostgreSQL. The providers cap throughput anyway.
- **Latency.** Polling adds up to 200 ms when the worker is idle, and River's scheduler up to
  5 s for scheduled jobs and receipts.

## 25. Delivery: one message per recipient and channel, short transactions, idempotent providers

**Decision.**

- **Preparation in one transaction.**
  - **Steps.** A dispatch job:
    1. locks the campaign, and starts it if it is scheduled (its `AsOf` is `started_at`);
    2. reads the audience with `segmentation.Recipients` on the same transaction;
    3. inserts one `pending` delivery per recipient and channel;
    4. inserts each delivery's send job into its channel's queue.

    All of it commits together. No send runs before every delivery exists, so "nothing
    pending" can't come early, and a failed preparation leaves nothing behind.
  - **Measured.** On the seeded data, every customer on both channels (3,552 recipients,
    4,546 deliveries and jobs) takes about 280 ms (265–291 ms over five runs; 404 ms on the
    running stack). That is about 60 µs per message.
  - **Scaling limit.** The transaction grows with the audience and holds the campaign's row
    lock throughout. A million messages would take about a minute in one transaction, so
    audiences in the hundreds of thousands need batched preparation with a checkpoint (README).
- **Short completion transactions.**
  - **When.** A message's outcome is recorded after the provider call, never during it.
  - **How.** The transaction locks the campaign row first and records the outcome only if
    the message is still pending. If nothing is pending any more, it marks the campaign
    `sent`.
  - **Why it is safe.** The completions of one campaign run one after another, so the last
    one always sees that nothing is pending. Without the lock, a test left a campaign stuck in
    `sending` in its first round. Every transaction locks the campaign before its deliveries,
    so none can deadlock.
- **Duplicate prevention, one layer per failure:**
  - the campaign's conditional transition (`WHERE status = 'draft'`);
  - the dispatch job inserted in that same transaction;
  - River's unique jobs: one dispatch per campaign, one send per delivery, one receipt per
    provider message;
  - the `UNIQUE (campaign_id, customer_id, channel)` key, with `ON CONFLICT DO NOTHING`;
  - conditional status updates (`WHERE status = 'pending'`, or `'accepted'` for receipts);
  - the delivery's ID as the provider's idempotency key.

  CHECK constraints tie each campaign status to its times, and each message status to its
  provider ID or reason.
- **Statuses.**
  - **Messages:**
    - `pending` → `accepted` (the provider took it) → `delivered`, or `failed` (`undelivered`)
      from the receipt;
    - `pending` → `failed` (`rejected`, `too_long`, `provider_unavailable`);
    - `pending` → `skipped`: the permission was revoked or the contact detail is gone,
      checked again just before sending.
  - **A campaign is `sent`** when every provider send attempt has reached a terminal business
    outcome: accepted, failed or skipped. It doesn't wait for delivery; receipts keep updating
    messages afterwards.
  - **A campaign is `failed`** when its segment no longer validates as its dispatch starts;
    its job is then cancelled.
- **Rendering.** Placeholders are filled from the customer's current details. A rendered SMS
  over 160 characters fails as `too_long` (decision 21).
- **Mock providers, in process.**
  - **The interface.** They sit behind a `provider` interface (`Send` with an idempotency key)
    that knows nothing of River or receipts.
  - **Durable answers.** The mock keeps the first answer to each key in
    `mock_provider_messages`, standing in for a provider's database. A repeated call gets the
    same answer, even after a restart.
  - **Receipts.** On acceptance it schedules its receipt as a River job, in the same
    transaction. The job stands in for a webhook.
  - **Determinism.** Its behaviour is a pure function of the key (FNV-1a modulo 100):
    - 1 % rejected;
    - 1 % accepted but answering too late on the first attempt;
    - 2 % busy once;
    - 2 % undelivered;
    - the rest delivered.

    Latencies and receipt delays are fixed per channel. Tests use scripted fakes.

**Limits of exactly-once.**

- **At least once.** Delivery is at least once, with idempotency. A retry after a crash
  between the provider's acceptance and our commit, or after a timeout, repeats the call.
- **What prevents a second message** is only the provider honouring the idempotency key, for a
  limited time (typically about a day). A provider without idempotency keys would send twice
  in exactly that gap.
- **Carriers and receipts.** Carriers can duplicate SMS, and receipts can be late, missing or
  wrong, so `delivered` is best effort.
- **Observed.** The end-to-end run, with the worker restarted mid-send, matched every provider
  acceptance with exactly one message.

## 26. Dispatch and progress API

**Decision.**

- **`POST /api/campaigns/{id}/dispatch`.**
  - **Body.** Optional: `{"scheduled_at": "…"}`, RFC 3339, in the future and at most a year
    ahead. No body means now.
  - **Behaviour.** It checks the segment's names again, then saves the transition and queues
    the job in one transaction.
  - **Answers:**
    - `202` with the campaign and `Location: /api/campaigns/{id}/delivery`;
    - `409 already_dispatched` for any campaign past the draft stage, which includes all but
      one of several concurrent requests;
    - `404`;
    - `422 invalid_campaign`.
- **No approval precondition yet.** A draft edited after review is sent as edited (README,
  further improvements: `If-Match`).
- **Dispatch status is on the campaign:** `status`, `scheduled_at`, `started_at`,
  `finished_at`.
- **`GET /api/campaigns/{id}/delivery`** is the campaign's progress snapshot:
  - the status and times;
  - the number of recipients;
  - message counts by status, overall and per channel;
  - `final`, true once the campaign is sent or failed and no message is pending or awaiting a
    receipt.
- **`GET /api/campaigns/{id}/delivery/events`** streams server-sent events, which answers the
  open question of real-time status.
  - **Updates.** A stream re-reads the snapshot every second, sends it when it changed, and
    pings every 15 s. It extends its write deadline for each write.
  - **Ending.** It ends when the client leaves or the server shuts down. Otherwise it ends
    with a `done` event exactly when the snapshot becomes final; a draft gets one snapshot and
    `done`. Clients close their EventSource on `done`, since it would otherwise reconnect.
  - **Proxying.** `X-Accel-Buffering: no` keeps nginx from buffering it.
  - **Scaling.** Each stream reads the database, so several API instances need no
    LISTEN/NOTIFY.
- **Routing.** The delivery capability serves its routes, mounted beside the campaign API in
  `cmd/api`.

**Consequences.**

- **No authentication**, outside the assessment's scope: anyone who reaches the API can
  dispatch.
- **Not built yet:** unscheduling and cancelling, cross-campaign statistics and receipt
  timeouts (README, further improvements).

## 27. A read-only segment API for the composer

**Context.** While a segment is edited, the composer must show whom it reaches. It must also
offer the countries, cities and categories a segment can name: names match exactly
(`İstanbul`), so free text would mostly fail as `unknown`. Decision 22 kept segmentation off
the REST API.

**Decision.** `internal/segmentation` serves two read-only routes at `/api/segments`
(`http.go`), amending decision 22:

- **`GET /api/segments/options`:** the countries (code and name), the cities (name and
  country), the categories and the customer languages, each in a stable order. Three small
  queries: the reference data is a few dozen rows.
- **`POST /api/segments/audience`,** with the body `{"segment": …}`, answers
  `{matched, reachable, by_channel: {email, sms}}` as of the request.
  - It runs `CountAudience`, the query dispatch uses, so estimate and sending agree. On the
    seeded data, everyone is estimated at 3,552 reachable customers, and dispatch prepares
    3,552 recipients.
  - It always counts both channels, so the composer can show the reach of whichever channels
    the campaign uses.
  - A segment with problems gets 422 `invalid_segment`, with the same field paths as a
    campaign draft, unknown names included.
- **Counts only.** No endpoint returns customers. A preview of sample recipients would show
  personal data, so it is deferred (README).
- **Delivery and the AI agent** still call the service directly.

**Consequences.**

- **Cost.** Each estimate is one count query (measured in decision 23). The composer waits
  400 ms after the last change and cancels stale requests.
- **Permissions** are read as they are when the estimate runs, and checked again when sending
  (decision 22).

## 28. Frontend: Vue Router, local state and plain CSS

**Context.** Phase 6 builds the marketer's interface:
- the campaign list;
- a three-step composer;
- live delivery;
- English and Turkish.

Decision 10 planned Vue Router, Pinia and a component library with the first screens.
Simplicity and few dependencies come first.

**Decision.**

- **One dependency added: `vue-router` 5,** in history mode; nginx already falls back to
  `index.html`. Version 5 installs the dependencies of its optional build plugin (33
  packages), but none of them reach the bundle.
- **No Pinia.** Each page and component holds its own state. Only the interface language and
  the notices are shared, as plain module values.
- **No component library.** The styling is plain CSS:
  - tokens on `:root` and a few shared classes in `src/styles.css`;
  - scoped styles in each component;
  - status colours with AA contrast, always shown with text.
- **No i18n library.**
  - `src/i18n.ts` holds a `locale` ref, `t(key, params)` and `Intl` formatting.
  - The texts are flat dictionaries. `src/locales/en.ts` is the reference; `tr.ts` must have
    exactly its keys, or type-checking fails.
  - English is the default, and the choice is kept in `localStorage`.
- **Every request goes through `src/api.ts`:** the JSON types, one function per endpoint, and
  every failure as an `ApiError` (status, code, problems).
- **Rules are mirrored, but the API is authoritative.**
  - `src/campaign.ts` repeats the draft rules of `internal/campaign` for instant feedback.
  - Its problems use the same field paths and codes, so client and API problems render the
    same way.
  - Only the API knows whether a name exists.
- **Editors emit new values.** The composer owns the draft; the segment group, condition and
  message editors emit a replacement instead of changing what they were given.
- **The wizard never loads a draft.** `CampaignWizard` takes an `initial` draft, so Phase 7 can
  open an AI proposal, and its problems, in it unchanged.
- **Dispatch is save, then dispatch:** two requests. A draft that saved stays saved if the
  dispatch fails.
- **Live progress** uses one `EventSource` per details page.
  - The browser retries a connection that dropped.
  - The page reopens one the browser gave up on, such as when nginx answers 502 while the API
    restarts. It waits 1 s, doubling up to 15 s.
  - `done` closes it.

**Consequences.**

- **The rules exist twice.** If the client's rules drift from the API's, the save still
  catches it, and the API's problem is shown at the same field.
- **Total spent** is entered in lira and stored in kuruş.
- **Drafts still save only when complete** (decision 19), so Save draft points at what's
  missing.

## 29. AI assistant: Claude tools validated in Go, dispatch only on approval

**Context.** Marketers should create, change, analyse and dispatch campaigns by chatting, in
any language. The model's output is untrusted. It must reuse the campaign rules and never send
on its own, and customers' data must not reach it.

**Decision.** `internal/assistant`:

- **The model.**
  - The standard Anthropic Messages API, with native tool calls.
  - `claude-haiku-5-5` by default, cost-efficient and fast; `ANTHROPIC_MODEL` changes it.
  - No thinking configuration, prompt caching or beta features.
  - `LLM_PROVIDER=mock` (the default) plays a fixed script offline; `claude` without a key
    falls back to it, with a warning. Tests use scripted models and a fake of the API, and make
    no paid calls.
- **Six tools, an explicit allowlist:** `create_campaign`, `update_campaign`, `get_campaign`,
  `list_campaigns` (to find a campaign by name), `estimate_audience`, `dispatch_campaign`.
  - The JSON schemas describe them to the model.
  - Go decodes each call into a typed struct that rejects unknown fields, then runs it through
    the campaign and segmentation services. Their problems go back to the model to repair.
  - Strict tool schemas aren't used: segments nest, which they can't express.
- **Privacy.** Results carry campaign settings, templates, statuses and aggregate counts only.
- **Dispatch.** `dispatch_campaign` only records a request, bound to the campaign's version
  (`updated_at`). `POST /api/assistant/approvals/{id}` closes it once, then calls
  `campaign.Service.DispatchApproved`.
  - That method's transaction updates `WHERE status = 'draft' AND updated_at = ?`.
  - A draft edited since fails atomically with `ErrChanged`.
  - The plain dispatch now uses the same condition, so it too sends only the draft it checked.
- **Conversations live in memory:** 2 h after their last turn ended, at most 500, with limits
  per turn and per conversation.
  - A conversation answering a message is busy: other messages to it get `conversation_busy`,
    and neither expiry nor the 500 limit evicts it. When all 500 are busy, a new conversation
    gets `assistant_busy`.
  - A failed turn is dropped, unless a tool already changed something (a draft, or a dispatch
    request). Then it is kept and answered with its cards and the notice `interrupted`, so a
    retry can't repeat the change unseen.
  - The model reply that reaches a turn's limit runs no tools, since the model would not see
    their results; the turn ends with `step_limit`.

**Consequences.**

- Conversations are lost on restart, and one API instance is assumed.
- Natural-language behaviour is checked by hand with a key.
- Answers the open question about running without a key.

## Open questions for later phases

- **LLM without an API key:** answered by decision 29: the mock is the default, and Claude
  without a key falls back to it, so the single command keeps working.
- **Segmentation rules:** answered by decisions 20 and 22: a whitelisted DSL validated on
  save, compiled to parameterised queries.
- **Real-time status:** answered by decision 26: server-sent events, each stream reading the
  database, so several API instances need no LISTEN/NOTIFY.
- **Seed data:** answered by decision 18, a one-shot, idempotent Compose service that runs
  before the API.
- **Mock provider:** answered by decision 25: in-process adapters in the worker, with
  durable idempotency in their own table.
- **What "haven't purchased" means** in a segment: answered by decision 20. It means "never" or
  "not within N days", while "not since the basket add" isn't expressible in DSL version 1.
