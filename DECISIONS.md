# Architecture decisions

Each entry records the context, the decision and its consequences. Entries are appended as
the project evolves; a superseded decision is marked as such rather than deleted.
Phase 1 (project foundation, entries 1–12) and Phase 2A (customer and commerce, 13–17)
decisions were recorded on 2026-11-13; Phase 2B (seeding, 18) and Phase 3 (campaigns, 19–21) on
2026-11-14.

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
  `min_try`/`max_try`, `match`/`conditions`), and every condition accepts `negate`.
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

## Open questions for later phases

- **LLM without an API key:** select the mock provider when `ANTHROPIC_API_KEY` is empty, so the
  single command keeps working.
- **Segmentation rules:** partly answered by decision 20, a whitelisted DSL validated on save.
  Compiling it to parameterised queries is the segmentation phase.
- **Real-time status:** SSE versus WebSockets; fan-out across instances (LISTEN/NOTIFY) if the API scales out.
- **Seed data:** answered by decision 18, a one-shot, idempotent Compose service that runs
  before the API.
- **Mock provider:** a separate Compose service versus an in-process adapter.
- **What "haven't purchased" means** in a segment: answered by decision 20. It means "never" or
  "not within N days", while "not since the basket add" isn't expressible in DSL version 1.
