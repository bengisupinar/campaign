# Campaign Composer — developer shortcuts. Run `make` to list them.
#
# `docker compose up --build` alone starts the whole application; these targets serve the
# development loop. Written for GNU Make 3.81 (the macOS default).

.DEFAULT_GOAL := help
SHELL := /bin/sh

# Optional local overrides (see .env.example). Only the keys defined in .env are exported;
# values given on the command line (make run LOG_LEVEL=info) still win.
-include .env
export $(shell sed -n 's/^\([A-Za-z_][A-Za-z0-9_]*\)=.*/\1/p' .env 2>/dev/null)

GOLANGCI_LINT_VERSION := v2.15.0
GOLANGCI_LINT := bin/golangci-lint-$(GOLANGCI_LINT_VERSION)

# goose CLI at the library version pinned in go.mod; `go run` needs no global install.
# The build tags leave out database drivers this project does not use.
GOOSE = go run -tags='no_clickhouse no_libsql no_mssql no_mysql no_sqlite3 no_vertica no_ydb' \
	github.com/pressly/goose/v3/cmd/goose@$(shell go list -m -f '{{.Version}}' github.com/pressly/goose/v3)
GOOSE_ENV = GOOSE_DRIVER=postgres GOOSE_DBSTRING="$(DATABASE_URL)" GOOSE_MIGRATION_DIR=migrations

.PHONY: help up down logs db run seed test test-integration lint fmt \
	migrate-up migrate-down migrate-status migrate-new \
	web-install web-dev web-test web-lint web-build check dotenv node-check

help: ## List the available targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z_-]+:.*## / {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}' $(firstword $(MAKEFILE_LIST))

# --- Application --------------------------------------------------------------------

up: ## Build and start the full stack (postgres, api, web) and wait until it is healthy
	docker compose up --build --detach --wait
	@echo "web: http://localhost:$(or $(WEB_PORT),3000)   api: http://localhost:$(or $(API_PORT),8080)"

down: ## Stop the stack (keeps the database volume)
	docker compose down

logs: ## Follow the logs of every service
	docker compose logs --follow

db: ## Start only PostgreSQL, for running the API on the host
	docker compose up --detach --wait postgres

run: dotenv ## Run the API on the host (needs `make db` and a .env)
	go run ./cmd/api

seed: dotenv ## Fill an empty database with synthetic demo data (needs `make db` and a .env)
	go run ./cmd/seed

# --- Go -----------------------------------------------------------------------------

test: ## Run the Go tests with the race detector
	go test -race ./...

# Database tests create and drop throwaway databases on the server in .env's DATABASE_URL;
# silenced (@) so the connection string is never echoed.
test-integration: dotenv ## Run the Go tests including those that need PostgreSQL (needs `make db`)
	@TEST_DATABASE_URL="$(DATABASE_URL)" go test -race -count=1 ./...

lint: $(GOLANGCI_LINT) ## Lint the Go code
	$(GOLANGCI_LINT) run

fmt: $(GOLANGCI_LINT) ## Format the Go code (gofmt + goimports)
	$(GOLANGCI_LINT) fmt

# golangci-lint must be built with a Go at least as new as go.mod's, so it is built with
# the module's toolchain into ./bin (git-ignored) rather than relying on a global install.
$(GOLANGCI_LINT):
	GOBIN="$(CURDIR)/bin" GOTOOLCHAIN="$$(go env GOVERSION)" \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	mv bin/golangci-lint $@

# --- Migrations (the API applies pending migrations on startup) ----------------------

# Recipes are silenced (@) so the connection string is never echoed to the terminal.
migrate-up: dotenv ## Apply pending migrations
	@$(GOOSE_ENV) $(GOOSE) up

migrate-down: dotenv ## Roll back the most recent migration
	@$(GOOSE_ENV) $(GOOSE) down

migrate-status: dotenv ## Show applied and pending migrations
	@$(GOOSE_ENV) $(GOOSE) status

migrate-new: ## Create the next migration: make migrate-new name=create_campaigns
	@test -n "$(name)" || { echo "usage: make migrate-new name=<snake_case_description>"; exit 1; }
	$(GOOSE) -dir migrations -s create $(name) sql

# --- Frontend -----------------------------------------------------------------------

web-install: node-check ## Install the frontend dependencies from the lockfile
	cd frontend && npm ci

web-dev: node-check ## Start the Vite dev server on :5173 (proxies /api and /readyz to :8080)
	cd frontend && npm run dev

web-test: node-check ## Run the frontend unit tests
	cd frontend && npm run test:unit -- --run

web-lint: node-check ## Lint, format-check and type-check the frontend (no auto-fix)
	cd frontend && npx oxlint . && npx eslint . && npx prettier --check src/ && npm run type-check

web-build: node-check ## Build the frontend for production
	cd frontend && npm run build

# --- All checks ---------------------------------------------------------------------

check: test lint web-lint web-test web-build ## Run every check (Go + frontend)

# --- Guards -------------------------------------------------------------------------

dotenv:
	@test -f .env || { echo "No .env file. Create one with: cp .env.example .env"; exit 1; }

# Vite needs Node ^22.18 or >=24.12 (frontend/package.json); .nvmrc pins 26.
node-check:
	@node -e 'const [a,b]=process.versions.node.split(".").map(Number);process.exit(a>24||(a===24&&b>=12)||(a===22&&b>=18)?0:1)' \
		|| { echo "Node $$(node -v) is too old for the frontend. Run: nvm use"; exit 1; }
