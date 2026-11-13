# Architecture decisions

Each entry records the context, the decision and its consequences. Entries are appended as
the project evolves; a superseded decision is marked as such rather than deleted.
Phase 1 (project foundation) decisions were recorded on 2026-11-13.

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
default, so no `.env` is needed. Healthchecks order startup: postgres → api (`/readyz`) → web.
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

## Open questions for later phases

- **LLM without an API key:** select the mock provider when `ANTHROPIC_API_KEY` is empty, so the
  single command keeps working.
- **Segmentation rules:** validate LLM output against a whitelist schema, then compile it to
  parameterised queries.
- **Real-time status:** SSE versus WebSockets; fan-out across instances (LISTEN/NOTIFY) if the API scales out.
- **Seed data:** a one-shot, idempotent Compose service that runs after the API is healthy.
- **Mock provider:** a separate Compose service versus an in-process adapter.
