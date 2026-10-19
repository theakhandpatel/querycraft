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
| `POST` | `/auth/signup` | Create a **learner** account and start a session; `201` with `{user, token, expiresAt}`. Sets the `qc_session` cookie for the SPA; the token is for API clients. `409 AUTH_EMAIL_TAKEN` on a duplicate address. |
| `POST` | `/auth/login` | Exchange email + password for a session. Unknown email and wrong password both return the same `401 AUTH_INVALID_CREDENTIALS`. Repeated failures for one address against one account return `429 AUTH_RATE_LIMITED`. |
| `POST` | `/auth/logout` | Revoke the session and clear the cookie. A revoked or absent session is refused with `401 AUTH_REQUIRED`. |
| `GET` | `/auth/me` | The account behind the session (cookie or bearer token). |
| `GET` | `/progress/{course}` | 🔒 Load the caller's progress snapshot for a course. |
| `PUT` | `/progress/{course}` | 🔒 Replace the caller's JSON progress snapshot for a course. |
| `POST` | `/events` | 🔒 Append an audit/attempt event; returns `202 Accepted`. |
| `POST` | `/courses/generate` | Generate a course skeleton (title, chapters, exercises, dataset version). Set `require_approval: true` to stop after the outline: the outcome is then `awaiting_approval` and `POST /courses/{id}/build` continues it. |
| `POST` | `/courses/{id}/build` | 🔒 Approve a paused course: continue its stored outline into the chapter phase. `404` for another account's course; `409` when the course is not still being built. Accepts `Prefer: respond-async` (or `async=1`) like the generate routes. |
| `GET` | `/admin/courses` | 🔒👑 Every stored course, every owner, with owner ids and statuses — the manage surface's inventory. A learner is refused with `403 ADMIN_REQUIRED`. |
| `POST` | `/challenges/generate` | Generate an AI challenge. |
| `GET` | `/generation/{id}` | 🔒 Poll a generation submitted asynchronously: the pipeline's stages while it runs, the outcome and the stored record id when it finishes. |
| `GET` | `/runs` | 🔒 List the caller's generation-run transcripts, newest first (bounded to 100). |
| `GET` | `/runs/{id}` | 🔒 Read one run's whole transcript: every provider message, tool call and tool result, in order: owner-scoped (`404` for another account's run), redacted and bounded (`contracts/run.schema.json`). |

Both generation routes answer **synchronously by default**: a script reads the
whole outcome from one request. A run takes minutes, so a client that cannot
hold a request open sends `Prefer: respond-async` (or `?async=1`) and gets
`202 Accepted` with `{"ok":true,"job":{"id":…,"kind":…,"status":"running"}}`,
then polls `GET /generation/{id}` until the status is no longer `running`. The
run continues on its own context: a client that goes away does not cancel work
the learner already paid for. Finished jobs are bounded per owner (the stored
record is the durable thing), and a job is readable only by the account that
submitted it: another learner gets `404`, the same rule the record routes
follow.

Every generation run also leaves an **audit trail**: its whole conversation,
every provider message, tool call and tool result, in order, is persisted to
`app.runs` and read back with `GET /runs` and `GET /runs/{id}`
(`contracts/run.schema.json`). The record is captured at the provider seam
(`internal/ai/transcript/`), so the challenge pipeline and the M8 course agent
are both covered; the course agent records its own conversation as it assembles
it, because each tool result is the engine's own verdict. Three properties hold
by construction:

* **Owner-scoped**: the record is stamped with the session's account, the same
  wrapper that stamps a stored challenge or course, and another account's run is
  `404`, never `403`.
* **Redacted**: DSN credentials, `Authorization` headers, named key/secret/token
  fields, vendor key prefixes and the provider's own API key are removed before
  the value is stored, and an entry that was scrubbed says so.
* **Bounded**: at most `ai.MaxTranscriptEntries` (512) entries and
  `ai.MaxTranscriptBytes` (256 KiB) per run, with one entry clipped at 8000
  characters. A run that hits a bound stores a prefix of itself and reports
  `truncated`; it can never grow the row without limit.

Errors use stable codes, never free text alone: `EMPTY_QUERY`,
`QUERY_SYNTAX_ERROR`, `QUERY_RUNTIME_ERROR`, `QUERY_TIMEOUT`,
`RESULT_LIMIT_EXCEEDED`, `ENVIRONMENT_UNAVAILABLE`, `PERSISTENCE_FAILED`,
`EVALUATION_ERROR`, plus `AUTH_REQUIRED`, `AUTH_INVALID_INPUT`,
`AUTH_INVALID_CREDENTIALS`, `AUTH_EMAIL_TAKEN` and `AUTH_RATE_LIMITED` on the
auth surface. Error responses are shaped as
`{"error":{"code":"...","message":"..."}}` (or inside a `QueryResult` for
`/query` and `/answers/evaluate`).

## Accounts and sessions

Progress and events are **owner-scoped**: each read and write names the account
it belongs to, and that id always comes from the session, never from the request
body. Two accounts can therefore hold independent snapshots for the same course,
and a body field cannot redirect a write onto another user's rows.

The SPA authenticates with the `qc_session` cookie: `HttpOnly`, `SameSite=Lax`,
`Path=/`, and `Secure` when the request arrived over TLS. The browser sends it on
same-origin requests, so the page never stores the credential where a script
could read it. A mutating request that arrives with the cookie is refused with
`403 CSRF_REFUSED` unless its `Origin`/`Sec-Fetch-Site` says same-origin; a
request with neither header is a non-browser client and is allowed. Cross-origin
cookie auth is not supported, because `SameSite=Lax` withholds the cookie
cross-site and the gateway's CORS policy uses the wildcard origin; the SPA must
be served same-origin, which the single binary does.

API clients use the opaque bearer token that signup and login return in the
response body (`Authorization: Bearer <token>`); the header wins over the cookie
when both are present, and a present-but-invalid token does not fall back to
the cookie. The token is 256 bits from `crypto/rand`, stored in `app.sessions`
with a 30-day TTL. Revocation is a `DELETE`, so logout is immediate; there is no
signature key to leak. Passwords are bcrypt (`golang.org/x/crypto/bcrypt`).

A fresh install seeds one administrator (`admin_email`, default
`admin@querycraft.local`) whose default password is **empty**, so a local install
needs no setup. That account can sign in with a blank password on a loopback
bind only: the gateway refuses to start with an empty admin password when
`addr` is not loopback, so a network-exposed install must set
`admin_password`. Any non-empty password will not match an empty stored hash.
Public signups always receive the `learner` role: admin is not self-service.
Seeding is idempotent and never rewrites the password of an existing account.

Anonymous access is still allowed for the public surface (`/health`,
`/capabilities`, `/query`, `/answers/evaluate`, `/datasets/{id}/tables`,
`/openapi.json`, `/docs`). A missing or unknown session leaves those alone;
every other route answers `401 AUTH_REQUIRED`.

### Failed sign-in throttling

`/auth/login` is the one endpoint where an anonymous caller can spend the
gateway's time guessing a secret, and the seeded administrator's address is
public knowledge, so attempts are limited. After `login_max_failures`
failures for one **client address and one account** inside `login_window_ms`,
the next attempt is refused with `429 AUTH_RATE_LIMITED` and a `Retry-After`
header carrying whole seconds; below the limit the answer is the ordinary
`401 AUTH_INVALID_CREDENTIALS`, unchanged. A sign-in that succeeds clears the
pair's failures.

The limit is deliberately small and self-contained:

- **In process, in memory.** No dependency and no Redis: one mutex-guarded map
  keyed by `address + normalized email`, bounded to `login_max_tracked`
  entries. An expired record is dropped on the way in, and at the cap the
  record closest to expiry is evicted, so the map cannot grow without bound. It
  never stores the attempted password.
- **Keyed on the socket address, never `X-Forwarded-For`.** A caller-supplied
  header would be a budget an attacker mints at will.
- **Not a distributed defence.** It does not survive a restart and is not shared
  between processes, so two gateways behind one address hold a budget each. It
  is a speed bump on one source guessing one account, not a shield against a
  botnet, and a refusal says nothing about whether the account exists.
- **Set any login limit to `0` or below to fall back to the documented default**.
It cannot be used to disable the limit or leave the table unbounded.

Every response carries an `X-Request-Id`; a caller-supplied `X-Request-Id` is
echoed back. CORS is open (`Access-Control-Allow-Origin: *`), permits the
`Authorization` request header, and preflight `OPTIONS` returns `204`. The
wildcard origin cannot be combined with credentials, so a cross-origin page
cannot authenticate with the session cookie; the SPA runs same-origin.

## Configuration

```mermaid
graph LR
    D["Shipped defaults<br/>internal/config.Defaults"] --> F["Config file<br/>--config, then ./querycraft.json"]
    F --> A["Command line<br/>--addr on serve"]
    A --> S["Settings<br/>one resolved value per key"]
    S --> R["run() in cmd/api<br/>wires the gateway"]
```

Every setting resolves through the three layers above, each overriding the one
before it. A layer that says nothing about a key leaves the earlier value
standing. The loader is `internal/config`, and the precedence test in
`config_test.go` proves each arrow.

The file is JSON with one snake_case key per setting:

```json
{
  "addr": "127.0.0.1:9000",
  "max_rows": 200,
  "timeout_ms": 10000,
  "database_url": "postgres://user:pass@db.internal:5432/querycraft?sslmode=disable",
  "admin_database_url": "postgres://admin:pass@db.internal:5432/querycraft?sslmode=disable",
  "max_environments": 16,
  "idle_ttl": "30m",
  "ai_provider": "openai-compatible",
  "ai_base_url": "https://llm.internal.example/v1",
  "ai_model": "inhouse-4",
  "ai_key": "sk-local-only"
}
```

Rules the loader enforces:

- The file is looked up in this order: the `--config` flag, then
  `querycraft.json` in the working directory. A named file that is missing
  is an error; the default location is optional.
- An unknown key, a wrong type, or a malformed file fails the start-up, so a
  typo cannot silently do nothing. The accepted keys are the cases of the
  switch in `internal/config.applyKey`.
- A duration (`idle_ttl`, `sqlite_idle`) is a string like `"30m"`. A bare
  number is refused, because its unit would be a guess.
- Secrets (`pg_password`, `admin_password`, `ai_key`, and the DSN keys) live
  in the file. None of them is ever logged; the logs carry only `keySet`
  booleans.

### The CLI

The binary is a cobra CLI (`cmd/api/cli.go`). `serve` is also the root
command's behavior, so a bare `querycraft` starts the gateway.

```sh
querycraft                    # start the gateway (same as `serve`)
querycraft serve --addr 127.0.0.1:9000
querycraft db reset           # delete the local cluster's data directory; the next boot re-applies the dataset
querycraft db status
querycraft version
```

`db status` reports what the gateway would resolve right now, without starting
anything:

```text
config file: /etc/querycraft/querycraft.json
listen address: 127.0.0.1:9000
database: external (database_url)
cluster data directory: .data/pg/data (exists)
binary cache: .data/pg/binaries (exists)
metadata: postgresql (admin_database_url)
ai provider: openai-compatible (model inhouse-4)
```

## Configuration keys

| Key | Default | Meaning |
| --- | --- | --- |
| `addr` | `127.0.0.1:8080` | Listen address for the HTTP server. Loopback by default; a non-loopback bind requires `admin_password`. |
| `database_url` | _(unset → local server)_ | Learner execution DSN. When unset the gateway starts its own local PostgreSQL (see below). When set (or unreachable) it uses that server. Never an administrative connection. |
| `admin_database_url` | falls back to `database_url` | PostgreSQL metadata DSN for accounts, sessions, progress, events, challenges, courses, patches and runs. When set it takes precedence over `metadata`; when unset, metadata stays in memory unless SQLite is explicitly selected. |
| `metadata` | _(unset → current behavior)_ | Set to `sqlite` to store app metadata in SQLite while learner SQL still uses the configured execution engine. If unset, Postgres DSN selection and the in-memory fallback are unchanged. |
| `metadata_path` | `.data/pg/metadata.sqlite` (derived from `pg_data`) | File used when `metadata` is `sqlite`; created with its parent directory if absent. |
| `admin_email` | `admin@querycraft.local` | Address the seeded default administrator gets. |
| `admin_password` | _(empty)_ | Password for the seeded administrator. Empty means "sign in with a blank password", which is allowed only on a loopback bind; the gateway refuses to start with an empty password on a non-loopback `addr`. Existing accounts are never reset. |
| `admin_name` | `Administrator` | Display name for the seeded administrator. |
| `no_local_db` | `0` | `1` = never start the local server, even with no DSN. |
| `pg_port` | `54329` | Port for the local PostgreSQL server. |
| `pg_data` | `.data/pg/data` | Cluster data directory for the local server. |
| `pg_runtime` | `.data/pg/runtime` | Where the extracted server binaries live. |
| `pg_binaries` | `.data/pg/binaries` | Download cache for the prebuilt binaries. |
| `pg_user` / `pg_password` / `pg_database` | `querycraft` | Credentials for the local server. |
| `max_rows` | `500` | Maximum rows returned per query. |
| `max_bytes` | `1048576` (1 MiB) | Maximum serialized result bytes per query. |
| `timeout_ms` | `5000` | Hard per-query timeout in milliseconds. |
| `max_concurrent` | `4` | Advertised maximum concurrent queries (`/capabilities`). |
| `sqlite_data` | `.data/sqlite` (derived from `pg_data`) | Data root for the SQLite dialect: `<root>/accounts/<hash>.db` per learner and `<root>/commerce.db` shared read-only. |
| `sqlite_max_accounts` | `8` | Maximum per-account SQLite executors cached at once. Past the bound the least-recently-used pool is closed and evicted; the learner's file stays on disk and re-opens on their next statement. |
| `sqlite_idle` | `15m` | Idle window for a cached SQLite executor, as a Go duration (`15m`, `1h`). A pool unused for this long is closed on the next access; the file is untouched. |
| `login_max_failures` | `5` | Failed sign-ins one address may make against one account inside the window before `/auth/login` answers `429 AUTH_RATE_LIMITED`. |
| `login_window_ms` | `900000` (15 min) | Fixed window, in milliseconds, a pair's failures are counted over. |
| `login_max_tracked` | `4096` | Upper bound on tracked (address, account) pairs in the in-process sign-in throttle. |
| `max_environments` | `8` | Maximum learner challenge environments open at once (ADR 0006). Past the bound a new environment is refused with `ENVIRONMENT_UNAVAILABLE` until the idle reaper closes one. |
| `idle_ttl` | `15m` | Idle window for an open challenge environment, as a Go duration (`15m`, `1h`). The idle reaper closes an environment unused this long, which frees capacity for a new one. |

## The local database (no Docker)

`internal/localdb` makes the gateway self-hosting. When `database_url` is
unset and `no_local_db` is false, it

1. downloads the prebuilt PostgreSQL binaries once into `.data/pg/binaries`;
2. `initdb`s a cluster at `.data/pg/data` and starts it on `pg_port`;

   Both paths are relative to the working directory the gateway starts from:
   `make serve` runs from the repository root, `go run ./cmd/api` from
   `backend/`. `querycraft db status` prints the resolved paths;
3. applies `internal/dataset/schema.sql` + `seed.sql` if the dataset is absent.

Everything above that package speaks the ordinary PostgreSQL wire protocol, so
pointing `database_url` at a container, a `postgres` service in
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
  serving. The dataset and everything saved to it are still there. Only a
  server whose data directory proves ownership is adopted, so an unrelated
  PostgreSQL holding the port is never hijacked.
- An adopted server is not stopped on shutdown, because this process did not
  start it.

## Graceful degradation

The service is designed to run usefully with no database configured.

- With no `database_url`, SQL execution is backed by an unavailable executor:
  `POST /query` and `POST /answers/evaluate` still respond, but every statement
  fails with the stable code `ENVIRONMENT_UNAVAILABLE`. `/health` still returns
  `200` with the build version.
- Every other endpoint keeps working: `/capabilities`, `/datasets/{id}/tables`,
  `/progress/{course}`, `/events`, `/courses/generate` and
  `/challenges/generate`.
- With no `admin_database_url`, progress and events are kept in an
  in-memory store that is lost on restart unless `metadata` is `sqlite`.
  The SQLite file stores app metadata only; learner SQL still requires a
  PostgreSQL execution database. Alternatively set a metadata/Postgres DSN to
  persist metadata in PostgreSQL. Agent-run transcripts live in the same store;
  with generation off, `GET /runs` answers `503` rather than inventing a
  transcript.

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
printf '{"database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable","admin_database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable"}\n' > querycraft.json
go run ./cmd/api
```

`docker compose up -d db` starts an external PostgreSQL 16 if you prefer one
server for several worktrees; the default local flow never needs it.

The HTTP surface has a gated integration test that exercises a real PostgreSQL
database. It is skipped unless `QC_TEST_DATABASE_URL` (a test-harness variable,
not gateway configuration) is set, so the default
`go test ./...` run needs no database:

```sh
QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable' \
  go test ./internal/httpapi -run TestGatewayIntegration -v
```

## The test fixture

Tests that need a real server use `internal/pgtest`. It starts the same
embedded PostgreSQL the gateway ships, so a test runs against the commerce
dataset and the restricted learner role with no Docker and no network.

| Entry point | Cluster | Database | Use it for |
| --- | --- | --- | --- |
| `pgtest.Shared(t)` | one per package test binary, started on first use | a private database per test, copied from the dataset template with `CREATE DATABASE ... TEMPLATE` | the default. A test gets a virgin database, and two tests cannot meet. |
| `pgtest.Start(t)` | one per test | the test's own database | a test that stops and restarts its own server (the `SurviveARestart` pattern), or one that deliberately leaks cluster-global state. |

A package that calls `pgtest.Shared` declares one `TestMain`:

```go
func TestMain(m *testing.M) { pgtest.Main(m) }
```

`pgtest.Main` stops the package cluster after the tests and exits with their
code. A shared database is a copy of the dataset template, so a test that needs
the app metadata schema opens it through `store.NewPostgres`, exactly as
production does.

The first boot pays for `initdb` and the dataset seed, then caches the result as
a golden cluster under `.data/pg/golden`. Every later boot copies that cluster,
so a shared test binary pays the expensive half once.

A test binary killed with `SIGKILL` is the one cleanup `t.Cleanup` and
`pgtest.Main` cannot serve: the postmaster would survive as an orphan.
`startShared` leaves a detached watchdog behind for that case. The watchdog
polls the test binary's pid, then kills the postmaster from the data directory
and removes the cluster. It runs in its own process group, so a signal aimed at
the test run cannot take it down with the binary it is watching.

