# CLAUDE.md

AI-powered marketing campaign composer (technical assessment). A marketer describes a
campaign in natural language; an LLM proposes audience segmentation rules and SMS/email
content; people review, edit and approve it; approved campaigns are sent asynchronously
to synthetic users through mock providers, with delivery status tracked in real time.

- Backend: Go 1.27, module `github.com/bengisupinar/campaign` at the repo root; chi, GORM + PostgreSQL 18, goose migrations, River job queue (one worker process, `cmd/worker`).
- Frontend: Vue 3 + TypeScript + Vite in `frontend/`.
- `docker compose up --build` starts everything; no `.env` is required.
- Rationale for every decision: [DECISIONS.md](DECISIONS.md). Setup and commands: [README.md](README.md). What each phase built: [PHASE_REVIEWS.md](PHASE_REVIEWS.md).

## Engineering principles

- Choose the simplest reliable solution. No speculative abstractions, no empty or placeholder packages.
- Package by business capability: `internal/<capability>` (e.g. `campaign`, `audience`, `delivery`), one package holding its handlers, logic and persistence. Cross-cutting technical code lives in `internal/platform/`. Never add `handlers/`, `services/`, `repositories/` or `models/` packages, nor per-layer sub-packages.
- Apply DDD where invariants exist (e.g. the campaign lifecycle); keep those rules in domain types and methods. Plain CRUD stays plain.
- HTTP handlers stay thin: decode with `httpserver.DecodeJSON`, call the capability's `Service`, answer with `httpserver.WriteJSON`. Rules live in the domain, so other callers (e.g. AI proposals) share them.
- Interfaces are small, declared by their consumer, and added only for a second implementation or a test seam.
- Idiomatic Go: wrap errors with `%w` and context; `ctx` is the first parameter; no package-level mutable state; dependencies are wired explicitly in `cmd/`. Domain code takes the current time as a parameter instead of calling `time.Now()`.
- Persistence: GORM only inside a capability's persistence code. The schema changes only through goose SQL migrations in `migrations/`, never `AutoMigrate`; the one exception is River's own tables, which River's versioned migrator manages (DECISIONS.md §24). Don't embed `gorm.Model`; do partial updates with `Select` or maps (struct `Updates` skips zero values).
- LLM output is untrusted input: validate it against a whitelist of fields and operators, and never interpolate it into SQL or `Where` strings.
- Secrets come only from the environment. Never commit `.env`; defaults in `.env.example` and `compose.yaml` are development-only.
- Logs are structured (`slog`); neither logs nor error messages contain credentials or personal data (DSNs, emails, phone numbers).
- Tests are table-driven, use `httptest` for handlers and run with `-race`. Behaviour changes ship with tests.

## Workflow

1. Work in phases: plan → user approval → implement → `make check` → phase review → propose a commit.
2. Never commit without explicit user approval. Use Conventional Commits (`feat:`, `fix:`, `refactor:`, `test:`, `docs:`, `build:`, `chore:`).
3. Never push, create remotes, or change Git configuration or credentials.
4. Record significant decisions in DECISIONS.md (context, decision, consequences).
5. After every phase (any development that adds or changes components), add an entry at the top of PHASE_REVIEWS.md in the existing format: the components built or changed (what each does, where it lives, how it is tested), verification results, deviations from the plan, and what deserves a closer look. Add what the phase defers to README.md's further-improvements list.
6. Challenge requirements that conflict with these principles instead of silently complying.

## Commands

| Command | Purpose |
|---|---|
| `docker compose up --build` | Start the whole application, seeding demo data on first start: web on :3000, API on :8080 |
| `make db` then `make run` | PostgreSQL in Docker, API on the host (needs `cp .env.example .env`) |
| `make seed` | Fill an empty database with the synthetic demo data; a seeded database is left alone |
| `make worker` | Run the delivery worker on the host; exactly one may run (the rate limits live in its process) |
| `make web-dev` | Vite dev server on :5173, proxying `/api` and `/readyz` to :8080 |
| `make test` · `make lint` · `make fmt` | Go tests with `-race`, golangci-lint v2, formatting |
| `make test-integration` | Go tests plus the schema tests against PostgreSQL (needs `make db`); run it when migrations change |
| `make migrate-new name=…` · `make migrate-status` | Create or inspect migrations; the API applies them on startup |
| `make check` | Every Go and frontend check; run it before proposing a commit |

Frontend targets need Node ≥ 24.12 (`nvm use` reads `.nvmrc`).
