# progress.md — live work tracker

Updated as work lands. Plan of record: `docs/m3-implementation-plan.md`
(M3 "Execution gateway"). Design source: `docs/technical-design.md` §13–§14.

Status legend: **done** (verified), **wip** (in flight), **todo**, **blocked**.

## Milestones

| Milestone | State | Evidence |
| --- | --- | --- |
| M1 — contracts + mock adapter | done | `contracts/`, `web/src/api/` |
| M2 — complete workbook (browser) | done, tagged `v0.2.0` | commit `2e7eac2be`, `make verify` green |
| M3 — execution gateway (Go + native PostgreSQL) | done, tagged `v0.3.0` | this file |
| M4 — generated challenges | todo | `docs/technical-design.md` §14 |
| M5 — SQL performance lab | todo | `docs/technical-design.md` §14 (plans, `EXPLAIN ANALYZE`) |
| M6 — second dialect: SQLite | todo | `docs/m6-implementation-plan.md` (plan of record); `docs/technical-design.md` §14 M6 + D12–D15; ADR `0004` records why PostgreSQL stays the default |

## M6 step board

Plan of record: `docs/m6-implementation-plan.md`. Steps 1–3 are the shared seam;
steps 4–5 are the backend track; steps 6–7 are the frontend track; step 8 joins
them. All state **todo**. D12 (browser engine) and D14 (learner isolation) are
settled by the step-5/step-6 spikes, not assumed.

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | Dialect in the contracts | shared | todo | `dialect` on QueryResult/exercise/curriculum; `dialects` on `/capabilities`; `DIALECT_MISMATCH` added to the closed error enum |
| 2 | SQLite dataset + checked mapping | shared | todo | `schema.sqlite.sql`, `seed.sqlite.sql`, `mapping.json`; `make audit-dataset-map` |
| 3 | Dialect profile: errors + value encoding | shared | todo | `contracts/dialects/sqlite.json` read by Go and TS; one test per error code per side; `docs/sqlite-dialect.md` |
| 4 | Gateway SQLite adapter | backend | todo | `modernc.org/sqlite` (pure Go), reuses `engine.Execute` caps; `make check-cgo` in `make test` |
| 5 | Per-learner SQLite isolation (D14) | backend | todo | file-per-account, reset by copy, mapped onto ADR `0003`; measured spike in ADR `0005` |
| 6 | Browser SQLite adapter (D12) | frontend | todo | wasm in a Worker; spike picks the library; same caps, deadline kill, honest capabilities |
| 7 | Dialect badge + actionable refusal | frontend | todo | chapter badge, disabled Run, one-action switch, draft preserved |
| 8 | Per-dialect parity audit | shared | todo | `make audit-parity` runs PostgreSQL and SQLite; `docs/adapter-parity.md` gains a SQLite section |
| 9 | M6 release | shared | todo | `make verify` + every `audit-*`; tag only with the owner's go-ahead |

## M3 step board

| # | Step | State | Notes |
| --- | --- | --- | --- |
| 1 | Metrics + diagnostics view | done | `GET /metrics`, `GET /diagnostics`, `make audit-metrics` (hermetic + live); Go tests |
| 2 | Structured request logs completed | done | one `withObservability` middleware: operation, route, duration_ms, status, error_code, request_id; credential redaction |
| 3 | Adversarial suite (§13.3) | done | `make audit-adversarial` (10 cases, green) + `docs/adversarial-suite.md` |
| 4 | Load test (§13.2) | done | `make audit-load` (320 requests, green) + `docs/load-test.md`, recorded numbers |
| 5 | Template environments (D11) | done | spike `dataset/template_test.go` (measured), ADR `docs/adr/0002-challenge-environments.md` |
| 6 | Exit-gate proof | done | 6.1 adapter parity **green: 61 compared / 61 match / 0 diverge** (`make audit-parity`, now inside `make test`; `docs/adapter-parity.md`); 6.2 no-admin-path Go test green; 6.3 clean-machine setup green |
| 7 | Writable learner workspaces (owner request) | done | ADR `0003`; verified end-to-end against the real binary (see "Step 7 notes") |
| 8 | M3 release | done | tagged `v0.3.0` (owner go-ahead given) |

### Step 1 checklist

| Item | State | Where |
| --- | --- | --- |
| Dependency-free metrics registry | done | `backend/internal/metrics/metrics.go` (+ `metrics_test.go`, green with `-race`) |
| `engine.Executor` gains `Stats()` / `Ready()` + `Diagnostician` | done | `engine/engine.go`, `pgx.go`, `unavailable.go` |
| Store reports `Kind()` (postgres vs memory) | done | `store/store.go`, `memory.go`, `postgres.go` |
| Metrics + logs middleware (single recorder) | done | `httpapi/metrics.go` `withObservability` |
| `GET /metrics` (Prometheus text) | done | declared names, pool/ready gauges sampled per scrape |
| `GET /diagnostics` (readiness, versions, pool, sandbox) | done | learner facts read over the restricted connection |
| Enforce `MaxConcurrent` inside the deadline | done | semaphore in `enter`; `QUERY_TIMEOUT` on saturation |
| OpenAPI doc + `openAPIOperations` cover the new routes | done | `openapi.go`, `openapi_test.go` |
| `scripts/metrics-audit.mjs` + `make audit-metrics` | done | in `make test`; `make audit-metrics-live` for the live half |
| Fakes updated for the wider `Executor` interface | done | `engine_test.go`, `eval_test.go`, `server_test.go` |
| Credential redaction (`engine.RedactSecrets`) | done | `engine/redact.go`; applied in `NewError`, `Ready`, diagnostics |

## Verified green (last full run)

- `make test`: frontend typecheck + 9 audit suites + all Go packages — **green**
  (includes `audit-parity`: 61 compared / 61 match / 0 diverge / 1 volatile exclusion).
- `make verify`: build + `make test` + a cold-boot smoke test on an isolated embedded
  PostgreSQL — `/health` → PostgreSQL 18.3, `/query` 200, anonymous `/progress` 401,
  admin login 200, learner signup 201, scoped progress PUT/GET 200, revoked token 401,
  SPA served — **green**.
- `make verify-mock`: honest degraded mode (`/query` → 400 `ENVIRONMENT_UNAVAILABLE`) — green.
- Owner-driven UI pass against the built binary (real PostgreSQL, gateway mode): sign-in
  screen in gateway mode (not the offline "Local workspace"), header `gateway · live`,
  lesson query ran against the engine (`… postgresql`, request id), exercise graded
  (wrong → "Not yet", correct → "passed" + Next chapter), progress survived a reload,
  tables drawer listed the live schema and reset the workspace (222 ms), Workbook /
  Progress / Challenge lab / Library / Appearance / Create-a-course all rendered.

### Step 1–2 notes

- The metrics registry is deliberately dependency-free (`backend/internal/metrics/`);
  a client library would be more code than the thing it measures.
- `MaxConcurrent` had been advertised in `/capabilities` and never enforced. It now is:
  a semaphore acquired inside the request's own deadline. Waiting spends the same budget
  the query would have, so saturation reports `QUERY_TIMEOUT` — an existing code in the
  closed contract enum — rather than inventing one or queueing unboundedly.
- Two middleware layers (logging and metrics) would each hold their own status and error
  code; they were merged into one so the log line and the counter cannot disagree.

### Step 6 notes (exit-gate proof)

- **6.2 No path runs learner SQL on an administrative connection — proven by behaviour.**
  `backend/internal/httpapi/adminpath_test.go` boots its own embedded cluster, builds a
  real executor on the *learner* DSN, and drives `POST /query`: `current_user` and
  `session_user` both answer `querycraft_learner`; `app.progress`, `public.progress`,
  `pg_read_file`, `pg_ls_dir`, `COPY … TO PROGRAM`, `CREATE ROLE`, `CREATE DATABASE`,
  `ALTER SYSTEM`, `DROP TABLE dataset.orders`, `TRUNCATE`, `SET ROLE` and `pg_authid`
  are all refused with `QUERY_RUNTIME_ERROR` (HTTP 400, never 5xx); the dataset counts and
  the admin-side sentinel are unchanged afterwards. `RESET ROLE` is a documented no-op on
  a learner session (it resets the *current* role to the session user, which is already the
  learner) and the test asserts that instead of a fake error.
- **6.3 Setup works on a clean machine from the documented command — measured.**
  The built binary was started with an empty data directory *and* an empty binaries cache:
  it downloaded PostgreSQL 18.3 once (60 MiB), ran `initdb`, applied schema + seed and
  served `/health` (`"version":"PostgreSQL 18.3"`), `POST /query`
  (`{"status":"succeeded",…"rows":[[80]],…}`), `/` (SPA, 200) and `/docs` (200). The
  documented flow is recorded in `README.md` §"Verified from nothing on a clean machine".
- **6.1 Adapter parity** is `scripts/adapter-parity-audit.mjs`, extended into a real
  instrument: it reports *which* side produced *what* for each mismatch, excludes the
  genuinely volatile statements (`EXPLAIN`, `EXPLAIN ANALYZE`, `random()`, `now()`,
  `pg_sleep`) with a printed reason each unless `--include-volatile` asks for them, and
  runs the naive-`timestamp` case under both a UTC and a non-UTC `TZ` so the host-zone
  shift can be watched appear and disappear.
- **Three real divergences the audit found, all now fixed in the product, not the test.**
  (a) The gateway emitted `numeric` as a JSON number, so `118.00` arrived as `118` —
  the learner's own price lost its scale; `internal/engine/normalize.go` now returns
  PostgreSQL's text for it. (b) A naive `timestamp` was decoded into a time value and
  re-rendered, which moves the wall-clock time by the host's UTC offset (measured:
  `10:30` → `05:00` under `Asia/Calcutta`); the same text rule fixes it, and the browser
  adapter now asks PGlite for the same text via per-OID parsers instead of letting it
  materialise a JS `Date`. (c) The gateway called a missing relation a *syntax* error
  (`42P01`/`42703` were matched by the `42` class prefix) where design §4.2 files
  "missing relation" under `QUERY_RUNTIME_ERROR` — and the browser adapter already said
  runtime, so the same mistake was described two ways; `classify` now shares one table
  with `web/src/api/pglite.ts`, pinned on both sides by tests. The canonical type-name
  spelling is PostgreSQL's own `pg_type.typname` (`int4`, `int8`, `bool`, `float8` …),
  which is what §4.2's example uses.
- The Go half is proven by `internal/engine/encoding_test.go`, which runs against a real
  server (the default test timezone here is `Asia/Calcutta`, the one that exposed the
  bug) and asserts the exact text for `date`/`time`/`timestamp`/`interval`/`numeric`,
  that `int4`/`float8`/`bool` stay JSON scalars, that `timestamptz` equals the server's
  own `::text`, and that the serialized body contains `"118.00"` and no `2024-01-15T…`.
  The cross-adapter number lands here when the audit is green end to end.

### Step 7 notes (writable learner workspaces)

The owner's objection is correct and is now a milestone: **a SQL course that cannot
write cannot teach SQL.** Both adapters wrapped learner SQL in a read-only transaction
and the learner role held a `SELECT`-only grant, so `INSERT`, `UPDATE`, `DELETE`, DDL and
transactions were untouchable — and the curriculum contains *zero* DML lessons, which is
the symptom rather than the fix.

Settled in `docs/adr/0003-writable-learner-workspaces.md`:

- Learner SQL is writable. Signed-in learners get their **own environment database**,
  copied from the sealed template (ADR 0002) and granted `SELECT, INSERT, UPDATE, DELETE`
  plus `USAGE, CREATE` on `dataset` — no `TRUNCATE`, no ownership, no `REFERENCES`, no
  `TRIGGER`.
- Reset is DROP + copy-from-template (~35 ms create, ~25 ms drop per ADR 0002), exposed as
  `POST /environment/reset`, with `GET /environment` for facts.
- **Anonymous stays read-only** on the shared dataset: no stable identity to hang an
  environment on, and a cookie-keyed environment would make every cookie-less client a
  `CREATE DATABASE`.
- The shared `dataset` schema is never writable in place; the gateway's own database keeps
  the `SELECT`-only grant, so "the learner cannot INSERT" becomes "the learner cannot
  INSERT into the shared dataset, and cannot reach another learner's environment".

Landed: the Go library layer (`dataset` writable template grants, `engine` writable mode
with commit-on-drain, `internal/envdb` per-account manager), the HTTP surface
(`GET /environment`, `POST /environment/reset`, wired into `cmd/api` with a reap loop and
prometheus counters), and the web layer (PGlite writable + `resetEnvironment()`, the
`ExecutionService` seam, and a two-step-confirm reset control in the tables drawer that
cancels on Escape and re-focuses the idle button).

**Verified against a real binary, not just unit tests** (built `./cmd/api`, its own
cluster on `:19011`/`:19012`, 2026-10-14):

| Step | Observed |
| --- | --- |
| anonymous write | `UPDATE products SET stock = 0` → 400 `QUERY_RUNTIME_ERROR` "cannot execute UPDATE in a read-only transaction" |
| anonymous `GET /environment` | `{"ok":true,"environment":{"exists":false,"writable":false}}` — it does not invent a workspace |
| signup → `GET /environment` | lazily created `qc_env_acct_1ed246…`, `exists:true`, `writable:true`, `tables:5`; gateway log `challenge environment ready … created_in=7.0ms` |
| learner write | `UPDATE products SET stock = 0` → `succeeded`; read back `[[1,0],[2,0],[3,0]]` |
| reset | `POST /environment/reset` → `{ok:true, duration_ms:401}`; read back `[[1,40],[2,25],[3,15]]` — the sealed dataset |
| DDL in the workspace | `CREATE TABLE scratch(id int)` → `succeeded`; the same statement anonymously → refused |
| OpenAPI | `/environment` and `/environment/reset` both present |

Both `POST /query` and `POST /answers/evaluate` route through the same
`statementExecutor`, so grading also runs in the learner's own workspace rather than the
shared dataset. Content for DML/DDL/transactions follows once the course is written, so
the lessons actually teach what the engine now allows.

### Step 8 notes (release)

Everything the release needs is green; only the tag waits on the owner's
go-ahead (`docs/m3-implementation-plan.md` step 8 holds the checklist):

| Check | Evidence |
| --- | --- |
| `make verify` (build + typecheck + every audit + all Go tests + cold-boot smoke) | green, `/health` → PostgreSQL 18.3 |
| `make verify-mock` (honest degradation) | green — `/query` 400 `ENVIRONMENT_UNAVAILABLE`, auth still enforced |
| Adapter parity | 61 compared / 61 match / 0 diverge (`docs/adapter-parity.md`) |
| `make audit-load` | green — concurrency cap proven, pool cross-checked against `/metrics` |
| `make audit-metrics-live` | green — deadline stop → `QUERY_TIMEOUT`, row cap → `truncated` at exactly the cap, both counted |
| `make audit-adversarial-live` | green — ten §13.3 cases, dataset intact after the suite |
| `make audit-courses` (live gateway) | green — **every authored solution in all four courses** runs on the gateway and grades against itself |
| `make audit-load-live` | green — concurrency cap enforced on a running gateway, pool bounded, healthy afterwards |
| Owner UI pass on the built binary | gateway mode, real engine queries, grading, retake, drawer + reset, authoring |
| Version bump + tag `v0.3.0` | **done** — `web/package.json` `0.3.0`, tag created with the owner's go-ahead |

### Step 3 notes (adversarial suite)

- `scripts/lib/gateway.mjs` is the shared harness for every live audit: `freePort`,
  `buildGateway`, `startGateway`/`stopGateway` (kill by PID *and* process group, never
  `pkill`), `liveEnv`, `getJSON`/`getText`/`postJSON`, `seriesValue` (subset label match)
  and `Reporter`. `scripts/adversarial-audit.mjs` and `scripts/load-test.mjs` are both
  built on it; there is one way to boot and stop a gateway, not three.
- Three real defects the suite found and that are now fixed: SQLSTATE `42501`
  (permission denied) was reported as `QUERY_SYNTAX_ERROR` and is now
  `QUERY_RUNTIME_ERROR`; a pool-acquire deadline was reported as
  `ENVIRONMENT_UNAVAILABLE` and is now `QUERY_TIMEOUT` with a message naming the wait;
  and hitting the row cap did not cancel the server, so `generate_series(1,1e9)` drained
  for 4003 ms holding a connection — it now cancels at the cap and returns in 4 ms.
- `docs/adversarial-suite.md` publishes the table **and** a "What is not protected"
  section: the learner can enumerate databases via `pg_database`, read role names,
  read operational settings (host paths are hidden), and read `pg_stat_activity`; the
  suite is not a resource-exhaustion proof and does no protocol fuzzing.

### Step 4 notes (load test)

- `scripts/load-test.mjs` builds and boots its own gateway (embedded PostgreSQL on a
  free port), drives N workers against `POST /query` mixing a real three-table
  join/aggregate with a statement that must hit its deadline, and reports the
  client-observed numbers *next to* the gateway's own `/metrics` and `/diagnostics` so
  the two can be compared rather than trusted.
- Recorded run (2026-10-14, i5-1135G7, 8 vCPU, WSL2, PostgreSQL 18.3, 8 workers × 40
  requests, `max_concurrent=4`): 320 requests in 1601.8 ms = **199.8 rps**; overall
  p50 16.3 ms / p95 123.3 ms / p99 130.7 ms; analytic p50 15.2 ms / p99 59.3 ms;
  288×200 and 32×400, every 400 a `QUERY_TIMEOUT`. `requests_total` delta and the
  latency-histogram count delta both equal 320, so the server's tally matches the
  client's.
- The concurrency cap is proven by a deterministic saturation probe, not by "nobody was
  refused": fill every slot with an 800 ms sleep, then two requests with a 120 ms
  deadline must be refused with the cap named in the message, and the slot-holders must
  still succeed. `docs/load-test.md` says plainly that the numbers are loopback, single
  host, ~1.6 s, and not a capacity plan.

### Step 5 notes (D11 — template environments)

- Decided: **one database per challenge environment**, copied from a sealed template
  (`ALTER DATABASE qc_template IS_TEMPLATE true ALLOW_CONNECTIONS false`). A schema cannot
  be copied; a shared schema with row scoping loses to a learner typing `SELECT * FROM orders`.
- New, additive code only: `backend/internal/dataset/template.go` (create / load / seal /
  drop / reset / reap / size / connection facts) and `backend/internal/dataset/template_test.go`
  (the spike). Nothing was wired into the request path; `engine/`, `httpapi/`, `Makefile` and
  `web/` are untouched.
- Measured on this machine, PostgreSQL 18.3, 2026-10-14: template build 57–71 ms + 55–63 ms;
  **copy median 34–44 ms** (first copy has no cold penalty); burst of 8 = 353–457 ms;
  **drop median 24–29 ms but the first drop pays a ~2.9 s checkpoint**; FORCE drop with a live
  session 128–133 ms; reset 164–228 ms; **7.6 MiB per environment**; grants, rows and the
  cluster-wide `search_path=dataset` all travel with the copy.
- Connection budget: `max_connections=100`, 97 usable by a non-superuser role (measured 96);
  one environment costs 0 slots at rest, 1 idle / up to 8 busy with the current pool default.
- Orphans survive a restart and nothing reaps them today; `ReapEnvironmentDatabases` is the
  M4 hook. The learner role can also CONNECT to the metadata database (PUBLIC default) — M4
  should revoke it.

## Known defects carried into M3

- `Config.MaxConcurrent` was advertised in `/capabilities` but never enforced. Fixed in
  Step 1 by a semaphore acquired inside the request deadline (`QUERY_TIMEOUT` on saturation,
  which reuses the existing closed error-code set).
- `/capabilities`, `/health` and (new) `/metrics`/`/diagnostics` are unauthenticated by
  design; none may ever print a DSN, password or hash. Checked in the adversarial suite.

## Defects found by driving the app (owner request, M3)

- **"Create a course" renamed the built-in course instead of creating one. Fixed & verified.**
  The authoring stub (`store.submitBuild`) wrote `COURSES[0].title = …`, so one run destroyed
  "SQL Analytics in the Real World" for the session, and `startLearning` then enrolled that
  same built-in course — the "new" course showed the old course's blurb and 11% progress.
  Found by the owner-driven UI pass (Create → review → "Add to my learning" → Library).
  Now `submitBuild` pushes a distinct course (`created-<epochms>`) with a blurb derived from
  the learner's goal (`Built from your goal: …`) and its own `modules` whose module and
  exercise ids carry a `@created-<epochms>` suffix, so progress and attempts cannot leak
  (`web/src/state/store.tsx` `draftModulesFor`/`blurbFromGoal`; `draftCourse` is exposed and
  `BuildingScreen`/`ReviewScreen`/`startLearning` use it). Verified against the fresh binary:
  the draft shows **0%** with its own blurb, and "SQL Analytics in the Real World" still
  shows **11%** with its original blurb; `npx tsc --noEmit` clean, `make verify` green.
- **`TablesDrawer` kept the last reset note across reopen. Fixed & verified.** The last
  `resetNote` ("Workspace restored in … ms.") survived a close/reopen, so a stale duration
  appeared beside a drawer that had just opened and reset nothing. The drawer now clears
  `resetNote`/`resetError` when it opens (`web/src/components/TablesDrawer.tsx`). Verified in
  the browser: after a reset the footer shows "Workspace restored in 368 ms."; after
  close/reopen the note is gone while the workspace line stays.

## Constraints (do not violate)

- Greenfield: no migrations, no backward compatibility, no backfill.
- No Docker needed locally; embedded PostgreSQL is the default.
- Preserve the visual design and copy — this is a port, not a redesign.
- All frontend data access goes through `ExecutionService` (`web/src/api/`); never `fetch`
  from a screen, never fabricate query results in the UI.
- The gateway stays honest with no database: `ENVIRONMENT_UNAVAILABLE`, never invented rows.
- TypeScript `strict`; `web` must stay clean under `npx tsc --noEmit`.
- Go over Bun is settled (`docs/adr/0001-backend-runtime-go-not-bun.md`).
- Do not commit or tag without asking.
