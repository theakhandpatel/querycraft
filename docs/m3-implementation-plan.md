# M3 implementation plan: "Execution gateway (Go and native PostgreSQL)"

Design source: `docs/technical-design.md` §14 (M3), §5.4 (Adapter B), §12
(security), §13.1 (observability), §13.2 (test matrix), §13.3 (adversarial
suite). Steps are ordered so each ships on its own and leaves `make verify`
green.

## What M3 is

> Scope: Adapter B behind the same contracts, restricted role, limits, streaming
> caps, cancellation, template environments, Postgres metadata database, Go
> evaluator, adversarial suite, load test, metrics, `docker compose up` setup.
>
> Exit gate:
> - The Go evaluator passes the shared fixture suite.
> - The adversarial suite passes with bounded outcomes and a healthy service afterwards.
> - No path runs learner SQL on an administrative connection.
> - The same lesson content runs against both adapters unchanged.
> - Setup works on a clean machine from the documented command.

## Where we are today (measured, not assumed)

Most of Adapter B already exists: M1/M2 built it because `make verify` needed a
real server to smoke-test against. The gaps are observability and proof, not
plumbing.

| M3 scope item | State | Evidence |
| --- | --- | --- |
| Adapter B behind the same contracts | Done | `backend/internal/engine/`, `backend/internal/httpapi/`; `make verify` drives `/query` on embedded PostgreSQL |
| Restricted role | Done | `dataset/roles.go`: `querycraft_learner`, `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS`, `temp_file_limit=64MB`, `search_path=dataset`; `TestLearnerRoleCannotEscapeTheSandbox` |
| Limits | Done | `engine/engine.go` `Limits{MaxRows:500, MaxBytes, TimeoutMS}` + `Merge` clamping; `execute.go` truncates while reading |
| Streaming caps | Done | caps applied row-by-row during `rows.Next()`, not after materialising |
| Cancellation | Done | context deadline → `SET LOCAL statement_timeout`; `context.Canceled`/`DeadlineExceeded` → `QUERY_TIMEOUT` |
| Postgres metadata database | Done | `store/postgres.go` (`app` schema), `localdb/` boots an embedded cluster when no DSN is set |
| Go evaluator | Done | `eval/` + `fixtures_test.go` over the shared `contracts/fixtures/*.json` (44) |
| `docker compose up` setup | Done | `docker-compose.yml`, `deploy/postgres/initdb/` (one server, database per environment, `dataset` + `app` schemas) |
| Structured logs | Partial | `slog` with `request_id`; operation/duration/status/error_code not yet all fields |
| Metrics (§13.1) | **Missing** | no metrics registry, no `/metrics` |
| Diagnostics view (§13.1) | **Missing** | `/health` only; no engine readiness/versions/migration view |
| Adversarial suite (§13.3) | **Missing** | unit tests cover timeout/cancel only; no end-to-end suite, no published results |
| Load test (§13.2) | **Missing** | no concurrency test, no pool metrics recorded |
| Template environments (D11) | **Missing** | one shared `dataset` schema; no per-challenge database |

## Sequencing

| # | Step | Ships | Depends on |
| --- | --- | --- | --- |
| 1 | Metrics + diagnostics view | `GET /metrics`, `GET /diagnostics`; `make audit-metrics` | (none) |
| 2 | Structured request logs completed | every request logs operation, duration, status, error code | 1 |
| 3 | Adversarial suite (§13.3) | `make audit-adversarial` + `docs/adversarial-suite.md` | 1 |
| 4 | Load test (§13.2) | `make audit-load` + `docs/load-test.md` with recorded numbers | 1 |
| 5 | Template environments (D11) | per-challenge database from a template; ADR 0002 | 3 |
| 6 | Exit-gate proof | `make audit-parity` (both adapters, one statement at a time, per dialect later); no admin path; clean-machine setup | 1–5 |
| 7 | Writable learner workspaces (owner request) | ADR 0003; per-account environment database from the sealed template; `GET /environment`, `POST /environment/reset`; PGlite `resetEnvironment()` and a confirming reset control | 5 |
| 8 | M3 release | tag `v0.3.0`, README/plan updated | all |

Step 1 comes first because steps 3 and 4 publish numbers that must come from the
instrumentation, not from a stopwatch. Step 5 is last of the engineering because
nothing in M1–M2 needs it and M4 does (a throwaway environment per generated
challenge). Step 7 is not in the original scope; it was added because the
owner pointed out that a SQL course which cannot teach `INSERT` cannot teach
SQL. It sits after step 5 because it is a second consumer of the template
mechanism, and before the release because a release that cannot teach writing
would not be the product.

---

## Step 1: Metrics and the diagnostics view (§13.1)

The design asks for counters for query duration, timeouts, caps hit, pool usage,
environment create/reset time, evaluator errors and persistence errors, and a
diagnostics view for engine readiness, versions and migration state.

1. `backend/internal/metrics/`: a small registry (counter, gauge, histogram) with
   a Prometheus text exposition. No dependency: the surface is ~120 lines.
2. Instrument at the seams that own the event: `engine/execute.go` (duration,
   timeouts, row/byte cap hits), `store/` (persistence errors), `eval/`
   (evaluator errors), `localdb/` (cluster start, schema/role ensure time),
   `httpapi` (requests by route and status).
3. `GET /metrics`: Prometheus text. `GET /diagnostics`: JSON:
   engine readiness, PostgreSQL version, dataset version, progress version,
   learner-role present, migration state, pool stats.
4. `scripts/metrics-audit.mjs` + `make audit-metrics`: boot the gateway on an
   isolated port, run a query that times out and one that trips the row cap,
   then assert `/metrics` shows both and `/diagnostics` reports ready.

**Done when:** `make audit-metrics` is green and `/diagnostics` names the version
of every moving part.

## Step 2: Structured logging, completed

`slog` already carries `request_id`. Add `operation`, `duration_ms`, `status`
and `error_code` to the request log line, and make sure no DSN or password can
reach it (`docs/technical-design.md` §13.1: no secrets or connection strings).

**Done when:** a Go test asserts the field set for a successful and a failing
request, and a test asserts a DSN-like string is redacted.

## Step 3: The adversarial suite (§13.3)

The ten named cases, each ending in a bounded reported outcome with a healthy
service afterwards:

1. `pg_sleep` beyond the deadline
2. very large `generate_series`
3. infinite recursive CTE
4. cross-join explosion
5. `DO` block with a loop
6. attempts to read the metadata database or another environment
7. `COPY … PROGRAM`, server file access, `CREATE EXTENSION`, role creation
8. very large single value in a result
9. connection exhaustion by concurrent requests
10. reset while a query is running

`backend/internal/adversarial/` as a Go test that drives a live gateway built
for the run (its own port and cluster, like `make verify`), asserting for each
case: a bounded outcome, an error code the client can act on, and `/health`
still ok afterwards. `make audit-adversarial` wraps it.

**Done when:** `make audit-adversarial` is green and `docs/adversarial-suite.md`
publishes the table, *including what is not protected*.

## Step 4: Load test (§13.2)

Concurrent `/query` load with timeouts and pool metrics recorded.

`make audit-load`: N concurrent clients (default 32) for a fixed number of
requests, mixing a fast query with a query that hits the deadline; report
p50/p95/p99, timeout count, cap hits and pool saturation from `/metrics`.
Record the result and the machine in `docs/load-test.md`.

**Done when:** the numbers are recorded, the service is healthy after, and the
doc says plainly what the test does *not* prove (it is loopback, single host,
not a capacity plan).

## Step 5: Template environments (D11)

Settle §15 D11: *database or schema per challenge*.

Spike: `CREATE DATABASE challenge_<id> TEMPLATE querycraft_template`, one
throwaway database per generated challenge, dropped on delete; compare against
"schema per challenge" on isolation, create time and pool cost. Write
`docs/adr/0002-challenge-environments.md` with the decision and the measured
create/drop times from `/metrics`.

**Done when:** the ADR records a decision with numbers, and a Go test proves a
learner statement in one environment cannot see another's tables.

## Step 6: Exit-gate proof

1. **Both adapters unchanged**: run one lesson's query text through Adapter A
   (PGlite) and Adapter B and assert the same `QueryResult` shape and rows: 
   `scripts/adapter-parity-audit.mjs`, `make audit-parity`.
2. **No admin path**: a Go test that greps the execution path for the admin pool,
or better, one that proves it by behaviour: the learner session cannot
   `SET ROLE`, cannot `SELECT` from `app.progress`, cannot read `pg_read_file`.
   Extend `TestLearnerRoleCannotEscapeTheSandbox` to cover the admin-DSN path
   end-to-end through `POST /query`.
3. **Clean-machine setup**: follow the README command on a fresh cluster
   (`make db-reset` then `make serve`) and record it in `docs/technical-design.md`
   §0 or the README.

## Step 7: Writable learner workspaces (owner request)

A course that cannot teach `INSERT`/`UPDATE`/`DELETE`, DDL or transactions is not
a course, so the owner asked for writable learner workspaces. Settled by ADR
`docs/adr/0003-writable-learner-workspaces.md`: one database per account, copied
from the sealed `qc_template` (the step-5 mechanism), reset or dropped on demand.
Anonymous visitors keep the shared read-only dataset: no stable identity, no
private workspace.

Ships `GET /environment`, `POST /environment/reset`, `internal/envdb` (manager +
reap loop, wired in `cmd/api/main.go`), the `TablesDrawer` reset control, and the
PGlite `resetEnvironment()` equivalent for the offline adapter.

**Done when:** a signed-in learner can `INSERT`/`UPDATE`/`DELETE`/DDL/`BEGIN`,
an anonymous caller is refused with `QUERY_RUNTIME_ERROR` naming the read-only
transaction, `POST /environment/reset` restores the seeded counts, and the build
stays green (`make test`, `make verify`).

## Step 8: M3 release

Release checklist: the owner's go-ahead is required before the tag (the "do not
commit or tag without asking" constraint holds):

1. `make verify` green (it runs `make test`: typecheck + every audit + all Go
   tests) and `make verify-mock` green.
2. The live-only suites green against a booted gateway: `make audit-load`,
   `make audit-metrics-live`, `make audit-adversarial-live`.
3. Docs consistent: README (gateway, writable workspaces, environment variables),
   `progress.md` (M3 board all `done`), this plan, `docs/technical-design.md` §14 M3.
4. Version: `web/package.json` `0.2.0` → `0.3.0`.
5. Commit the M3 work, then tag `v0.3.0`.
6. Flip the M3 row in `progress.md` to `done, tagged v0.3.0` and mark the design
   §14 M3 section shipped.

**M3 exit gate, mapped:** Go evaluator → already green (`fixtures_test.go`);
adversarial suite → step 3; no admin connection → step 6.2; both adapters →
step 6.1; clean-machine setup → step 6.3; writable workspaces → step 7.
