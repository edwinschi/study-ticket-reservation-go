# Ticket Reservation Go Lab

`ticket-reservation-go-lab` is a professional study project that implements a
concurrency-safe ticket reservation REST API in Go.

The project models a realistic backend problem: multiple users trying to reserve
limited inventory at the same time. It supports quantity-based tickets, seat-based
tickets, anonymous visitor sessions, basic authentication, reservation lifecycle
transitions, automatic expiration, consistency assertions, automated concurrency
tests, and local k6 stress tests.

This repository is intentionally designed as a technical portfolio case. The
important business invariants are protected by PostgreSQL, not by in-memory locks
or best-effort Redis counters.

## Stack

- Go 1.26
- chi router
- PostgreSQL 16
- pgx / pgxpool
- sqlc
- golang-migrate
- Redis with go-redis
- Docker Compose
- Native Go testing
- slog structured logging
- k6 stress testing

## Architecture

The code is organized around explicit layers:

```text
cmd/
  api/                  HTTP API entrypoint
  worker/               reservation expiration worker entrypoint

internal/
  config/               environment-based configuration
  db/                   PostgreSQL, Redis, transaction helpers, UUID helpers
  httpserver/           chi router, middleware, JSON responses, app errors
  modules/
    health/             health and readiness endpoints
    sessions/           anonymous visitor session flow
    auth/               basic user registration/login/logout
    events/             event, ticket type, seat, and inventory endpoints
    reservations/       quantity/seat reservation and lifecycle logic
    stressadmin/        local seed/reset/consistency assertion endpoints
  sqlc/                 generated type-safe database access code
  worker/               expiration loop implementation

db/
  migrations/           PostgreSQL migrations
  queries/              SQL files consumed by sqlc

stress/
  k6/                   local load test scripts

tests/
  integration/          API and database integration tests
  concurrency/          race-condition focused tests
```

The API layer parses HTTP requests and returns standardized JSON responses. The
service layer owns business rules. The repository layer owns SQL execution and
short PostgreSQL transactions. The generated `internal/sqlc` package keeps SQL
explicit while still giving compile-time Go types.

## Running locally

Start the full local environment:

```bash
docker compose up --build
```

Docker Compose starts:

- `postgres`
- `redis`
- `migrate`
- `api`
- `worker`
- `k6` as an on-demand stress-test service
- `sqlc` as an on-demand code generation service

The API listens on:

```text
http://localhost:8080
```

Health check:

```bash
curl http://localhost:8080/health
```

Expected response:

```json
{"status":"ok"}
```

Readiness check:

```bash
curl http://localhost:8080/ready
```

Expected response when PostgreSQL and Redis are reachable:

```json
{"postgres":"ok","redis":"ok","status":"ready"}
```

Stop containers:

```bash
docker compose down
```

Stop containers and remove local volumes:

```bash
docker compose down -v
```

## API testing with Bruno

The repository includes a versioned Bruno collection in:

```text
api/bruno/
```

Start the API with `docker compose up --build`, open Bruno, choose
**Open Collection**, and select the `api/bruno/` folder. The local Bruno
environment uses:

```text
baseURL=http://localhost:8080
```

Suggested manual flow:

1. Run `health/Healthcheck` and `health/Readiness`.
2. Run `sessions/Create Anonymous Session` so Bruno stores the `visitor_session` cookie.
3. Run `stress-admin/Seed Stress Fixture`.
4. Copy the returned `event_id`, `ticket_type_id`, and relevant `seat_ids` into the active Bruno environment.
5. Run reservation requests and then `stress-admin/Assert Consistency`.

See `api/bruno/README.md` for the full collection notes.

## Environment variables

The project includes `.env.example` with the local defaults.

| Variable | Purpose | Default |
| --- | --- | --- |
| `APP_ENV` | Application environment. Stress admin reset is intended for local use. | `development` |
| `LOG_LEVEL` | Structured log level. | `INFO` |
| `API_PORT` | Host port mapped to the API container. | `8080` |
| `COOKIE_SECURE` | Whether cookies are marked `Secure`. Local development uses `false`. | `false` |
| `VISITOR_SESSION_TTL` | Anonymous visitor session lifetime. | `720h` |
| `RESERVATION_TTL` | Default reservation hold duration. | `15m` |
| `EXPIRATION_WORKER_INTERVAL` | Worker loop interval. | `5s` |
| `EXPIRATION_WORKER_BATCH_SIZE` | Maximum expired reservations processed per worker batch. | `100` |
| `POSTGRES_DB` | PostgreSQL database name. | `ticket_reservation_go` |
| `POSTGRES_USER` | PostgreSQL username. | `ticket_reservation_go` |
| `POSTGRES_PASSWORD` | PostgreSQL password. | `ticket_reservation_go` |
| `STARTUP_TIMEOUT` | Startup dependency timeout. | `10s` |
| `READINESS_TIMEOUT` | `/ready` dependency ping timeout. | `2s` |
| `SHUTDOWN_TIMEOUT` | Graceful shutdown timeout. | `10s` |

Inside Docker Compose, `DATABASE_URL` and `REDIS_ADDR` are configured on the
internal Docker network.

## Running migrations

Migrations are applied automatically by the `migrate` service before the API and
worker start.

Run migrations manually:

```bash
make migrate-up
```

Rollback one migration:

```bash
make migrate-down
```

The migration files live in:

```text
db/migrations/
```

## Running sqlc

SQL queries live in:

```text
db/queries/
```

Generated Go code lives in:

```text
internal/sqlc/
```

Regenerate sqlc code:

```bash
make sqlc
```

The project uses explicit SQL with `pgx/v5`. There is no heavy ORM. This makes
the concurrency behavior visible and auditable.

## Running tests

Run all tests inside the API container:

```bash
docker compose exec api go test ./...
```

Or use Make:

```bash
make test
```

Run quality checks:

```bash
make fmt
make vet
make tidy
```

The test suite includes:

- health/readiness integration tests;
- migration/schema tests;
- anonymous session and auth tests;
- event/inventory/stress admin tests;
- quantity reservation tests;
- seat reservation tests;
- reservation lifecycle tests;
- strong concurrency tests with goroutines, `sync.WaitGroup`, and channels;
- expiration worker race tests.

## Running stress tests

k6 scripts live in:

```text
stress/k6/
```

Available scripts:

- `quantity.js`: anonymous users reserve quantity tickets and sometimes cancel or confirm.
- `seats.js`: anonymous users reserve random seats and accept expected conflicts.
- `mixed.js`: mixed quantity and seat traffic with lifecycle transitions.

Recommended local flow:

```bash
docker compose up --build

curl -X POST http://localhost:8080/v1/admin/stress/reset

curl -X POST http://localhost:8080/v1/admin/stress/seed

docker compose run --rm k6 run \
  -e BASE_URL=http://api:8080 \
  /scripts/mixed.js

curl http://localhost:8080/v1/admin/stress/assert-consistency
```

Load variations:

```bash
docker compose run --rm k6 run \
  -e BASE_URL=http://api:8080 \
  -e VUS=500 \
  -e DURATION=30s \
  /scripts/mixed.js

docker compose run --rm k6 run \
  -e BASE_URL=http://api:8080 \
  -e VUS=1000 \
  -e DURATION=1m \
  /scripts/mixed.js

docker compose run --rm k6 run \
  -e BASE_URL=http://api:8080 \
  -e VUS=5000 \
  -e DURATION=2m \
  /scripts/mixed.js
```

Focused scripts:

```bash
docker compose run --rm k6 run -e BASE_URL=http://api:8080 /scripts/quantity.js
docker compose run --rm k6 run -e BASE_URL=http://api:8080 /scripts/seats.js
```

Interpretation:

- `201 Created` means the reservation was created.
- `409 Conflict` is expected when stock is exhausted or a seat is already active.
- `409` is not a system failure in this project.
- `500`, excessive timeouts, API crashes, or failed consistency assertions are real failures.
- A healthy run ends with `server_errors = 0` and `assert-consistency` returning `ok: true`.

## Main endpoints

### Health

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Simple liveness check. |
| `GET` | `/ready` | Validates PostgreSQL and Redis connectivity. |

### Sessions and auth

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/sessions/anonymous` | Creates an anonymous visitor session and sets the `visitor_session` cookie. |
| `GET` | `/v1/me/session` | Returns the current visitor session. |
| `POST` | `/v1/auth/register` | Creates a user with a securely hashed password. |
| `POST` | `/v1/auth/login` | Validates credentials and links the current visitor session to the user. |
| `POST` | `/v1/auth/logout` | Clears the session cookie without deleting existing reservations. |

### Events and inventory

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/events` | Creates an event. |
| `GET` | `/v1/events/{event_id}` | Reads an event. |
| `POST` | `/v1/events/{event_id}/ticket-types` | Creates quantity inventory for an event. |
| `POST` | `/v1/events/{event_id}/seats` | Creates seats in batch. |
| `GET` | `/v1/events/{event_id}/inventory` | Reads quantity and seat inventory. |

### Reservations

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/reservations/quantity` | Reserves ticket inventory by quantity. |
| `POST` | `/v1/reservations/seats` | Reserves one or more seats. |
| `GET` | `/v1/reservations/{reservation_id}` | Reads a reservation visible to the current session/user. |
| `POST` | `/v1/reservations/{reservation_id}/cancel` | Cancels a reservation idempotently. |
| `POST` | `/v1/reservations/{reservation_id}/confirm` | Confirms a reservation idempotently. |

### Local stress administration

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/v1/admin/stress/seed` | Creates one event, one 1,000-unit ticket type, and 100 seats. |
| `POST` | `/v1/admin/stress/reset` | Removes local stress fixtures safely. |
| `GET` | `/v1/admin/stress/assert-consistency` | Validates database consistency invariants after tests or stress runs. |

## Data model summary

The first migration creates the following main tables:

- `users`: registered users.
- `visitor_sessions`: anonymous or logged-in visitor sessions.
- `events`: events that own ticket types and seats.
- `ticket_types`: quantity-based inventory counters.
- `seats`: physical seats scoped by event.
- `reservations`: reservation header with status, type, idempotency key, and expiration time.
- `reservation_items`: quantity reservation lines.
- `reservation_seats`: seat reservation lines.

Important database constraints:

- `ticket_types.total_quantity >= 0`
- `ticket_types.sold_quantity >= 0`
- `ticket_types.reserved_quantity >= 0`
- `ticket_types.sold_quantity + ticket_types.reserved_quantity <= ticket_types.total_quantity`
- `seats` has a unique constraint on `(event_id, section, row_name, seat_number)`
- `reservations` has a unique constraint on `(visitor_session_id, idempotency_key)`
- reservation statuses are constrained to `reserved`, `confirmed`, `cancelled`, and `expired`
- reservation types are constrained to `quantity` and `seats`
- `reservation_items.quantity > 0`
- `reservation_seats` has a partial unique index for active seats

## Concurrency strategy

The core rule is simple: PostgreSQL is the source of truth for inventory
correctness.

The API does not rely on Redis counters, local mutexes, process memory, or
application-level pre-checks to protect stock. Those techniques break down when
multiple API processes, workers, or machines are involved.

Instead, the project uses:

- atomic SQL updates for quantity inventory;
- short transactions;
- row-level locks for seats and reservation lifecycle transitions;
- deterministic lock ordering to reduce deadlocks;
- partial unique indexes for active seat reservations;
- idempotency keys for safe client retries;
- `FOR UPDATE SKIP LOCKED` for concurrent expiration workers;
- database check constraints to reject invalid counters.

Expected conflicts return `409 Conflict`, not `500 Internal Server Error`.

## Quantity reservation: atomic UPDATE

Quantity reservation is protected by a single PostgreSQL statement:

```sql
UPDATE ticket_types
SET reserved_quantity = reserved_quantity + $3
WHERE id = $1
  AND event_id = $2
  AND total_quantity - sold_quantity - reserved_quantity >= $3
RETURNING id, total_quantity, sold_quantity, reserved_quantity;
```

The availability check and the counter increment happen in the same statement.
PostgreSQL serializes concurrent updates to the same row and rechecks the `WHERE`
clause when needed. This prevents two requests from both reading the same
available stock and reserving the same last ticket.

If the statement returns no row, the API responds with:

```http
409 Conflict
```

The service then creates:

- one `reservations` row;
- one `reservation_items` row;
- an expiration timestamp, defaulting to `now + 15 minutes`.

No Redis state is used to decide whether quantity inventory is available.

## Seat reservation: FOR UPDATE and partial unique index

Seat reservation uses a short transaction. First, the service validates and locks
the requested seats in deterministic order:

```sql
SELECT id
FROM seats
WHERE event_id = $1
  AND id = ANY($2::uuid[])
ORDER BY id
FOR UPDATE;
```

`FOR UPDATE` prevents another transaction from modifying the same seat rows until
the current transaction commits or rolls back. The `ORDER BY id` is intentional:
when two requests ask for the same seats in different client-side orders, the
database locks rows in a stable order, reducing recurring deadlocks.

Before inserting new `reservation_seats`, the transaction expires stale active
holds for the requested seats. Then the insert is protected by this partial unique
index:

```sql
CREATE UNIQUE INDEX uq_active_reservation_seat
ON reservation_seats(seat_id)
WHERE status IN ('reserved', 'confirmed');
```

This index means a seat can have historical cancelled or expired rows, but only
one active row with status `reserved` or `confirmed`.

The lock is useful for predictable transaction behavior. The partial unique index
is the final database-level guarantee that duplicate active seat reservations
cannot exist.

## Idempotency

Every reservation request includes an `idempotency_key`.

The database enforces:

```text
unique(visitor_session_id, idempotency_key)
```

If a client retries the same request with the same visitor session and the same
idempotency key, the API returns the reservation that was already created instead
of reserving stock again.

This protects against:

- client retries after network timeouts;
- duplicate browser submissions;
- repeated requests from stress tests;
- race conditions where the same logical request is sent concurrently.

Idempotency is scoped to the visitor session, so different users can use the same
client-generated key without colliding with each other.

## Reservation expiration

Reservations start with status `reserved` and an `expires_at` timestamp.

The `worker` service runs a local loop every 5 seconds. Each batch:

1. finds expired `reserved` reservations;
2. locks them with `FOR UPDATE SKIP LOCKED`;
3. releases quantity inventory by decrementing `reserved_quantity`;
4. marks seat holds as `expired`;
5. marks reservation headers as `expired`.

`SKIP LOCKED` allows multiple workers to run at the same time. If one worker
locks a reservation, another worker skips it and continues with different rows.
This avoids duplicate logical processing and avoids blocking the whole queue.

Cancellation and confirmation use the same consistency principle: lock the
reservation row first, then apply guarded stock transitions in the same
transaction.

## Consistency assertion endpoint

After concurrency tests or k6 runs, call:

```bash
curl http://localhost:8080/v1/admin/stress/assert-consistency
```

Healthy response:

```json
{
  "ok": true,
  "checks": {
    "ticket_quantity_not_oversold": true,
    "ticket_quantity_not_negative": true,
    "no_duplicate_active_seats": true,
    "no_orphan_reservation_items": true,
    "no_orphan_reservation_seats": true,
    "no_stale_expired_active_reservations": true
  },
  "details": []
}
```

The endpoint checks that:

- ticket counters are never negative;
- `sold_quantity + reserved_quantity` never exceeds `total_quantity`;
- no seat has more than one active reservation seat row;
- reservation item rows are not orphaned;
- reservation seat rows are not orphaned;
- active expired reservations are not stale beyond the local tolerance window.

This endpoint is a practical post-stress-test safety check. It does not replace
database constraints, but it makes invariant violations easy to detect and debug.

## Error handling and logs

The API returns standardized JSON errors:

```json
{
  "error": {
    "code": "INSUFFICIENT_STOCK",
    "message": "Not enough stock available",
    "request_id": "uuid"
  }
}
```

The request ID middleware:

- preserves an incoming `X-Request-ID` when present;
- generates a UUID when missing;
- returns `X-Request-ID` in the response.

Structured logs use `slog` and include:

- method;
- path;
- status code;
- duration in milliseconds;
- request ID;
- remote address;
- user agent.

Passwords and full cookies are not logged.

## Useful commands

| Command | Description |
| --- | --- |
| `make up` | Builds and starts the full Docker Compose environment. |
| `make down` | Stops the Docker Compose environment without removing volumes. |
| `make test` | Runs the Go test suite inside the API container. |
| `make fmt` | Formats Go code with `go fmt`. |
| `make vet` | Runs `go vet` inside the API container. |
| `make tidy` | Updates `go.mod` and `go.sum` with `go mod tidy`. |
| `make logs` | Follows API and worker container logs. |
| `make migrate-up` | Runs pending PostgreSQL migrations. |
| `make migrate-down` | Rolls back one PostgreSQL migration. |
| `make sqlc` | Regenerates type-safe Go code from SQL queries. |
| `make stress-reset` | Resets local stress-test fixtures through the admin endpoint. |
| `make stress-seed` | Creates local stress-test fixtures through the admin endpoint. |
| `make stress` | Runs the mixed k6 stress script. |
| `make k6` | Alias for `make stress`. |
| `make stress-quantity` | Runs the quantity-only k6 stress script. |
| `make stress-seats` | Runs the seat-only k6 stress script. |
| `make assert` | Calls the consistency assertion endpoint. |

## Current limitations

- No real payment integration.
- No frontend.
- No production deployment configuration.
- Authentication is intentionally simplified for study purposes.
- Local stress testing is limited by the hardware of the machine running Docker.
- The local admin stress endpoints are designed for development and portfolio demonstration, not public production exposure.

## Suggested next steps

- Full JWT-based authentication and refresh-token flow.
- OpenTelemetry tracing.
- Prometheus metrics.
- Production deployment configuration.
- GitHub Actions CI.
- Rate limiting.
- Testcontainers-based integration tests.
- A direct comparison with the Python version of the same project.
