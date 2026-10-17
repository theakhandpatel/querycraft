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
| `POST` | `/auth/login` | Exchange email + password for a session token. Unknown email and wrong password both return the same `401 AUTH_INVALID_CREDENTIALS`. Repeated failures for one address against one account return `429 AUTH_RATE_LIMITED`. |
| `POST` | `/auth/logout` | Revoke the bearer token. A revoked or absent token is refused with `401 AUTH_REQUIRED`. |
| `GET` | `/auth/me` | The account behind the bearer token. |
| `GET` | `/progress/{course}` | 🔒 Load the caller's progress snapshot for a course. |
| `PUT` | `/progress/{course}` | 🔒 Replace the caller's JSON progress snapshot for a course. |
| `POST` | `/events` | 🔒 Append an audit/attempt event; returns `202 Accepted`. |
| `POST` | `/courses/generate` | Generate a course skeleton (title, chapters, exercises, dataset version). Set `require_approval: true` to stop after the outline: the outcome is then `awaiting_approval` and `POST /courses/{id}/build` continues it. |
| `POST` | `/courses/{id}/build` | 🔒 Approve a paused course: continue its stored outline into the chapter phase. `404` for another account's course; `409` when the course is not still being built. Accepts `Prefer: respond-async` (or `async=1`) like the generate routes. |
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

Every generation run also leaves an **audit trail**: its whole conversation: 
every provider message, tool call and tool result, in order, is persisted to
`app.runs` and read back with `GET /runs` and `GET /runs/{id}`
(`contracts/run.schema.json`). The record is captured at the provider seam
(`internal/ai/transcript.go`), so the challenge pipeline and the M8 course agent
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
it belongs to, and that id always comes from the bearer token, never from the
request body. Two accounts can therefore hold independent snapshots for the same
course, and a body field cannot redirect a write onto another user's rows.

Transport is an opaque session token (`Authorization: Bearer <token>`), 256 bits
from `crypto/rand`, stored in `app.sessions` with a 30-day TTL. Revocation is a
`DELETE`, so logout is immediate; there is no signature key to leak. Passwords
are bcrypt (`golang.org/x/crypto/bcrypt`).

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
`/openapi.json`, `/docs`). A missing or unknown token leaves those alone; every
other route answers `401 AUTH_REQUIRED`.

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
`Authorization` request header, and preflight `OPTIONS` returns `204`.

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

## Simplification pass, 2026-10-16

This pass removed code a reader had to hold in their head. The behaviour, the
JSON error codes, the HTTP surface and the honesty rule did not move. The
command `cd backend && go build ./... && go vet ./... && go test ./...` is green
for all 13 packages, and `go test` boots real PostgreSQL through `internal/pgtest`.

### Measured shape

| Package | Before | After | Delta |
| --- | --- | --- | --- |
| `internal/httpapi` | 13276 | 13228 | -48 |
| `internal/ai` | 11157 | 11102 | -55 |
| `internal/engine` | 6235 | 6230 | -5 |
| `internal/store` | 3510 | 3490 | -20 |
| `internal/eval` | 1380 | 1374 | -6 |
| `internal/perf` | 832 | 817 | -15 |
| `internal/metrics` | 532 | 540 | +8 |
| `internal/webui` | 228 | 225 | -3 |
| `internal/dataset`, `internal/envdb`, `internal/dialect`, `internal/localdb`, `internal/auth`, `internal/pgtest`, `cmd/api` | unchanged | unchanged | 0 |
| **Total** | **43947** | **43803** | **-144** |

The pass removed 8 functions, 0 interfaces and 0 files. `internal/metrics` grew
by 8 lines because a per-call allocation fix needed a doc comment; the behaviour
is the same and the allocation is gone.

### What changed

Standard library over hand-rolled helpers. `store/memory.go` copied
`json.RawMessage` by hand and now calls `bytes.Clone`; its three `sort.Slice`
comparators became `slices.SortFunc` with `cmp`. `eval` and `perf` each carried a
private `contains`; both are `slices.Contains` now, and `perf` gained
`slices.ContainsFunc` for `anyNode`. `engine/explain.go` dropped a warning loop
and a unique-append loop for `slices.Contains`. `metrics` built a
`strings.Replacer` per rendered line and now holds two package-level replacers,
and it sorts series with `slices.SortFunc`.

Duplicated knowledge, kept once. `store/sqlite.go` had `marshalJSON` and a
`jsonText` wrapper over it; `jsonText` holds the body now. `engine/engine.go`
repeated the string `"postgresql"` in `EngineName` and `DialectPostgres`, so
`EngineName` is an alias. `ai/jobs.go` and `ai/transcript.go` held the same
random-id builder under two names, now one `newID`. Three `Provenance` literals
in the pipeline, the course pipeline and review became one `provenance()`
constructor. `httpapi` wrote the owner-scoped course fetch four times, now one
`ownedCourse` helper that states the 404-never-403 rule once.

Layers that only forwarded. `httpapi.trimCourse` renamed `strings.TrimSpace`, so
the two call sites call `strings.TrimSpace`. `ai.complete` repeated `completeRaw`
line for line, so it delegates now. `ai.timeout` repeated `agentTimeout`, so it
delegates now.

Unreachable and misnamed code. `httpapi.writeSQLiteTablesError` had a `qerr ==
nil` branch no caller could reach. `store/memory.go` and `store/postgres.go`
carried a `Close` doc comment stacked on `Kind`; each comment sits on its
function now. The field `ExerciseResult.FixturesPassed` held a rejected count
under a `fixtures_rejected` JSON tag, so it is `FixturesRejected`.

Type-erased and stringly dispatch. `store/postgres.go` read SQLSTATE `23505`
through an anonymous `interface{ SQLState() string }`; it asserts
`*pgconn.PgError` like `engine` does. `ai/exerciseIndexFromField` parsed digits
by hand and now uses `strconv.Atoi`. `ai.clampInt` hand-rolled bounds and now
uses the `min` and `max` builtins. `httpapi.asyncRequested` compared one query
value twice.

Arguments and guards that lied. `envdb.buildEnvironment` took `accountID` and
`key` and read neither. `webui.Available` repeated the broken-embed guard that
`distFS` already holds.

### Proposals, not implemented

These would change behaviour or the wire, or they need test edits the brief
forbids, so they stay as proposals.

- `store` repeats the owner-scoped list and scan shape six times across
  `memory.go`, `postgres.go` and `sqlite_depot.go`, and states the metadata
  schema twice. A generic list helper and one schema template would cut real
  lines, but both rewrite a live SQL path for no behaviour change. Worth a
  separate, measured change.
- `eval.multisetEqual` and `eval.compareRowsDetailed` share a row-matching rule
  but return different verdict shapes. `ai.exhausted` and the course refusal
  switch differ on `StageValidateArtifact`, which a test caught when I tried to
  merge them: the challenge path reports `AI_REPAIR_EXHAUSTED`, the course path
  reports `AI_INVALID_ARTIFACT`. I reverted that merge.
- `engine.PgxExecutor.ClassifyError` only forwards to `pgCodeFor`, and
  `classifyFor` restates the sentinel ladder `classify` already holds. For a
  `PgxExecutor` the two paths are provably identical, but tests assert the hook
  exists and the ladder documents the dialect boundary. Removing it is a test
  edit plus a design call.
- `envdb.ResetPerf` stores its reset time under the challenge key
  `digest(accountID)`, so a perf reset overwrites the challenge environment's
  reported reset time. Keying it per kind fixes a latent bug, and that is a
  behaviour change.
- `dataset.perfTablesResponse` reports the commerce column list for
  `perf.orders`, while `dataset/perf.sql` adds five columns, and
  `dataset/perf_test.go` asserts the wrong claim. This is a bug, not a refactor.
- `dialect` exports `Names`, `ByName`, `Affinity`, `AffinityClasses`,
  `ErrorCodes` and a package-level `ClassifyError` that only tests call. Moving
  them into the test file is safe in this module but would ripple if an external
  consumer imports them.
- `ai` still holds dead knobs: `Config.AdversarialFixtures` has no setter,
  `GenerationRequest.Steps` is validated but never rendered, `CodeAIAvailable`
  has no reference, and `validateCoursePlan` plus `FakeProvider.courseBundle`
  run only in tests. Removing production symbols because only tests call them
  means deleting tests, which the brief forbids.
- `httpapi` states the routes in four places (the mux, `requestErrorCode`, the
  `operations` map and `openapi.go`). They already disagree on `GET /docs/`. A
  drift test is the cheap fix; generating the OpenAPI document is a larger one.
- `engine/explain.go` classifies a plan node by the substring `"index"`. An
  explicit node-type set would tighten that, and tightening is a behaviour
  change.

### Not run

I did not run `make test` or `make verify`, because both build and lint `web/`,
which another agent edits in this tree. I ran the Go suite instead, which holds
the Go half of the fixture suite (`internal/eval`), the explain fixtures, and
the HTTP contract tests. The JavaScript audit suites in `scripts/` did not run.

Two files in `backend/internal/ai` (`prompt_test.go`, `review_test.go`) carried
uncommitted test additions before this pass. I did not author or edit them.

The package layout changed after this pass; see
[`docs/backend-decomposition.md`](../docs/backend-decomposition.md).
