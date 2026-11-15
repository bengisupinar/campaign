# Campaign Composer

An AI-powered marketing campaign platform that turns natural-language requests into targeted SMS and email campaigns.

Marketers can define audiences, generate campaign content with AI, review and approve campaigns, and track asynchronous delivery in real time.

The application uses **5,000 synthetic customers** and mock messaging providers, allowing the complete workflow to be demonstrated without contacting real users.

## Tech Stack

| Layer | Technologies |
|---|---|
| Backend | Go, GORM, PostgreSQL |
| Frontend | Vue 3, TypeScript, Vite |
| AI | Anthropic Claude, native tool calling |
| Background Jobs | River (PostgreSQL-backed queue) |
| Infrastructure | Docker Compose, nginx |
| Testing | Go unit/integration tests, race detector, frontend unit tests |

## Key Features

**AI Campaign Assistant**
- Create, update, and estimate campaigns through natural-language conversations.
- Multilingual chat with Claude or an offline mock.
- Native tool calling with strict backend validation.
- Human-in-the-loop approval before AI-requested dispatch.

**Campaign Management**
- Create and manage SMS and email campaigns.
- Build audience segments using nested AND/OR conditions.
- Filter customers by location, language, shopping behavior, and spending.
- Preview messages with personalized placeholders.
- Estimate audience size and channel reachability.

**Asynchronous Delivery**
- Immediate or scheduled campaign dispatch.
- PostgreSQL-backed job processing with River.
- Mock SMS and email providers.
- Per-channel rate limiting (100 messages/second).
- Idempotent delivery attempts and retries.
- Real-time delivery progress through Server-Sent Events (SSE).

**Web Application**
- Three-step campaign composer.
- Campaign management and delivery monitoring.
- English and Turkish interface.

## Architecture

The application follows a modular Go architecture, separating business capabilities into independent packages.

```text
Vue 3 Frontend
      |
      v
Go REST API
      |
      +-- AI Assistant --------> Anthropic Claude
      |       |
      |       +-- Tool Validation
      |       +-- Human Approval
      |
      +-- Campaign Management
      |
      +-- Segmentation Engine
      |
      +-- PostgreSQL
              |
              +-- River Job Queue
                       |
                       v
                  Go Worker
                       |
                       v
                Mock SMS / Email
```

Campaign creation and audience estimation are handled synchronously. Dispatch creates background jobs processed asynchronously by the worker.

The frontend receives delivery progress through SSE.

For the detailed architecture diagram, see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).

## Quick Start

**Requirement:** Docker with Compose v2.

Start the application:

```bash
docker compose up --build
```

| Service | URL |
|---|---|
| Web Application | http://localhost:3000 |
| API | http://localhost:8080 |
| Health Check | http://localhost:8080/healthz |
| Readiness Check | http://localhost:8080/readyz |

No `.env` file is required for the default development setup.

On first startup, the application automatically initializes the database and seeds:

- 5,000 synthetic customers across Türkiye, Germany, and the UK.
- Customer permissions, shopping baskets, and orders.
- A catalogue of 168 products.

Data persists between restarts.

To reset the database:

```bash
docker compose down -v
docker compose up --build
```

## Using the Application

### Campaign Composer

Campaigns are created in three steps:

1. **Audience:** Define segmentation conditions and preview audience estimates.
2. **Message:** Write SMS/email content and preview personalized messages.
3. **Review:** Verify campaign details, save the draft, and send immediately or schedule delivery.

Campaign details include delivery status and real-time progress.

### AI Assistant

The assistant supports natural-language campaign creation and management.

Example:

> Create an SMS campaign for customers in İstanbul who added products to their baskets but haven't purchased anything in the last 30 days. Offer a 30% discount and keep the message friendly and short.

The assistant can create campaign drafts, update existing campaigns, estimate audiences, and request dispatch.

**The LLM cannot dispatch campaigns directly.** AI-requested dispatch requires explicit approval through the application's approval interface.

### AI Configuration

By default, the application uses an offline mock that demonstrates a fixed campaign workflow without an API key.

To use Claude, create a `.env` file:

```env
LLM_PROVIDER=claude
ANTHROPIC_API_KEY=your-api-key
ANTHROPIC_MODEL=claude-haiku-5-5
```

Restart the API:

```bash
docker compose up -d --force-recreate api
```

The model uses a fixed set of backend tools. Tool arguments are validated in Go, and business rules are enforced by the existing domain services.

**AI Guardrails**
- Fixed tool allowlist.
- Strict tool argument validation.
- Backend-enforced campaign validation.
- Human approval for AI-requested dispatch.
- Campaign version verification during approval.
- No individual customer data or contact details exposed to the LLM.
- Conversation length, tool call, and execution time limits.

## Development

**Requirements:** Go 1.27, Node.js ≥ 24.12, GNU Make, Docker.

```bash
cp .env.example .env

make db            # Start PostgreSQL
make seed          # Seed demo data
make run           # Start Go API
make worker        # Start delivery worker

make web-install
make web-dev       # Start Vue development server
```

The frontend development server runs at `http://localhost:5173`.

### Testing

```bash
make test              # Go tests with race detector
make test-integration  # PostgreSQL integration tests
make web-test          # Frontend unit tests
make check             # Go and frontend checks
```

Integration tests require PostgreSQL and a configured test database.

The project includes unit tests, database integration tests, and concurrency checks.

## API Overview

| Method | Endpoint | Description |
|---|---|---|
| POST | `/api/campaigns` | Create campaign |
| GET | `/api/campaigns` | List campaigns |
| GET | `/api/campaigns/{id}` | Get campaign |
| PUT | `/api/campaigns/{id}` | Update draft |
| DELETE | `/api/campaigns/{id}` | Delete draft |
| POST | `/api/campaigns/{id}/dispatch` | Dispatch campaign |
| GET | `/api/campaigns/{id}/delivery` | Delivery progress |
| GET | `/api/campaigns/{id}/delivery/events` | Live delivery events (SSE) |
| GET | `/api/segments/options` | Available segmentation values |
| POST | `/api/segments/audience` | Estimate audience |
| POST | `/api/assistant/messages` | AI assistant chat |
| POST | `/api/assistant/approvals/{id}` | Approve/cancel AI dispatch |

The regular campaign dispatch endpoint is separate from the AI assistant's approval workflow.

## Project Structure

```text
cmd/
  api/              API entry point
  worker/           Background delivery worker
  seed/             Demo data initialization

internal/
  assistant/        AI integration, tools, approvals
  campaign/         Campaign lifecycle and management
  segmentation/     Audience filtering and estimation
  delivery/         Queue processing, providers, SSE
  customer/         Customer profiles and permissions
  commerce/         Products, baskets, orders
  seed/             Synthetic data generation
  platform/         Shared infrastructure

frontend/           Vue 3 application
migrations/         PostgreSQL migrations
docs/               Architecture documentation
ai-sessions/        Claude Code development sessions
```

## Design Decisions

Key architectural decisions include:

- **PostgreSQL + River:** Reuses the existing database for durable background jobs without introducing a separate message broker.
- **Modular Backend:** Business capabilities are separated into focused Go packages.
- **Backend-Enforced Guardrails:** LLM-generated tool calls cannot bypass application validation.
- **Human-in-the-Loop:** AI-requested dispatch requires explicit user approval.
- **Idempotency:** Delivery retries use idempotency keys to reduce duplicate sends.
- **SSE:** Provides real-time delivery updates without polling.

See [DECISIONS.md](DECISIONS.md) for detailed reasoning and trade-offs.

## Future Improvements

The following enhancements are intentionally outside the current scope.

| Area | Potential Improvements |
|---|---|
| AI | Usage and cost monitoring, stronger guardrails, AI-generated email images, persistent conversations |
| Campaigns | Similarity detection, multilingual content, recipient local-time scheduling, cancellation |
| Data | Multi-currency support, basket history, consent history, price history |
| Performance | Redis caching, segmentation optimization, multiple workers |
| Security | User authentication, authorization, role-based permissions |
| Observability | Prometheus metrics, delivery and queue monitoring |
| Validation | Enhanced input validation, improved error handling, SMS encoding-aware limits |
| UX | Campaign search and pagination, dark mode, automated browser tests |

## Documentation

- [Architecture](docs/ARCHITECTURE.md) — System architecture diagram.
- [Design Decisions](DECISIONS.md) — Technical decisions and trade-offs.
- [Phase Reviews](PHASE_REVIEWS.md) — Implementation phases and verification.
- [Claude Instructions](CLAUDE.md) — Development principles and workflow.
- [AI Sessions](ai-sessions/) — AI-assisted development history.
