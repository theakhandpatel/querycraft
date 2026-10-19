# Querycraft gateway

The gateway is a JSON HTTP service that runs learner SQL against PostgreSQL or
SQLite and grades the result against a contract. It owns query isolation,
resource limits, cancellation, and result caps behind the same
`ExecutionService` contracts the React app uses. It compares query results,
never SQL text.

Module: `github.com/querycraft/sqlquery/backend`.

## Endpoints

A route marked public accepts an anonymous caller. Every other route answers
`401 AUTH_REQUIRED` without a session. Admin routes also answer
`403 ADMIN_REQUIRED` for a learner.

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `GET` | `/health` | Public | Liveness plus the execution engine version. |
| `GET` | `/capabilities` | Public | Limits, isolation, dialects, plan support, and AI availability. |
| `GET` | `/metrics` | Public | Prometheus counters, histograms, and pool gauges. |
| `GET` | `/diagnostics` | Session | Pool state and the learner role's privileges. |
| `GET` | `/openapi.json` | Public | OpenAPI 3.1 document. |
| `GET` | `/docs`, `/docs/` | Public | Swagger UI. |
| `POST` | `/auth/signup` | Public | Create a learner account and start a session. Returns `201` with `{user, token, expiresAt}` and sets the `qc_session` cookie. |
| `POST` | `/auth/login` | Public | Exchange an email and password for a session. A wrong password and an unknown address both answer `401 AUTH_INVALID_CREDENTIALS`. |
| `POST` | `/auth/logout` | Session | Revoke the session and clear the cookie. |
| `GET` | `/auth/me` | Session | Read the account behind the cookie or bearer token. |
| `POST` | `/query` | Public | Run one statement and return a `QueryResult`. |
| `POST` | `/explain` | Public | Return a PostgreSQL plan bundle, with optional `EXPLAIN ANALYZE` and perf-contract grading. |
| `POST` | `/answers/evaluate` | Public | Run the learner's SQL and grade it against an exercise contract. |
| `GET` | `/environment` | Public | Report the caller's workspace. An anonymous caller is told it has none. |
| `POST` | `/environment/reset` | Session | Recreate the caller's workspace from the sealed template. Accepts `course` and `dialect` query parameters. |
| `POST` | `/environment/prepare` | Session | Create the workspace and apply a stored course's setup once per generation. |
| `GET` | `/datasets/{id}/tables` | Public | Introspect a dataset: schema, row counts, and preview rows. |
| `GET` | `/progress/{course}` | Session | Load the caller's progress snapshot for a course. |
| `PUT` | `/progress/{course}` | Session | Replace the caller's progress snapshot for a course. |
| `GET` | `/preferences` | Session | Read the caller's UI preferences. |
| `PUT` | `/preferences` | Session | Store the caller's UI preferences. |
| `POST` | `/events` | Session | Append an attempt event. Returns `202 Accepted`. |
| `POST` | `/courses/generate` | Session | Generate a course. Set `require_approval: true` to stop after the outline. |
| `POST` | `/courses/{id}/build` | Session | Continue a paused course into the chapter phase. |
| `GET` | `/courses` | Session | List the caller's stored courses. |
| `GET` | `/courses/{id}` | Session | Read one stored course. |
| `PUT` | `/courses/{id}` | Session | Edit a stored course. |
| `DELETE` | `/courses/{id}` | Session | Delete a stored course. |
| `GET` | `/admin/courses` | Admin | List every stored course and owner. |
| `POST` | `/challenges/generate` | Session | Generate an AI challenge. |
| `GET` | `/challenges` | Session | List the caller's stored challenges. |
| `GET` | `/challenges/{id}` | Session | Read one challenge. |
| `POST` | `/challenges/{id}/reset` | Session | Reset a challenge workspace. |
| `POST` | `/challenges/{id}/critique` | Session | Run a model critique of the challenge. |
| `POST` | `/challenges/{id}/report` | Session | Report a defect in a challenge. |
| `POST` | `/challenges/{id}/patches/{patchID}` | Session | Apply a stored patch. |
| `POST` | `/tutor` | Session | Ask the tutor about the learner's query and error. |
| `GET` | `/runs` | Session | List the caller's generation transcripts, newest first, bounded to 100. |
| `GET` | `/runs/{id}` | Session | Read one transcript in order. |
| `GET` | `/admin/ai` | Admin | Read the live AI provider and where its configuration came from. |
| `GET` | `/admin/ai/models` | Admin | List the embedded chat-model catalog. |
| `PUT` | `/admin/ai` | Admin | Validate, store, and swap the live AI provider without a restart. |
| `GET` | `/` | Public | Serve the embedded SPA. |

Both generation routes answer synchronously by default. A script reads the whole
outcome from one request. A run can take minutes, so a client that cannot hold a
request open sends `Prefer: respond-async` or `?async=1`. The gateway then
answers `202 Accepted` with
`{"ok":true,"job":{"id":...,"kind":...,"status":"running"}}`. Poll
`GET /generation/{id}` until the status is no longer `running`.

The run continues on its own context. A client that goes away does not cancel
work the learner already paid for. Finished jobs are bounded per owner, and a
job is readable only by the account that submitted it. Another learner gets
`404`, the same rule the stored records follow.

Every generation run also leaves an audit trail. The whole conversation, every
provider message, tool call, and tool result, is stored in `app.runs` and read
back with `GET /runs` and `GET /runs/{id}`. Three properties hold by
construction:

- The record is stamped with the session's account. Another account's run is
  `404`, never `403`.
- DSN credentials, `Authorization` headers, named key, secret, or token fields,
  vendor key prefixes, and the provider's own API key are removed before the
  value is stored. An entry that was scrubbed says so.
- A run stores at most 512 entries and 256 KiB, with one entry clipped at 8000
  characters. A run that hits a bound stores a prefix of itself and reports
  `truncated`.

Errors use stable codes: `EMPTY_QUERY`, `QUERY_SYNTAX_ERROR`,
`QUERY_RUNTIME_ERROR`, `QUERY_TIMEOUT`, `RESULT_LIMIT_EXCEEDED`,
`ENVIRONMENT_UNAVAILABLE`, `DIALECT_MISMATCH`, `PERSISTENCE_FAILED`,
`EVALUATION_ERROR`, plus `AUTH_REQUIRED`, `AUTH_INVALID_INPUT`,
`AUTH_INVALID_CREDENTIALS`, `AUTH_EMAIL_TAKEN`, and `AUTH_RATE_LIMITED` on the
auth surface. Error responses look like
`{"error":{"code":"...","message":"..."}}`, or carry the same error inside a
`QueryResult` for `/query` and `/answers/evaluate`.

## Accounts and sessions

Progress and events are owner-scoped. Each read and write names the account it
belongs to, and the gateway takes that id from the session, never from the
request body. Two accounts can hold independent snapshots for the same course,
and a body field cannot redirect a write onto another user's rows.

The SPA authenticates with the `qc_session` cookie. The cookie is `HttpOnly`,
`SameSite=Lax`, `Path=/`, and `Secure` when the request arrived over TLS. The
browser sends it on same-origin requests, so the page never stores the
credential where a script could read it. A mutating request that arrives with the
cookie is refused with `403 CSRF_REFUSED` unless its `Origin` or `Sec-Fetch-Site`
says same-origin. A request with neither header is a non-browser client and is
allowed. Cross-origin cookie auth is not supported, because `SameSite=Lax`
withholds the cookie cross-site and the gateway's CORS policy uses the wildcard
origin. The SPA must be served same-origin, which the single binary does.

API clients use the opaque bearer token that signup and login return in the
response body. Send it as `Authorization: Bearer <token>`. The header wins over
the cookie when both are present, and a present but invalid token does not fall
back to the cookie. The token is 256 bits from `crypto/rand`, stored in
`app.sessions` with a 30-day TTL. Logout deletes the session. Passwords use
bcrypt.

A fresh install seeds one administrator. The address is `admin_email`, default
`admin@querycraft.local`, and the default password is empty, so a local install
needs no setup. That account can sign in with a blank password on a loopback bind
only. The gateway refuses to start with an empty admin password when `addr` is
not loopback, so a network-exposed install must set `admin_password`. Public
signups always receive the `learner` role. Seeding is idempotent and never
rewrites the password of an existing account.

The public surface is `/health`, `/capabilities`, `/metrics`, `/query`,
`/explain`, `/answers/evaluate`, `/environment`, `/datasets/{id}/tables`,
`/openapi.json`, `/docs`, and the SPA. A missing or unknown session leaves those
alone. Every other route answers `401 AUTH_REQUIRED`.

### Sign-in throttling

`/auth/login` is the one endpoint where an anonymous caller can spend the
gateway's time guessing a secret. After `login_max_failures` failures for one
client address and one account inside `login_window_ms`, the next attempt is
refused with `429 AUTH_RATE_LIMITED` and a `Retry-After` header in whole
seconds. Below the limit the answer is the ordinary
`401 AUTH_INVALID_CREDENTIALS`. A successful sign-in clears the pair's failures.

The limit is in process and in memory. One mutex-guarded map is keyed by the
address and the normalized email, bounded to `login_max_tracked` entries. An
expired record is dropped on the way in. At the cap, the record closest to
expiry is evicted, so the map cannot grow without bound. It never stores the
attempted password.

The key is the socket address, never `X-Forwarded-For`. A caller-supplied header
would be a budget an attacker mints at will. The limit does not survive a
restart and is not shared between processes, so two gateways behind one address
hold a budget each. It is a speed bump on one source guessing one account, not a
shield against a botnet. A refusal says nothing about whether the account
exists.

A login limit set to `0` or below falls back to the documented default. It cannot
be used to disable the limit or leave the table unbounded.

Every response carries an `X-Request-Id`. A caller-supplied value is echoed
back. CORS is open (`Access-Control-Allow-Origin: *`), permits the
`Authorization` request header, and preflight `OPTIONS` returns `204`. The
wildcard origin cannot be combined with credentials, so a cross-origin page
cannot authenticate with the session cookie.

## Configuration

```mermaid
graph LR
    D["Shipped defaults<br/>internal/config.Defaults"] --> F["Config file<br/>--config, then ./querycraft.json"]
    F --> A["Command line<br/>--addr on serve"]
    A --> S["Settings<br/>one resolved value per key"]
    S --> R["run() in cmd/api<br/>wires the gateway"]
```

Every setting resolves through the three layers above. Each layer overrides the
one before it. A layer that says nothing about a key leaves the earlier value
standing.

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

The loader enforces these rules:

- The file is looked up through `--config`, then `querycraft.json` in the working
  directory. A named file that is missing is an error. The default location is
  optional.
- An unknown key, a wrong type, or a malformed file fails start-up, so a typo
  cannot silently do nothing.
- A duration such as `idle_ttl` or `sqlite_idle` is a string like `"30m"`. A bare
  number is refused, because its unit would be a guess.
- Secrets live in the file: `pg_password`, `admin_password`, `ai_key`, and the
  DSN keys. None of them is logged. The logs carry only `keySet` booleans.

### The CLI

The binary is a cobra CLI. `serve` is also the root command's behavior, so a bare
`querycraft` starts the gateway.

```sh
querycraft                    # start the gateway (same as serve)
querycraft serve --addr 127.0.0.1:9000
querycraft db reset           # delete the local cluster's data directory
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
| `addr` | `127.0.0.1:8080` | Listen address. A non-loopback bind requires `admin_password`. |
| `database_url` | unset, starts the local server | Learner execution DSN. Never an administrative connection. |
| `learner_database_url` | derived from `database_url` | Restricted-role DSN for learner statements. |
| `admin_database_url` | falls back to `database_url` | Metadata DSN for accounts, sessions, progress, events, challenges, courses, patches, and runs. |
| `metadata` | unset | Set to `sqlite` to store app metadata in SQLite. |
| `metadata_path` | derived from `pg_data` | File used when `metadata` is `sqlite`. |
| `admin_email` | `admin@querycraft.local` | Address for the seeded administrator. |
| `admin_password` | empty | Password for the seeded administrator. An empty value is allowed only on a loopback bind. Existing accounts are never reset. |
| `admin_name` | `Administrator` | Display name for the seeded administrator. |
| `no_local_db` | `false` | Never start the local server. |
| `pg_port` | `54329` | Port for the local PostgreSQL server. |
| `pg_data` | `.data/pg/data` | Cluster data directory. |
| `pg_runtime` | `.data/pg/runtime` | Where the extracted server binaries live. |
| `pg_binaries` | `.data/pg/binaries` | Download cache for the prebuilt binaries. |
| `pg_user`, `pg_password`, `pg_database` | `querycraft` | Credentials for the local server. |
| `max_rows` | `500` | Maximum rows returned per query. |
| `max_bytes` | `1048576` (1 MiB) | Maximum serialized result bytes per query. |
| `timeout_ms` | `5000` | Hard per-query timeout in milliseconds. |
| `max_concurrent` | `4` | Advertised maximum concurrent queries. |
| `sqlite_data` | derived from `pg_data` | Data root for the SQLite dialect: `<root>/accounts/<hash>.db` and `<root>/commerce.db`. |
| `sqlite_max_accounts` | `8` | Maximum per-account SQLite executors cached at once. An evicted account keeps its file and reopens on the next statement. |
| `sqlite_idle` | `15m` | Idle window for a cached SQLite executor, as a Go duration. |
| `login_max_failures` | `5` | Failed sign-ins one address may make against one account inside the window. |
| `login_window_ms` | `900000` (15 min) | Fixed window for counting a pair's failures. |
| `login_max_tracked` | `4096` | Upper bound on tracked address and account pairs. |
| `max_environments` | `8` | Maximum learner challenge environments open at once. Past the bound a new environment is refused with `ENVIRONMENT_UNAVAILABLE` until the idle reaper closes one. |
| `idle_ttl` | `15m` | Idle window for an open challenge environment. |
| `ai_provider` | `fake` | `fake`, `pi-ai`, `openai-compatible`, or `off`. |
| `ai_base_url` | empty | Base URL for a custom provider. |
| `ai_model` | empty | Model id or `vendor/model` ref. |
| `ai_key` | empty | Provider API key. |
| `ai_timeout_ms` | provider default | Per-call AI timeout. |
| `ai_max_output_bytes` | provider default | Maximum model output bytes. |
| `ai_run_timeout_ms` | derived from `ai_timeout_ms` | Whole-run budget. |

## The local database

`internal/localdb` makes the gateway self-hosting. When `database_url` is unset
and `no_local_db` is false, it:

1. Downloads the prebuilt PostgreSQL binaries once into `.data/pg/binaries`.
2. Runs `initdb` at `.data/pg/data` and starts the server on `pg_port`.
3. Applies `internal/dataset/schema.sql` and `seed.sql` when the dataset is
   absent.

Both paths are relative to the working directory the gateway starts from.
`make serve` runs from the repository root. `go run ./cmd/api` runs from
`backend/`. `querycraft db status` prints the resolved paths.

Everything above that package speaks the ordinary PostgreSQL wire protocol, so
pointing `database_url` at a container, a `postgres` service, or a managed server
replaces it with no code change. An external database also needs the dataset
applied once. [`../docs/self-hosting.md`](../docs/self-hosting.md) shows the two
`psql` commands.

### Lifecycle

The cluster's lifetime is tied to the gateway's:

- `SIGINT`, `SIGTERM`, and `SIGHUP` shut the gateway down gracefully and stop the
  server it started. `SIGHUP` matters because a closing terminal or an exiting
  parent shell sends it. Without a handler, the process would die instantly and
  orphan the postmaster.
- If a gateway is killed hard with `SIGKILL` and leaves the postmaster running,
  the next start adopts it instead of failing. It checks for `postmaster.pid` in
  the configured data directory, reconnects, and keeps serving. Only a server
  whose data directory proves ownership is adopted, so an unrelated PostgreSQL
  holding the port is never hijacked.
- An adopted server is not stopped on shutdown, because this process did not
  start it.

## Graceful degradation

The service runs usefully with no database configured.

- With no `database_url`, SQL execution uses an unavailable executor.
  `POST /query` and `POST /answers/evaluate` still respond, but every statement
  fails with `ENVIRONMENT_UNAVAILABLE`. `/health` still returns `200`.
- The other endpoints keep working: `/capabilities`, `/datasets/{id}/tables`,
  `/progress/{course}`, `/events`, `/courses/generate`, and
  `/challenges/generate`.
- With no `admin_database_url`, progress and events live in an in-memory store
  that is lost on restart, unless `metadata` is `sqlite`. The SQLite file stores
  app metadata only. Learner SQL still needs a PostgreSQL execution database.
- Agent-run transcripts live in the same store. With generation off, `/runs`
  answers `503` rather than inventing a transcript.

## Build, run, and test

Go is not assumed to be on `PATH`.

```sh
export PATH=$PATH:/usr/local/go/bin
cd backend

go build ./...
go vet ./...
go test ./...

# Run the gateway: defaults to :8080 and starts its own PostgreSQL.
go run ./cmd/api
```

The first `go run` downloads the server binaries and seeds the dataset. Later
runs answer immediately. To use an external server instead:

```sh
printf '{"database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable","admin_database_url":"postgres://querycraft:querycraft@localhost:5432/querycraft?sslmode=disable"}\n' > querycraft.json
go run ./cmd/api
```

`docker compose up -d db` starts an external PostgreSQL 16 if you prefer one
server for several worktrees. The default local flow never needs it.

The HTTP surface has a gated integration test that runs against a real
PostgreSQL database. It is skipped unless `QC_TEST_DATABASE_URL` is set, so the
default `go test ./...` needs no database:

```sh
QC_TEST_DATABASE_URL='postgres://querycraft:querycraft@localhost:5432/querycraft_test?sslmode=disable' \
  go test ./internal/httpapi -run TestGatewayIntegration -v
```

## The test fixture

Tests that need a real server use `internal/pgtest`. It starts the same embedded
PostgreSQL the gateway ships, so a test runs against the commerce dataset and the
restricted learner role with no Docker and no network.

| Entry point | Cluster | Database | Use it for |
| --- | --- | --- | --- |
| `pgtest.Shared(t)` | one per package test binary, started on first use | a private database per test, copied from the dataset template | the default. A test gets a virgin database, and two tests cannot meet. |
| `pgtest.Start(t)` | one per test | the test's own database | a test that stops and restarts its own server, or one that deliberately leaks cluster-global state. |

A package that calls `pgtest.Shared` declares one `TestMain`:

```go
func TestMain(m *testing.M) { pgtest.Main(m) }
```

`pgtest.Main` stops the package cluster after the tests and exits with their
code. A shared database is a copy of the dataset template, so a test that needs
the app metadata schema opens it through `store.NewPostgres`, exactly as
production does.

The first start pays for `initdb` and the dataset seed, then caches the result as
a golden cluster under `.data/pg/golden`. Every later start copies that cluster,
so a shared test binary pays the expensive half once.

A test binary killed with `SIGKILL` is the one cleanup `t.Cleanup` and
`pgtest.Main` cannot serve: the postmaster would survive as an orphan.
`startShared` leaves a detached watchdog behind for that case. The watchdog polls
the test binary's pid, then kills the postmaster from the data directory and
removes the cluster. It runs in its own process group, so a signal aimed at the
test run cannot take it down with the binary it is watching.
