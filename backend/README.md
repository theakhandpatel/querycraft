# Querycraft execution gateway (Adapter B)

The gateway is a JSON HTTP service that runs learner SQL against native
PostgreSQL and grades it against result contracts. It is Adapter B in the
technical design: it owns query isolation, resource limits, cancellation and
result caps behind the same `ExecutionService` contracts the React client already
uses. The Go evaluator compares query *results*, never SQL text.

Module: `github.com/querycraft/sqlquery/backend`.

## Endpoints

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/health` | Liveness plus the execution engine version (`status`, `engine`, `version`). |
| `GET` | `/capabilities` | Advertised limits: `hard_timeout`, `explain`, `max_concurrent`, `isolation` (`read_only_tx`). |
| `POST` | `/query` | Run a single statement and return a `QueryResult` (columns, rows, row count, truncation, duration, stable error code). |
| `POST` | `/answers/evaluate` | Run the learner's SQL and grade it against the exercise's result contract; returns `passed` plus detail. |
| `GET` | `/datasets/{id}/tables` | Dataset introspection (schema, row counts, preview) without a live database. Only `commerce` is defined. |
| `POST` | `/auth/signup` | Create a **learner** account and start a session; `201` with `{user, token, expiresAt}`. `409 AUTH_EMAIL_TAKEN` on a duplicate address. |
| `POST` | `/auth/login` | Exchange email + password for a session token. Unknown email and wrong password both return the same `401 AUTH_INVALID_CREDENTIALS`. |
| `POST` | `/auth/logout` | Revoke the bearer token. Idempotent. |
| `GET` | `/auth/me` | The account behind the bearer token. |
| `GET` | `/progress/{course}` | 🔒 Load the caller's progress snapshot for a course. |
| `PUT` | `/progress/{course}` | 🔒 Replace the caller's JSON progress snapshot for a course. |
| `POST` | `/events` | 🔒 Append an audit/attempt event; returns `202 Accepted`. |
| `POST` | `/courses/generate` | Generate a course skeleton (title, chapters, exercises, dataset version). |
| `POST` | `/challenges/generate` | Generate an AI challenge. |

Errors use stable codes, never free text alone: `EMPTY_QUERY`,
`QUERY_SYNTAX_ERROR`, `QUERY_RUNTIME_ERROR`, `QUERY_TIMEOUT`,
`RESULT_LIMIT_EXCEEDED`, `ENVIRONMENT_UNAVAILABLE`, `PERSISTENCE_FAILED`,
`EVALUATION_ERROR`, plus `AUTH_REQUIRED`, `AUTH_INVALID_INPUT`,
`AUTH_INVALID_CREDENTIALS` and `AUTH_EMAIL_TAKEN` on the auth surface. Error
responses are shaped as `{"error":{"code":"...","message":"..."}}` (or inside a
`QueryResult` for `/query` and `/answers/evaluate`).

## Accounts and sessions

Progress and events are **owner-scoped**: each read and write names the account
it belongs to, and that id always comes from the bearer token — never from the
request body. Two accounts can therefore hold independent snapshots for the same
course, and a body field cannot redirect a write onto another user's rows.

Transport is an opaque session token (`Authorization: Bearer <token>`), 256 bits
from `crypto/rand`, stored in `app.sessions` with a 30-day TTL. Revocation is a
`DELETE`, so logout is immediate; there is no signature key to leak. Passwords
are bcrypt (`golang.org/x/crypto/bcrypt`).

A fresh install seeds one administrator (`QC_ADMIN_EMAIL`, default
`admin@querycraft.local`) whose default password is **empty**, so a local install
needs no setup. That account can sign in with a blank password; any non-empty
password will not match it. Set `QC_ADMIN_PASSWORD` before exposing the port.
Public signups always receive the `learner` role — admin is not self-service.
Seeding is idempotent and never rewrites the password of an existing account.

Anonymous access is still allowed for the public surface (`/health`,
`/capabilities`, `/query`, `/answers/evaluate`, `/datasets/{id}/tables`, the
generators, `/openapi.json`, `/docs`). A missing or unknown token leaves those
alone; the scoped routes answer `401 AUTH_REQUIRED`.

Every response carries an `X-Request-Id`; a caller-supplied `X-Request-Id` is
echoed back. CORS is open (`Access-Control-Allow-Origin: *`), permits the
`Authorization` request header, and preflight `OPTIONS` returns `204`.

## Environment variables

| Variable | Default | Meaning |
| --- | --- | --- |
| `QC_ADDR` | `:8080` | Listen address for the HTTP server. |
| `QC_DATABASE_URL` | _(unset → local server)_ | Learner execution DSN. When unset the gateway starts its own local PostgreSQL (see below). When set (or unreachable) it uses that server. Never an administrative connection. |
| `QC_ADMIN_DATABASE_URL` | `$QC_DATABASE_URL` | Metadata DSN for accounts, sessions, progress snapshots and events. When unset the in-memory store is used (accounts then vanish on restart). |
| `QC_ADMIN_EMAIL` | `admin@querycraft.local` | Address the seeded default administrator gets. |
| `QC_ADMIN_PASSWORD` | _(empty)_ | Password for the seeded administrator. Empty means "sign in with a blank password"; set it before exposing the port. Existing accounts are never reset. |
| `QC_ADMIN_NAME` | `Administrator` | Display name for the seeded administrator. |
| `QC_NO_LOCAL_DB` | `0` | `1` = never start the local server, even with no DSN. |
| `QC_PG_PORT` | `54329` | Port for the local PostgreSQL server. |
| `QC_PG_DATA` | `.data/pg/data` | Cluster data directory for the local server. |
| `QC_PG_RUNTIME` | `.data/pg/runtime` | Where the extracted server binaries live. |
| `QC_PG_BINARIES` | `.data/pg/binaries` | Download cache for the prebuilt binaries. |
| `QC_PG_USER` / `QC_PG_PASSWORD` / `QC_PG_DATABASE` | `querycraft` | Credentials for the local server. |
| `QC_MAX_ROWS` | `500` | Maximum rows returned per query. |
| `QC_MAX_BYTES` | `1048576` (1 MiB) | Maximum serialized result bytes per query. |
| `QC_TIMEOUT_MS` | `5000` | Hard per-query timeout in milliseconds. |
| `QC_MAX_CONCURRENT` | `4` | Advertised maximum concurrent queries (`/capabilities`). |

## The local database (no Docker)

`internal/localdb` makes the gateway self-hosting. When `QC_DATABASE_URL` is
unset and `QC_NO_LOCAL_DB` is not set, it

1. downloads the prebuilt PostgreSQL binaries once into `.data/pg/binaries`;
2. `initdb`s a cluster at `.data/pg/data` and starts it on `QC_PG_PORT`;
3. applies `internal/dataset/schema.sql` + `seed.sql` if the dataset is absent.

Everything above that package speaks the ordinary PostgreSQL wire protocol, so
pointing `QC_DATABASE_URL` at a container, a `postgres` service in
`docker-compose.yml`, or a managed server replaces it with no code change. The
dataset is applied once per cluster; `make db-reset` (repo root) deletes the
cluster so the next boot re-applies it.

### Lifecycle

The cluster's lifetime is tied to the gateway's:

- `SIGINT`, `SIGTERM` **and `SIGHUP`** all shut the gateway down gracefully and
  stop the server it started. `SIGHUP` matters because a closing terminal or an
  exiting parent shell sends it; without a handler the process would die
  instantly and orphan the postmaster.
- If a gateway is killed hard (`SIGKILL`) and leaves the postmaster running,
  the next boot **adopts** it instead of failing: it checks for
  `postmaster.pid` in the configured data directory, reconnects, and keeps
  serving — the dataset and everything saved to it are still there. Only a
  server whose data directory proves ownership is adopted, so an unrelated
  PostgreSQL holding the port is never hijacked.
- An adopted server is not stopped on shutdown, because this process did not
  start it.

## Graceful degradation

The service is designed to run usefully with no database configured.

- With no `QC_DATABASE_URL`, SQL execution is backed by an unavailable executor:
  `POST /query` and `POST /answers/evaluate` still respond, but every statement
  fails with the stable code `ENVIRONMENT_UNAVAILABLE`. `/health` still returns
  `200` with the build version.
- Every other endpoint keeps working: `/capabilities`, `/datasets/{id}/tables`,
  `/progress/{course}`, `/events`, `/courses/generate` and
  `/challenges/generate`.
- With no `QC_ADMIN_DATABASE_URL`, progress and events are kept in an
  in-memory store that is lost on restart. Set it (or `QC_DATABASE_URL`) to
  persist them in PostgreSQL.

This means the React client always has something to talk to, whether or not a
database is running.

## Build, run and test

Go is not assumed to be on `PATH`; add it first if needed.

```sh
export PATH=$PATH:/usr/local/go/bin
cd backend

go build ./...
go vet ./...
go test ./...

# Run the gateway (defaults to :8080; starts its own PostgreSQL).
go run ./cmd/api
```

`go run ./cmd/api` boots the local PostgreSQL the first time (~10 s: it also
downloads the server binaries), then answers instantly on later runs. To use an
external server instead:

```sh
export QC_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable'
export QC_ADMIN_DATABASE_URL="$QC_DATABASE_URL"
go run ./cmd/api
```

`docker compose up -d db` starts an external PostgreSQL 16 if you prefer one
server for several worktrees; the default local flow never needs it.

The HTTP surface has a gated integration test that exercises a real PostgreSQL
database. It is skipped unless `QC_TEST_DATABASE_URL` is set, so the default
`go test ./...` run needs no database:

```sh
QC_TEST_DATABASE_URL="$QC_DATABASE_URL" go test ./internal/httpapi -run TestGatewayIntegration -v
```
