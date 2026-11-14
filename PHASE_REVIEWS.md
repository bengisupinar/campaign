# Phase reviews

What each development phase built, newest first: its components, how they were tested and
verified, how the result differs from the plan, and what deserves a closer look. Entries are
snapshots taken when a phase is finished; later phases add entries rather than rewriting old
ones. The reasoning behind each choice is in [DECISIONS.md](DECISIONS.md), setup and commands
are in [README.md](README.md).

## Phase 4: Segmentation engine (2026-11-14)

**Scope.**

- **Compiler.** Segment DSL version 1 compiles into parameterised PostgreSQL: every condition
  type, nested all/any groups and negation.
- **`segmentation.Service`.**
  - `CountAudience`: customers matched, reachable, and reachable per channel.
  - `Recipients`: keyset batches of up to 1,000.
  - `CheckReferences`.
- **Eligibility.** A contact point and a granted permission, checked per channel.
- **References.** Countries, cities and categories must exist. They are checked by every engine
  call and when drafts are saved.
- **Query plans**, measured.

Decisions taken while planning:

- references are checked when drafts are created or updated (A);
- email validity stays in the domain, with no new CHECK (B);
- no index without a measurement (C);
- `AsOf` bounds windows and is not a snapshot;
- delivery re-checks permissions just before sending (Phase 5);
- an empty segment matches every customer, subject to eligibility;
- `total_spent` sums completed orders' lines.

Not built yet: segment endpoints, sending, scheduling, queues, providers, AI and the frontend.

**Result.**

- `make check` and `make test-integration` pass.
- **Seeded data.** The engine reaches the same 56 customers by SMS for the Phase 2A example as
  the seed's hand-written query, which the seed tests tie to the generator's own analysis.
- **Speed.** Every measured call takes under 60 ms on 5,000 customers, and no index was added.
- **Through nginx,** a draft naming unknown cities or categories gets 422 listing each of them,
  together with the draft's other problems.

### How the pieces fit

```text
campaign.Service ── create/update a draft ──► referenceChecker ─┐
AI tools, delivery (later) ─────────────────────────────────────┤
                                                                ▼
                          segmentation.Service: CountAudience · Recipients · CheckReferences
                            prepare: Segment.Problems, channels and AsOf, then the names
                                     Segment.References lists, looked up in one query
                                                                │
                                                                ▼
                          compileSegment(segment, AsOf) ─► WHERE over customers c + bind arguments
                          reachable                     ─► email_ok and sms_ok, the campaign's
                                                           channels as parameters
                                                                │
                                   ┌────────────────────────────┴───────────────────────┐
                                   ▼                                                    ▼
                  CountAudience: one pass, count(*) FILTER      Recipients: id > after ORDER BY id LIMIT n
```

### Components

| Component | Location | What it does | Tests |
|---|---|---|---|
| Compiler | `internal/segmentation/compile.go` | Segment and `AsOf` to SQL and arguments, one fixed shape per condition type | 15 compile cases; placeholders match arguments; unknown types fail |
| References | `internal/segmentation/references.go` | Looks up every name `Segment.References` lists in one query, and reports the unknown ones | Integration: unknown, wrong case, injection-shaped name, malformed segment |
| Service | `internal/segmentation/segmentation.go` | Query validation; `CountAudience` and `Recipients` over one eligibility fragment for both channels | 5 cases without a database; integration: 22 segments, 5 counts, batches, seeded cross-check |
| Benchmark | `internal/segmentation/segmentation_test.go` | `BenchmarkAudience` on the seeded data | — |
| Campaign changes | `internal/campaign` (`service.go`, `segment.go`, `problems.go`) | `referenceChecker`; unknown references join the 422; `Segment.Problems` and `Segment.References`; `total_spent` takes `min`/`max` in kuruş; code `unknown` | 6 API cases plus a 409; `References` paths; every segment case also checks `Problems` |
| Wiring | `cmd/api/main.go` | Passes the segmentation service to the campaign service | Running stack |

Coverage of `internal/segmentation` is 55.0 % without a database and 93.0 % with PostgreSQL;
`internal/campaign` is at 78.9 % and 93.2 %. The audience queries are only covered with
PostgreSQL.

#### Compiler and service (`internal/segmentation`)

- **Safety.** Each condition type has one fixed SQL fragment, and segment values only travel
  as bind arguments. A test checks that every compiled segment has exactly as many
  placeholders as arguments.
- **Semantics** ([DECISIONS.md](DECISIONS.md) §22):
  - same-row filters for basket lines and orders;
  - completed orders only;
  - spend bounds in kuruş, compared with the sum as stored;
  - `COALESCE` for customers without orders;
  - windows from `AsOf − N × 24h` to `AsOf`.
- **One validation path.** Both operations go through `prepare`. It reports structural and
  query problems together; references are only looked up when there are none.
- **Recipients.**
  - Contact details come only for the channels a customer can receive, as domain types that
    mask themselves in logs. `Recipient` logs only its ID and channels.
  - Phones and emails are parsed again on the way out; a value that fails parsing is an error.

#### Campaign changes (`internal/campaign`)

- **The DSL describes itself.** `Segment.Problems` lists a segment's structural problems and
  `Segment.References` the names it uses with their field paths, so segmentation repeats no
  DSL knowledge.
- **Reference check.** `Service.Create` and `Update` add the unknown references to the
  draft's other problems; the checker reports nothing for a segment with problems of its own.
  Errors other than validation errors pass through, so a scheduled campaign still gets 409.
- **One pair of bounds.** `total_spent` takes `min` and `max` in kuruş, like `order_count`.
  `min_try` and `max_try` are gone, and both types share Phase 3's bounds rule: neither
  negative, and `min` not above `max`.

### Verification

| # | Check | Result |
|---|---|---|
| 1 | `make check` | Pass: Go tests with `-race`, lint with 0 issues, frontend tests (6) and build |
| 2 | `make test-integration`, hand-made fixture | Pass. Six customers whose audiences are known, covering:<br>• every condition type, and windows ending at `AsOf` (a basket line dated after it);<br>• same-line and same-order filters;<br>• cancelled and refunded orders;<br>• bound edges, 999.90 against 1,000 TRY;<br>• nested and negated groups;<br>• eligibility: no phone with a granted SMS row, revoked, declined, no permissions;<br>• batches of two with contact details per channel;<br>• unknown references, including `Ankara'); DROP TABLE customers; --`, with the table intact afterwards, and none reported for a malformed segment |
| 3 | `make test-integration`, seeded dataset | Pass. The example reaches 56 customers by SMS, as the hand-written query does. For every customer, the example and a nested segment, paging through `Recipients` finds exactly the customers and channels `CountAudience` counts |
| 4 | Mutation check, repeated after the refactor | Eight deliberate bugs each fail their intended test: counting every order status, dropping `COALESCE`, dropping the phone check, open-ended windows, categories from another basket line, categories from another order, ignoring the campaign's channels, checking references in a malformed segment |
| 5 | EXPLAIN ANALYZE and `BenchmarkAudience` | Existing indexes only. Count / first batch, medians of five runs: everyone 2.6 / 4.3 ms, the example 1.6 / 4.0 ms, nested 53 / 25 ms ([DECISIONS.md](DECISIONS.md) §23) |
| 6 | Running stack, through nginx | Pass. Unknown city, category and country: 422 listing each one, also together with a too-long SMS. A padded city name: 422 `invalid` only. Known names: 201. `total_spent` with `min`/`max` in kuruş: 201; with `min_try`: 400 `malformed_json`; with `min` above `max`: 422. Test drafts were deleted afterwards, and customers still number 5,000 |

### Deviations from the plan

- **The count materialises the audience.** The plans showed PostgreSQL inlining the audience
  and testing every permission twice. A `MATERIALIZED` CTE made counting every customer 7 times
  faster (17 → 2.4 ms); it is the only query change that came from measuring.
- **Unknown references join the draft's other problems.** The first version checked
  references only once the rest of the draft was valid, so problems came in two rounds. A
  self-review moved them into the same 422, as the plan said, following decision 21's "every
  problem at once".
- **Errors from the reference check are returned as they are.** The segmentation service
  already says what failed; the first version wrapped the same context twice.
- **`total_spent` bounds in kuruş, a review decision.**
  - The plan kept `min_try`/`max_try` in whole lira, converted to kuruş by the compiler and
    capped so the conversion couldn't overflow.
  - After review, `total_spent` reuses `min`/`max` in kuruş, and the compiler compares them
    as stored. The conversion and the caps are gone.
  - DSL version 1 changed in place, since no stored or released campaign used the old fields.
- **Readability refactor, a review request.** Behaviour is unchanged except where noted.
  - **Eligibility.** A per-channel loop that built the count and recipient queries, whose
    results were then mapped back by hand, became one fixed SQL fragment for both channels,
    with the campaign's channels as parameters.
  - **Compiler.** Every condition's SQL is in one switch. Optional parts compose by value
    instead of through a pointer-mutating builder, and basket lines and order lines share
    one category filter.
  - **References.** The DSL walk moved to `campaign` (`Segment.References`), and the lookup
    is one query instead of one per kind. The "nothing for a malformed segment" rule now
    lives only in `CheckReferences`.
  - **Campaign helpers.** Each condition type lists its parameters beside their checks,
    replacing `allowedParams`. `Segment.Problems` replaces `Validate`, and one generic
    `clonePtr` replaces the copying code.
  - **Size.** The seven production files went from 1,230 to 1,122 lines.
  - **Measured A/B.** Benchmarking showed no performance change: the variants were within
    run-to-run noise.
  - **SQL text changes.** A negated group now reads `NOT ((…))`, and categories are matched
    through a products subquery instead of a join.
- **Additions.**
  - **Benchmark.** `BenchmarkAudience` repeats the measurement.
  - **`ANALYZE`.** The seeded test database runs it, so plans don't change while a benchmark
    runs.

### Worth a closer look

- **`AsOf` against live data.** Windows end at `AsOf`, but everything else is read live:
  - rows dated after `AsOf` still count for conditions without a window;
  - batches see changes made between them.
- **Permissions.** Delivery must re-check each recipient just before sending.
- **Amounts in kuruş.** `"min": 100000` means 1,000 TRY. The AI phase's tool schema must say
  so, or a model writing lira would target spends 100 times smaller.
- **City names match in every country.** Names are unique within a country, not across
  countries, so pair `city` with `country` when a name is ambiguous.
- **Exact names.** "Istanbul" is unknown, and so is the same name under another Unicode
  normalisation. The `unknown` problems are meant to go back to the user or the model.
- **Aggregation over long windows.** `order_count` and `total_spent` make the nested segment
  the slowest at 53 ms, and they grow with the data ([DECISIONS.md](DECISIONS.md) §23).
- **Database dependency.** Saving a draft queries the database for references; the API tests
  use a fake checker.

## Phase 3: Campaign management (2026-11-14)

**Scope.**

- **Domain.** A draft has a name, a segment in DSL version 1, and content per channel. The
  lifecycle is minimal (draft, scheduled, sending), with its transitions defined; only drafts
  change.
- **Persistence.** The `campaigns` table (migration 4) and a GORM store whose writes only
  touch drafts.
- **REST API** under `/api/campaigns`: create, list, get, replace and delete drafts.

Decisions taken while planning:

- every save must be complete and valid;
- SMS length counts the template as written;
- placeholders are `first_name`, `last_name` and `city`;
- city and category names are checked against reference data in the segmentation phase;
- the lifecycle stays minimal.

Not built yet: sending, scheduling, queues, segment queries, AI, the frontend and the shared
error framework.

**Result.**

- `make check` and `make test-integration` pass.
- The API works end to end through nginx.
- The seeded data is untouched.

### How the pieces fit

```text
POST/PUT /api/campaigns ─► handler: decode JSON (strict, 64 KB), map errors to statuses
                              │
                              ▼
                           Service ─► Campaign: New / Update validate the Draft and guard the lifecycle
                              │                 (later, AI proposals build the same Draft)
                              ▼
                           Store (GORM) ─► campaigns: segment as jsonb, one nullable column per content field
```

### Components

| Component | Location | What it does | Tests |
|---|---|---|---|
| Domain | `internal/campaign` (`campaign.go`, `content.go`, `segment.go`, `problems.go`) | Draft, Campaign, lifecycle, content and segment validation | 64 cases |
| Service | `internal/campaign/service.go` | Create, Update, Get, List, Delete over a repository interface | Through the API tests |
| API | `internal/campaign/http.go` | REST handlers; JSON errors, including for unknown routes | 18 cases |
| Store | `internal/campaign/store.go` | jsonb segment, nullable content, writes that only touch drafts | 6 integration cases |
| Schema | `migrations/00004_campaigns.sql` | The `campaigns` table and its CHECK constraints | 8 constraint cases |
| JSON helpers | `internal/platform/httpserver/json.go` | `DecodeJSON` (strict, 64 KB) and `WriteJSON`, now also used by health | 10 cases |
| Reused checks | `internal/customer` | `Channel.Valid()` and `Language.Valid()` exported | Existing tests |

Coverage of `internal/campaign` is 77.3 % without a database and 92.2 % with PostgreSQL.

#### Domain (`internal/campaign`)

- **Lifecycle.**
  - `Status.CanBecome` defines four moves: draft → scheduled or sending, and scheduled →
    draft or sending.
  - `Update` and the service's `Delete` refuse anything but a draft.
- **`New` and `Update`.**
  - They trim the name and validate the whole draft, reporting every problem at once as a
    `*ValidationError`. Each problem has a field path and a stable code.
  - They copy the draft in and out, so callers can't change a campaign behind its back.
- **Content.**
  - An optional SMS or email block; the channels follow from the blocks.
  - Limits count Unicode characters: name 100, SMS 160, subject 80.
  - Placeholders are parsed strictly: unknown names, `{{` without `}}`, `}}` on its own, and
    `{{}}` are all rejected.
- **Segment DSL version 1.**
  - 8 condition types plus `group`; any condition can be negated.
  - Strict parameters per type.
  - Limits: depth 3, 20 conditions, 50 values per list, windows of 1–3,650 days.
  - An empty root targets every customer.

#### Service, store and API

- **Service.** `Service` is the single entry point that manual edits use now and AI proposals
  will use later. It sets times in UTC at PostgreSQL's microsecond precision.
- **Conditional writes.** `Store.Update` and `Store.Delete` add `AND status = 'draft'`. When no
  row matches, they look the campaign up to answer `ErrNotFound` or `ErrNotDraft`.
- **Errors.** Handlers map errors to responses: 422 `invalid_campaign` with the problems, 404
  `not_found`, 409 `not_draft`, 400 `malformed_json`, 413 `body_too_large`, 405
  `method_not_allowed`, and 500 `internal` (logged with the request ID).
- **Requests.** Unknown fields, such as a client trying to set `status`, are rejected.

### Verification

| # | Check | Result |
|---|---|---|
| 1 | `make check` | Pass: Go tests with `-race`, lint with 0 issues, frontend tests (6) and build |
| 2 | `make test-integration` | Pass. jsonb round trip, including nested groups and pointer parameters. Writes that only touch drafts, even from a stale copy. Newest-first listing. 8 new constraint cases. The migration round trip now includes version 4 |
| 3 | Running stack, through nginx | Pass. Create 201, get 200, replace 200 (both channels), invalid 422 with all four problems, `/send` 404 in JSON, `PATCH` 405 in JSON, 409 on a campaign scheduled in psql, delete 204, then 404. Customers still number 5,000 |

### Deviations from the plan

- **JSON for unknown routes.** The campaign API answers unknown routes and methods with JSON
  404 and 405. Testing with curl showed chi's plain-text defaults.
- **One validation error type.** Validation returns a `*ValidationError` holding a list of
  problems rather than an `errors.Join` of field errors, because a list is simpler for the
  handlers and the AI repair loop.
- **No request DTOs.** `Draft`, `Segment`, `Content` and `Problem` carry JSON tags and
  double as the API's request shapes.
- **Tests use `new(30)`,** which Go 1.26 allows for pointers to values.
- **Review fix: complete problem lists.** Three checks used to stop at their first problem:
  - range bounds;
  - lists with more than 50 values;
  - placeholder scanning after a stray `}}` or an empty `{{ }}`.

  They now report every problem, with malformed braces reported once per field.
- **Review fix: names.** `validName` now rejects whitespace-only names explicitly; it already
  did so implicitly. Tests cover spaces, tabs and non-breaking spaces.

### Worth a closer look

- **JSON tags on domain types.** `Draft` and `Segment` are the wire format for the API and,
  later, for AI. Changing a JSON tag is an API change, and so is a new DSL version.
- **Last write wins** when two people edit the same draft.
- **SMS length.** 160 characters is the GSM-7 limit; Turkish letters make real messages UCS-2,
  where each message holds 70 characters.
- **Unchecked names.** A draft can reference an unknown city or category, e.g. "Istanbul"
  without the dotted İ, until the segmentation phase checks names against reference data.
- **Local error bodies.** They are defined in the campaign handlers until the shared error
  framework moves them to the API boundary.
- **Content storage.** The content columns will be restructured for multilingual campaigns.

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
