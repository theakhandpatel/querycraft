# M6 implementation plan: "Second dialect: SQLite"

Design source: `docs/technical-design.md` §14 (M6), §15 D12–D15 (settled by the
M6 spikes); ADR `docs/adr/0004-postgresql-not-sqlite.md` (why PostgreSQL stays
the default dialect and why this is a second dialect, not a replacement); ADR
`docs/adr/0003-writable-learner-workspaces.md` (the per-account environment and
its reset, which the SQLite isolation in step 5 must map onto).

Steps are ordered so the shared seam lands first, then the two tracks proceed in
parallel, **Track B** (Go gateway) and **Track F** (browser), and meet at one
per-dialect parity audit. PostgreSQL remains the default dialect throughout; no
existing lesson, contract or adapter changes behaviour in M6.

## What M6 is

> Scope: SQLite as a **second dialect**, alongside PostgreSQL, not a replacement for it
> (ADR 0004 explains why the app is PostgreSQL-first). The learner can take a course that
> teaches SQLite, run it on a real SQLite engine in both adapters, and be graded the same
> way. This is the milestone that turns "SQL" from one engine into a dialect dimension.
>
> Concretely:
>
> - A **dialect** concept in the contracts: a course/chapter declares its dialect, and
>   `QueryResult.engine` names the engine that produced a result.
> - Adapter A: SQLite compiled to WebAssembly in a Worker, mirroring the PGlite adapter
>   (same deadline kill, same row/byte caps, same honest capability reporting).
> - Adapter B: SQLite behind the same `POST /query` contract, so the gateway serves both.
> - A second dataset authored for SQLite (schema + seed), with a checked mapping to the
>   commerce dataset so the two dialects answer the same questions about the same shape.
> - **Dialect-aware grading**: SQLite has no SQLSTATE, dynamic typing, `LIKE` is
>   case-insensitive for ASCII, and dates are not types. Error classification and value
>   encoding must therefore be defined per dialect, not assumed from PostgreSQL.
> - Courses that teach what is *different*: SQLite vs PostgreSQL types and affinity,
>   `AUTOINCREMENT` vs `GENERATED`/`IDENTITY`, `PRAGMA` vs `SET`, `rowid`, when SQLite is
>   the right choice, and what it cannot do (no roles, no `EXPLAIN ANALYZE`, no schemas).

### Exit gate (verbatim from §14 M6)

> - A learner cannot accidentally run a SQLite chapter against PostgreSQL, or the reverse,
>   without the UI saying which dialect they are on, and the refusal is actionable.
> - Both dialects run the same *shape* of question (SELECT, JOIN, GROUP BY, window, CTE,
>   recursive CTE) and pass the same result contracts.
> - Error classification is dialect-specific and documented, with a test per code.
> - The adapter-parity audit runs **once per dialect** (PostgreSQL: PGlite vs gateway;
>   SQLite: wasm vs gateway), and both are green.
> - The single binary stays cgo-free and Docker-free.

### The gate, operationalized

| Gate clause | What proves it | Step |
| --- | --- | --- |
| Dialect refusal is actionable | UI badge + disabled Run + "switch engine" action; server backstop returns `DIALECT_MISMATCH` naming the accepted dialect | 1, 6, 7 |
| Same *shape* of question, same contracts | `contracts/fixtures/` cases tagged by dialect; both evaluators pass the union; the six named shapes each have a PostgreSQL and a SQLite case | 2, 8 |
| Dialect-specific error classification, test per code | One shared table in `contracts/dialects/`; a Go table test and a Node audit read the same file, one case per code per side | 3 |
| Parity runs once per dialect, both green | `make audit-parity` runs both; `docs/adapter-parity.md` grows a SQLite section with its own tables | 8 |
| Single binary stays cgo-free and Docker-free | `make check-cgo` (`CGO_ENABLED=0 go build ./...` + a dependency scan) in `make test`; SQLite is `modernc.org/sqlite`, pure Go | 4 |

## Where we are today (measured, not assumed)

The dialect seam does not exist yet. Everything below is the current state.

| M6 scope item | State | Evidence |
| --- | --- | --- |
| A dialect concept in the contracts | **Missing** | `contracts/query-result.schema.json` has `engine` but no `dialect`; `contracts/exercise.schema.json`'s `setup` has `dataset`/`schema_version`/`seed_version` but no dialect; `web/src/data/curriculum.ts` has no dialect field |
| SQLite on the gateway (Adapter B) | **Missing** | one executor, `backend/internal/engine/pgx.go`; `engine.EngineName = "postgresql"`; `/health` hard-codes `"engine": "postgresql"` |
| SQLite in the browser (Adapter A) | **Missing** | `web/src/api/pglite.ts` is the only real engine; `execution.ts` picks `mock` / `pglite` / `http` |
| SQLite dataset | **Missing** | one dataset, `backend/internal/dataset/schema.sql` + `seed.sql`, imported by the web app via `?raw` (`web/src/api/dataset.ts`) |
| Dialect-aware error classification | **Missing** | `web/src/api/pglite.ts` `mapError` and `backend/internal/engine/pgx.go` `classify` both switch on PostgreSQL SQLSTATE: a table with no SQLite rows |
| Dialect-aware value encoding | **Missing** | `pglite.ts` `OID_TYPE` / `TEXT_VALUE_OIDS`; `backend/internal/engine/normalize.go` `textOIDs`: both are PostgreSQL OIDs |
| Per-dialect parity audit | **Missing** | `scripts/adapter-parity-audit.mjs` compares PGlite vs gateway only; `make audit-parity` has one dialect |
| cgo-free guard | **Missing** | nothing asserts the binary did not acquire a cgo dependency |
| Writable-workspace reset (ADR 0003) | Done, PostgreSQL | `internal/envdb` per-account database; `GET /environment`, `POST /environment/reset`; PGlite `resetEnvironment()` |
| Course/chapter UI shows the engine | Partial | `web/src/components/ResultGrid.tsx` prints `qr.engine` per result; nothing names the chapter's dialect |

## Sequencing

| # | Step | Track | Ships | Depends on |
| --- | --- | --- | --- | --- |
| 1 | Dialect in the contracts | shared | `dialect` enum on QueryResult, exercise/curriculum, `/capabilities`; `DIALECT_MISMATCH` code | (none) |
| 2 | SQLite dataset + checked mapping | shared | `schema.sqlite.sql` + `seed.sqlite.sql` + `mapping.json`; `make audit-dataset-map` | 1 |
| 3 | Dialect profile: errors + value encoding | shared | `contracts/dialects/sqlite.json` + `dialect.schema.json`; a test per code on each side | 1 |
| 4 | Gateway SQLite adapter (Track B) | backend | `internal/engine/sqlite.go` behind `POST /query`; `make check-cgo` | 1, 2, 3 |
| 5 | Per-learner SQLite isolation (Track B) | backend | file-per-account env + reset-by-copy mapped onto ADR 0003; settles D14 | 4 |
| 6 | Browser SQLite adapter (Track F) | frontend | wasm in a Worker; caps, deadline kill, honest capabilities | 1, 2, 3 |
| 7 | Dialect badge + actionable refusal (Track F) | frontend | chapter badge, disabled Run, one-action switch; settles D12 usage | 6 (+1) |
| 8 | Per-dialect parity audit | shared | `make audit-parity` runs both dialects; `docs/adapter-parity.md` §SQLite | 4, 5, 6, 7 |
| 9 | M6 release | shared | `make verify` + every `audit-*` green; docs updated; state flipped | 1–8 |

Steps 1–3 are shared and small by design: the contracts, dataset and dialect
profile are the seam both tracks compile against, so they land before the two
tracks diverge. Steps 4–5 and 6–7 are disjoint file sets (`backend/` vs `web/`)
and can proceed in parallel after step 3. Step 8 is the join: it needs both
adapters and the profile, so it cannot start before either track is runnable.
Step 9 only ships with the owner's go-ahead, matching M3's step 8.

D12 (browser engine) is still **Undecided**. Step 6 opens with a spike that must
record measured numbers (wasm bundle size, cold start, OPFS availability,
whether the host can terminate the worker mid-statement) before the adapter is
built against a chosen library; the spike result and the decision go in an ADR,
and the design §15 row is updated. D13 is decided (`modernc.org/sqlite`). D14 is
settled by step 5's spike. D15 is decided (one dialect per chapter) and shapes
the course track at the end of this file: **every chapter carries exactly one
dialect badge**, and a cross-dialect comparison is a *separate* chapter, not a
dialect-neutral chapter.

---

## Step 1: Dialect in the contracts

The smallest change that makes a dialect expressible, so both tracks and the
course track have something to compile against. No execution behaviour changes.

1. **QueryResult** (`contracts/query-result.schema.json`): add a required
   `dialect` property, `enum: ["postgresql", "sqlite"]`, next to `engine`.
   `engine` stays the human string ("PostgreSQL 18.3", "SQLite 3.4x"), because
   `ResultGrid.tsx` already prints it and `probeGatewayEngine()` already reads
   `/health`; `dialect` is the machine value the UI compares against a chapter.
   Add the same field to the Go `engine.QueryResult` struct and the TypeScript
   `QueryResult` type (`web/src/types.ts`).
2. **Exercise setup** (`contracts/exercise.schema.json` → `setup`): add a
   required `dialect` so a graded exercise names the engine its contract was
   written for. A contract whose values are dialect-specific is otherwise
   ambiguous the moment two dialects exist.
3. **Curriculum** (`web/src/data/curriculum.ts`, `web/src/types.ts`): add
   `dialect` to `CourseModule`, and a `dialect` (default) to `Course`. Chapters
   are the unit D15 decides, so the module field is authoritative and the course
   field is a default for new chapters.
4. **Capabilities** (`backend/internal/httpapi/handlers.go` →
   `handleCapabilities`, `openapi.go` → `CapabilitiesResponse`; and
   `EngineCapabilities` in `web/src/types.ts`): add `dialects: string[]` (what
   the running gateway can serve) and keep `isolation`. The browser adapter's
   `EngineCapabilities` gains `dialect: "postgresql" | "sqlite"` so the UI has
   one honest source for "what am I about to run on".
5. **Mismatch code**: add `DIALECT_MISMATCH` to the closed error-code enum in
   `contracts/query-result.schema.json` (and the Go constants in
   `engine/engine.go` and the TS union in `web/src/types.ts`). It is a product
   refusal, not an engine error: the message names the chapter's dialect, the
   running engine, and the one action that fixes it. Adding a code to a closed
   enum is a deliberate contract change; the alternative (reusing
   `ENVIRONMENT_UNAVAILABLE`) hides a fixable mistake behind a fault, so it is
   rejected.

**Files/areas.** `contracts/query-result.schema.json`,
`contracts/exercise.schema.json`; `backend/internal/engine/engine.go`,
`backend/internal/httpapi/handlers.go`, `openapi.go`; `web/src/types.ts`,
`web/src/data/curriculum.ts`. No new files.

**Verification.** `cd backend && go test ./...` (contract tests validate the
new field against the schema); `cd web && npx tsc --noEmit`;
`make audit-content`. The schemas stay valid against the sample documents.

**Done when:** a `QueryResult` without `dialect` fails both the Go contract test
and the browser validation; `/capabilities` advertises `dialects`; the
error-code enum still round-trips on both sides.

## Step 2: The SQLite dataset and its checked mapping

"A second dataset authored for SQLite (schema + seed), with a checked mapping to
the commerce dataset so the two dialects answer the same questions about the
same shape." The mapping is the interesting part: it is the artifact that lets a
reviewer see, column by column, that SQLite `active` (an INTEGER holding `0`/`1`)
is the same column as PostgreSQL `active` (`boolean`), and that every other
divergence is a named row with a reason rather than an omission.

1. Author `backend/internal/dataset/sqlite/schema.sqlite.sql` and
   `seed.sqlite.sql`: the same four tables (`customers`, `products`, `orders`,
   `order_items`), the same row counts and the same required edge cases from
   §7.1 (customers with no orders, never-ordered products, NULL emails/cities, a
   zero-stock product, several statuses across several months). SQLite has no
   `dataset` schema, so the tables are unqualified; there is no `search_path`.
   Types are chosen to be honest about SQLite and still teachable: declared types
   as in the PostgreSQL dataset, but see step 3 for why a declared type is not a
   constraint. `STRICT` is a decision for step 3 + the course track, not an
   assumption here.
2. Author `backend/internal/dataset/sqlite/mapping.json`: for every table and
   column, the PostgreSQL name/type → SQLite name/declared type, plus the
   value-representation mapping (boolean `true`/`false` ↔ `1`/`0`, `date` ↔
   ISO-8601 `TEXT`, `timestamp` ↔ ISO-8601 `TEXT`, `numeric(10,2)` ↔ declared
   `NUMERIC` with the same scale in the seed). Every deliberate divergence is a
   named row with a reason, not an omission.
3. Make the SQLite files the second `?raw` import beside `dataset.ts`
   (`web/src/api/sqliteDataset.ts`), and embed them in Go with the existing
   `go:embed` set in `backend/internal/dataset/dataset.go`. One source of truth
   per dialect, imported by both sides, mirroring `schema.sql`.
4. `scripts/dataset-map-audit.mjs` + `make audit-dataset-map`: apply both
   schemas+seeds to a real engine of each dialect, then assert against the
   manifest: table set, row counts, per-column declared type, the edge-case
   probes, and that every PostgreSQL column has a mapped SQLite column and vice
   versa. A divergence is a failure unless the manifest names it.

**Files/areas.** `backend/internal/dataset/sqlite/` (new), `dataset.go` (embed);
`web/src/api/sqliteDataset.ts` (new); `scripts/dataset-map-audit.mjs` (new),
`Makefile` (`audit-dataset-map`, added to `test`). The PostgreSQL dataset is
untouched.

**Verification.** `make audit-dataset-map` (returns a table of matched rows and
named divergences); `make dataset` still rebuilds the PostgreSQL pair unchanged.

**Done when:** both seeds satisfy the same §7.1 edge-case list, the manifest
accounts for every column, and the audit fails if a table, column, edge case or
row count drifts on either side.

## Step 3: The dialect profile for error classification and value encoding

SQLite has no SQLSTATE, no boolean, no date type and ASCII-case-insensitive
`LIKE`; PostgreSQL has all four. Grading must therefore be per dialect, and the
rules must be one artifact both languages read: the same pattern as
`contracts/fixtures/`.

1. Add `contracts/dialect.schema.json` and `contracts/dialects/sqlite.json`
   (and leave a `postgresql.json` that records today's behaviour so the pair is
   symmetric). The SQLite profile holds:
   - **Error map.** Every reachable SQLite primary result code and the message
     patterns that split the generic ones, each → one contract code:
     `SQLITE_ERROR(1)` splits on the message into `QUERY_SYNTAX_ERROR`
     (`near "…": syntax error`, `incomplete input`, `unrecognized token`,
     `unterminated …`) or `QUERY_RUNTIME_ERROR` (`no such table`, `no such
     column`, `no such function`, `ambiguous column name`, …);
     `SQLITE_CONSTRAINT(19)` and its extended subtypes → `QUERY_RUNTIME_ERROR`;
     `SQLITE_MISMATCH(20)`, `SQLITE_RANGE(25)`, `SQLITE_READONLY(8)`,
     `SQLITE_AUTH(23)` → `QUERY_RUNTIME_ERROR`; `SQLITE_INTERRUPT(9)` →
     `QUERY_TIMEOUT`; `SQLITE_BUSY(5)`/`SQLITE_LOCKED(6)` → `QUERY_TIMEOUT`
     (contention spends the learner's deadline; the message names the writer,
     exactly as the PostgreSQL pool-saturation path already does);
     `SQLITE_TOOBIG(18)` → `RESULT_LIMIT_EXCEEDED`; `SQLITE_CANTOPEN(14)`,
     `SQLITE_NOTADB(26)`, `SQLITE_CORRUPT(11)`, `SQLITE_IOERR(10)`,
     `SQLITE_FULL(13)`, `SQLITE_PERM(3)`, `SQLITE_NOMEM(7)`, `SQLITE_MISUSE(21)`
     → `ENVIRONMENT_UNAVAILABLE`; the default is `QUERY_RUNTIME_ERROR`.
     `PERSISTENCE_FAILED` and `EVALUATION_ERROR` are not engine errors and the
     executor must never emit them.
   - **Value encoding.** INTEGER → JSON number when it is within
     `Number.MAX_SAFE_INTEGER`, else a decimal string (the same rule
     `pglite.ts` already uses for `bigint`); REAL → number, with a named rule for
     the non-finite cases; TEXT → string verbatim; BLOB → `\x<hex>` (matches
     PostgreSQL `bytea`'s text form and `pglite.ts`'s `\x` convention); NULL →
     null. No booleans and no dates: a `BOOLEAN`-declared column stores `0`/`1`
     and a date column stores ISO-8601 text, and those values are emitted as
     integers and strings, never coerced to `true`/`false` or a JS `Date`.
   - **Column type label.** SQLite's `sqlite3_column_decltype` is the declared
     type verbatim when present (`INTEGER`, `TEXT`, `NUMERIC`, `VARCHAR(20)`, …)
     and empty for expressions. The profile defines the fallback label for the
     empty case; it must not claim a type the engine never had.
2. Implement the profile twice, once per side, and **test each entry**:
   `backend/internal/engine/sqlite_classify_test.go` (table-driven; one case per
   contract code, plus a case for every SQLite result code in the profile) and
   `scripts/sqlite-error-codes.mjs` + `make audit-sqlite-errors` for the browser
   side. Both read `contracts/dialects/sqlite.json`, so the two languages cannot
   drift. The PostgreSQL profile gets the mirror test so the pair is symmetric.
3. `docs/sqlite-dialect.md`: publish the two tables (error map, value encoding),
   the divergence from PostgreSQL in one line each, and the same "what is not
   covered" honesty as `docs/adversarial-suite.md`.

**Files/areas.** `contracts/dialect.schema.json`, `contracts/dialects/` (new);
`backend/internal/engine/sqlite_classify.go` + `_test.go`;
`web/src/api/sqliteProfile.ts` (new) or a shared reader; `scripts/sqlite-error-codes.mjs`
(new); `Makefile` (`audit-sqlite-errors`); `docs/sqlite-dialect.md` (new).

**Verification.** `cd backend && go test ./internal/engine/ -run SQLite`;
`make audit-sqlite-errors`; `make audit-fixtures` (the comparison rules are
untouched by this step).

**Done when:** every contract code reachable from the SQLite executor has a
named producer and a passing case; the profile is a single file both languages
load; and `docs/sqlite-dialect.md` states where SQLite's error and value
behaviour differs from PostgreSQL's.

## Step 4: Gateway SQLite adapter (Track B)

SQLite behind the same `POST /query` contract, pure Go, cgo-free.

1. `backend/internal/engine/sqlite.go`: an `Executor` implementation over
   `database/sql` + `modernc.org/sqlite` (`driverName = "sqlite"`), not
   `mattn/go-sqlite3` (which requires cgo and would break the single binary).
   It satisfies the existing `engine.Executor` interface: `Query`, `Ping`,
   `Version` (`SELECT sqlite_version()`), `Stats` (zeroes, not invented pool
   numbers), `Ready`, `Close`, so `engine.Execute` supplies the row cap, byte
   cap, deadline, truncation flag and `RESULT_LIMIT_EXCEEDED` unchanged. This is
   the whole point of the seam: the caps are not reimplemented.
2. Deadline and cancellation: build the statement with
   `context.WithTimeout`; the driver interrupts on context cancellation, and the
   resulting `SQLITE_INTERRUPT(9)` maps to `QUERY_TIMEOUT` via step 3. Whether
   the interrupt fires mid-statement for every statement shape is a **spike
   question** for this step, record what was measured, do not assume it.
   `modernc.org/sqlite` returns `*sqlite.Error` with an extended `Code()`; the
   classifier reads that, never a message alone where a code exists.
3. `PRAGMA`s the executor needs are set per connection via the DSN
   (`_pragma=foreign_keys(1)` etc.): never inherited from a global: in SQLite
   `foreign_keys` is **off by default**, so a chapter that teaches foreign keys
   must be explicit. The exact set (foreign_keys, busy_timeout, journal_mode,
   query_only for anonymous) is recorded in step 5's ADR; step 4 lands the
   read-only path with `query_only`.
4. Dialect dispatch: `POST /query` gains an optional `dialect` (default
   `postgresql`). The server selects the executor by dialect; an unsupported
   dialect returns `DIALECT_MISMATCH` (step 1) with an actionable message.
   `QueryResult.dialect` and `engine` come from the executor that ran the
   statement. `GET /health`, `/capabilities`, `/diagnostics` and `/metrics` grow
   a dialect dimension (per-dialect readiness, version, counters) but keep
   PostgreSQL as the default so `make verify` is unchanged.
5. cgo guard: `make check-cgo` runs `CGO_ENABLED=0 go build ./...` **and** a
   dependency scan (`go list -deps ./...` must not contain a cgo-using package;
   a `go:build cgo` file anywhere fails the target). Added to `make test`.
6. Go tests: the shared fixture suite (`internal/eval/fixtures_test.go`) runs
   the same contracts through the SQLite executor for the SQLite-tagged cases;
   `engine_test.go` fakes gain the dialect.

**Files/areas.** `backend/internal/engine/sqlite.go` (new), `sqlite_test.go`
(new); `backend/internal/httpapi/handlers.go`, `server.go`, `openapi.go` (new
dialect routing + docs); `backend/go.mod` (`modernc.org/sqlite`); `Makefile`
(`check-cgo`). JSON tags and the `Executor` interface are shared with PostgreSQL
and only extended, not forked.

**Verification.** `make check-cgo`; `cd backend && go test ./...`;
`make audit-fixtures`; `make verify` (PostgreSQL path unchanged, still green);
`make verify-mock` (SQLite unavailable is reported honestly, not as invented
rows).

**Done when:** `POST /query {"dialect":"sqlite", …}` runs a real SQLite
statement on the gateway with the same caps, deadline and result shape as
PostgreSQL; a missing SQLite engine reports `ENVIRONMENT_UNAVAILABLE`; and
`CGO_ENABLED=0 go build ./...` still produces the single binary.

## Step 5: Per-learner SQLite write isolation (Track B), settling D14

D14: *per-learner database file, reset by copy*. This step maps that onto ADR
0003's per-account environment and reset, so the HTTP shape a learner sees does
not change between dialects.

1. **Spike (measured).** Build the SQLite template file once (schema + seed),
   seal it read-only, and measure create (copy), reset (copy over / atomic
   rename), drop, and per-file size: the ADR 0004 figures (≈1 ms copy vs ≈35 ms
   `CREATE DATABASE` for PostgreSQL) are the ADR's claim, not this repo's
   measurement, so re-measure here and record it. Also settle: journal mode
   (`DELETE` vs `WAL` and the `-wal`/`-shm` sidecars a reset must remove),
   whether a copy needs a checkpoint first, and the writer-contention behaviour
   with one connection per environment.
2. Extend `backend/internal/envdb/` with a dialect-specific backend behind the
   existing interface: PostgreSQL keeps `CREATE DATABASE … TEMPLATE`; SQLite
   gives each account `<env-root>/<account>.db`, copied from the sealed template,
   reset by re-copy (the file is the database: there is nothing to `DROP`).
   `GET /environment` and `POST /environment/reset` keep their response shapes;
   the per-account environment's `database` name points at the file. The
   reset-by-copy path mirrors the PGlite adapter's `resetEnvironment()`
   (dispose + rebuild from the bundled schema/seed), which is the same
   "reset, not repair" idea in a different mechanism.
3. **Anonymous stays read-only**, exactly as ADR 0003 requires: no account, so
   no per-learner file; the shared SQLite dataset is opened `query_only`
   (step 4) and a write is refused with `QUERY_RUNTIME_ERROR`, matching the
   PostgreSQL anonymous path.
4. A Go test proves isolation by behaviour, not by construction: two accounts
   writing to their own files, neither sees the other's rows, a reset restores
   the seed, and a write that fails is not half-applied. This is the SQLite
   analogue of `TestLearnerRoleCannotEscapeTheSandbox`.

**Files/areas.** `backend/internal/envdb/` (dialect backend), a
`template_sqlite.go` beside `dataset/template.go`; ADR
`docs/adr/0006-sqlite-learner-isolation.md` (new, records the measured spike and
the D14 decision); `docs/technical-design.md` §15 D14 default → Decided.
Nothing in `web/`.

**Verification.** `cd backend && go test ./internal/envdb/ ./internal/dataset/`;
`make verify` (PostgreSQL unchanged); a live probe of `GET /environment` /
`POST /environment/reset` against a SQLite dialect, recorded in the ADR the way
M3 step 8 recorded its table.

**Done when:** a signed-in learner's SQLite writes land in their own file,
survive a reload, and are undone by one reset; an anonymous write is refused;
and the recorded copy/reset numbers are attached to the decision.

## Step 6: Browser SQLite adapter (Track F)

SQLite compiled to WebAssembly in a Worker, mirroring the PGlite adapter. This
step **settles D12** (currently *Undecided*).

1. **Spike first.** Compare the real candidates on this machine (`wa-sqlite` and
   `sql.js` are the two named in ADR 0004; consider others only if the spike
   needs to). Record, measured: wasm bundle size, cold start, whether the engine
   persists to OPFS (and on which browsers), whether the host can terminate a
   statement mid-run, and the exact SQL surface for caps. Write the decision in
   `docs/adr/0007-browser-sqlite-engine.md` (0006 is the learner-isolation ADR),
   update §15 D12, and only then build.
   If no candidate can be pre-empted, the adapter must report `hardTimeout:
   false`: the honest-reporting rule from `pglite.ts` applies unchanged.
2. `web/src/api/sqlite.worker.ts` + `web/src/api/sqlite.ts`: the same lifecycle
   as `pglite.ts`: a Worker with a main-thread fallback when `new Worker` is
   blocked (the design Appendix A finding), `engineCapabilities()` that returns
   `null` until boot so the UI says "starting", and `placement` reported
   honestly. Same deadline kill: the worker is terminated on timeout and the
   dataset is restored, so a runaway `WITH RECURSIVE …` cannot freeze the tab.
3. **Same caps.** Apply the PGlite row cap (`MAX_ROWS`) and the gateway's byte
   cap before the result crosses `postMessage`, with the same `truncated` flag: 
   the caps are the contract's, not the engine's. Note the current PGlite path
   enforces the row cap and does not separately implement the byte cap; whether
   that is a pre-existing gap to close in both adapters or a documented
   difference is a **spike question** for this step, and the answer is written
   down either way. Row/byte caps must not be "roughly the same": they are
   stated as numbers in the adapter and asserted by a test.
4. **Same honest capability reporting**: `EngineCapabilities` gains
   `dialect: "sqlite"`; `hardTimeout` is true only where the spike proved the
   host can stop the engine; `writable` reflects the browser workspace.
5. `resetEnvironment()` re-seeds from `sqliteDataset.ts` (schema + seed + the
   SQLite analogue of `app.content_docs`), so a reset is by construction, and
   the adapter's SQLite profile (step 3) is applied to every result.
6. The adapter is selected by the existing seam (`web/src/api/index.ts`,
   `execution.ts`): a `VITE_API_MODE` value (e.g. `sqlite`) selects it in dev;
   the single binary's own-origin probe still prefers the gateway. No screen
   imports the adapter directly.

**Files/areas.** `web/src/api/sqlite.ts`, `sqlite.worker.ts`,
`sqliteDataset.ts`, `sqliteProfile.ts` (new); `web/src/api/execution.ts`,
`index.ts`, `web/src/types.ts` (dialect on capabilities/QueryResult);
`web/package.json` (the chosen engine); `docs/adr/0007-browser-sqlite-engine.md`
(new). No backend
files.

**Verification.** `cd web && npx tsc --noEmit`; `npm run build` (the wasm asset
ships); a runaway-statement check like the PGlite deadline test; the browser half
of `make audit-parity` once step 8 wires it in.

**Done when:** a SQLite statement runs in the browser against the SQLite dataset,
a runaway statement is stopped by the host where the spike proved it can be, caps
and truncation match the stated numbers, and `engineCapabilities()` never claims
a capability the spike did not measure.

## Step 7: Dialect badge and an actionable mismatch refusal (Track F)

The gate clause a learner actually experiences. The UI must say which dialect a
chapter is on, and a mismatch must be fixable in one action.

1. **Badge.** `CourseModule.dialect` (step 1) renders as a badge on the lesson
   header and the course card: one word, `PostgreSQL` or `SQLite`, next to the
   chapter title, using existing type scale and colour tokens (this is a port of
   an existing pattern, not a redesign; `ResultGrid.tsx` already prints the
   engine string per result).
2. **Active-dialect truth.** The badge is compared against the active adapter's
   dialect from `ExecutionService.capabilities()`, not a constant, so the UI
   cannot say "SQLite" while `POST /query` is talking to PostgreSQL. When
   capabilities are still `null`, the badge says "starting", not a guess.
3. **The refusal.** When a chapter's dialect ≠ the active adapter's, the Run
   control is disabled and the row shows the reason and the fix:
   *"This chapter is SQLite. The active engine is PostgreSQL. Switch to the
   SQLite engine."* The action is a single control that switches the adapter
   (the existing mode/settings seam); it is not a link to documentation, and the
   learner's draft text is preserved across the switch.
4. **Server backstop, and honesty per result.** A mismatched request that does
   reach the gateway returns `DIALECT_MISMATCH` naming the accepted dialect
   (step 4). Every rendered result still carries `qr.engine` and `qr.dialect`
   from the engine that produced it, so a result can never be labelled with the
   chapter's dialect when a different engine produced it.
5. Persist the choice the way the adapter is already chosen (mode/settings), so
   a reload does not silently flip dialects under a learner's open chapter.

**Files/areas.** `web/src/components/` (lesson header, course card,
`SqlCell.tsx` run control), `web/src/state/` (settings/mode),
`web/src/api/execution.ts` (mismatch guard + capabilities). No backend files
beyond step 4's backstop.

**Verification.** `cd web && npx tsc --noEmit`; a component test or an audit
script asserting: (a) a SQLite chapter under a PostgreSQL adapter renders the
reason and a working switch, (b) after switching, Run executes, (c) a gateway
mismatch renders the server's actionable message, (d) a result's label follows
`qr.dialect`, not the chapter. Add it to the existing audit surface, not a new
test runner.

**Done when:** a learner cannot press Run on a chapter whose dialect differs from
the engine, is told which is which in plain words, and can fix it in one action
without losing their draft.

## Step 8: Per-dialect adapter-parity audit

The gate that turns "two engines" into "one product": the audit runs **once per
dialect** and both are green.

1. Generalise `scripts/adapter-parity-audit.mjs` to accept `--dialect
   postgresql|sqlite` (default `postgresql`, so existing behaviour is unchanged)
   and to select, per dialect, the adapter-A engine, the dataset, the profile and
   the exclusion list. A SQLite run compares wasm-SQLite vs gateway-SQLite;
   a PostgreSQL run stays PGlite vs gateway as today.
2. `make audit-parity` runs **both** dialects; `make audit-parity` must fail if
   either is red. Keep a `--dialect` single-run mode for debugging.
3. Extend `docs/adapter-parity.md` with a SQLite section: the same "what it
   proves / what is deliberately not compared" structure, the SQLite exclusion
   reasons (e.g. `EXPLAIN QUERY PLAN` output is planner text, not a comparable
   plan tree; `rowid` is stable only within a file), and the actual table.
4. Cross-dialect literal equality is **not** asserted by this audit and must not
   be implied: each dialect is compared against its own adapter pair. The
   "same shape of question" gate is satisfied by the query shapes (SELECT, JOIN,
   GROUP BY, window, CTE, recursive CTE) each appearing in both dialects' fixture
   sets, with values that are correct *for that dialect*.
5. The pipeline and admin-path properties carry over per dialect: signed-in
   writable comparison with both workspaces restored after every write, and the
   anonymous-write refusal re-checked on the SQLite path.

**Files/areas.** `scripts/adapter-parity-audit.mjs`, `Makefile`
(`audit-parity` runs both), `docs/adapter-parity.md`, `contracts/fixtures/`
(dialect-tagged cases for the six shapes on both dialects).

**Verification.** `make audit-parity` (both dialects green, with the printed
per-dialect tables); `node scripts/adapter-parity-audit.mjs --dialect sqlite
--markdown` for the doc paste; `make test`.

**Done when:** both dialect runs are green in one `make audit-parity`, the
PostgreSQL run is byte-for-byte the same instrument it was, and the SQLite
section publishes its table plus what it does not compare.

## Step 9: M6 release

`make verify` + `make verify-mock` + every `audit-*` (including the new
`audit-dataset-map`, `audit-sqlite-errors`, `check-cgo`, and both
`audit-parity` dialects) green; README, this plan, `docs/technical-design.md`
§14 M6 and §15 D12/D14 updated; then commit and tag **only on the owner's
go-ahead**, exactly as M3 step 8 requires.

**M6 exit gate, mapped:** actionable refusal → steps 1, 6, 7; same shape of
question → steps 2, 8; dialect-specific classification with a test per code →
step 3; parity once per dialect → step 8; cgo-free, Docker-free → step 4.

---

## Step board

| # | Step | Track | State | Notes |
| --- | --- | --- | --- | --- |
| 1 | Dialect in the contracts | shared | done | `dialect` on QueryResult/exercise/curriculum; `dialects` on `/capabilities`; `DIALECT_MISMATCH` in the closed enum; `web/src/lib/dialect.ts` is the only reader; `make audit-sqlite-errors` in `test` |
| 2 | SQLite dataset + checked mapping | shared | done | `schema.sqlite.sql`, `seed.sqlite.sql`, `mapping.json` (22 rows, 5 named mismatches each with a reason); `make audit-dataset-map` green |
| 3 | Dialect profile: errors + value encoding | shared | done | `contracts/dialects/sqlite.json` read by both languages (Go embeds a byte-identical mirror, the browser imports the contract itself); 30 ordered message rules, affinity, 28 capabilities; `docs/sqlite-dialect.md`. **Open defect**: the Go executor must strip the driver's `SQL logic error: … (N)` framing or the `equals`/`prefix` rules can never fire: measured, being fixed |
| 4 | Gateway SQLite adapter | backend | done | `backend/internal/engine/sqlite.go` over `modernc.org/sqlite` (pure Go), 15 tests incl. a real 300 ms deadline stop; per-account files + a shared read-only `commerce.db`; optional `dialect` on `/query`/`/answers/evaluate`/`/explain`; `DIALECT_MISMATCH` names the served dialects; `make check-cgo` proves the binary stays cgo-free |
| 5 | Per-learner SQLite isolation (D14) | backend | done | file-per-account, reset by re-copy; measured in ADR `0006` (copy-create ≈2 ms median vs PostgreSQL's 42–46 ms `CREATE DATABASE`; reset ≈2 ms vs 47–160 ms); D14 settled |
| 6 | Browser SQLite adapter (D12) | frontend | wip | spike measured 4 candidates (`/tmp/sqlite-spike/RESULTS.json`); `web/src/api/sqliteDataset.ts` and `sqliteProfile.ts` (contract-read classifier, encoder, capabilities) have landed; the Worker + engine choice + ADR `0007` follow |
| 7 | Dialect badge + actionable refusal | frontend | todo | badge from the chapter, truth from `capabilities()`, one-action switch, draft preserved; `dialectLabel`/`dialectSupported` already exist in `web/src/lib/dialect.ts` and nothing renders them yet |
| 8 | Per-dialect parity audit | shared | todo | `make audit-parity` runs PostgreSQL and SQLite; `docs/adapter-parity.md` gains its SQLite section |
| 9 | M6 release | shared | todo | `make verify` + all audits; tag only with the owner's go-ahead |

---

## SQLite course track

Three courses the app could ship. **D15 is settled: one dialect per chapter**, so
every chapter below carries exactly one badge. The three courses are all
`SQLite`; a chapter that needs to show the PostgreSQL form shows it as a labelled
"in PostgreSQL" callout, and the *sibling* PostgreSQL course is where that form
is practised. Chapter ids continue the existing convention (`s…` for the SQLite
track, the same way `m…`/`j…`/`q…`/`f…` name the current four).

The differences are grounded in SQLite 3.4x behaviour; version-gated features
name their minimum version, and anything the plan asserts about the engines is a
line the adapter tests must confirm (step 3), not a fact taken on faith. This is
the same posture ADR 0004 takes when it warns that `STRICT`, `RETURNING` and
`RIGHT`/`FULL JOIN` narrow the gap "somewhat".

### Course 1: *SQLite Essentials* (`sqlite-essentials`)

Badge on every chapter: `SQLite`. Level: beginner → intermediate. The dialect
course that teaches the engine on its own terms, and where a PostgreSQL reflex
produces the wrong result.

| Chapter | Dialect badge | SQLite-vs-PostgreSQL difference taught |
| --- | --- | --- |
| s1: *A database in a file* | SQLite | No server, no roles, no `search_path`; the file is the database; `sqlite_master` vs the PostgreSQL catalog; what "air-gapped" means here (ADR 0004). |
| s2: *Storage classes and type affinity* | SQLite | Five storage classes (NULL, INTEGER, REAL, TEXT, BLOB) and five affinity rules; affinity only converts when the text looks numeric, so a column declared `INT` still accepts `'abc'` and stores it as TEXT: `SELECT typeof(x)` reveals what is really stored, where PostgreSQL's `integer` rejects it with `22P02`. `CAST(x AS t)` is the only cast: there is no `::`. |
| s3: *`rowid`, `INTEGER PRIMARY KEY` and `AUTOINCREMENT`* | SQLite | Every ordinary table has a `rowid`; `INTEGER PRIMARY KEY` aliases it; `AUTOINCREMENT` (which uses `sqlite_sequence`) promises no reuse: plain `INTEGER PRIMARY KEY` reuses ids after deletes; PostgreSQL has no `rowid` and uses `GENERATED … AS IDENTITY`/`serial`. |
| s4: *Dates are not a type* | SQLite | No `DATE`/`TIMESTAMP`/`INTERVAL`; store ISO-8601 `TEXT`, Julian `REAL` or epoch `INTEGER` and compute with `date()`, `datetime()`, `strftime()`, `julianday()`, `unixepoch()` (3.38+); `DATE_TRUNC`, `TO_CHAR`, `DATE '…'` and `INTERVAL '…'` do not exist (ADR 0004 counts 30 `DATE_TRUNC` and 33 `::` uses in the current curriculum). |
| s5: *`LIKE`, `GLOB` and collations* | SQLite | `LIKE` is case-insensitive for ASCII **by default**; there is no `ILIKE`; `GLOB` is case-sensitive; `COLLATE NOCASE` is ASCII-only; `PRAGMA case_sensitive_like`. PostgreSQL defaults to case-*sensitive* `LIKE` and offers `ILIKE`, so the reflex teaches the wrong habit (ADR 0004: **absent** for `ILIKE`). |
| s6: *Reading a query plan without `EXPLAIN ANALYZE`* | SQLite | SQLite has `EXPLAIN` and `EXPLAIN QUERY PLAN` only: planner text, no actual row counts; `ANALYZE` populates `sqlite_stat1`. The M5 performance lab is built on `EXPLAIN ANALYZE`, so this chapter teaches what *can* be read here; ADR 0004 calls the PostgreSQL chapter "not portable at all". |
| s7: *When SQLite is the right choice, and what it cannot do* | SQLite | No roles or `GRANT`, no schemas beyond `ATTACH`, limited `ALTER TABLE`, one writer at a time, weak typing unless `STRICT`; the honest comparison with a client/server engine and with PostgreSQL specifically. |

### Course 2: *SQLite in Production* (`sqlite-in-production`)

Badge on every chapter: `SQLite`. Level: intermediate → advanced. For a learner
who can write SQL and wants the parts of SQLite that surprise application
developers.

| Chapter | Dialect badge | SQLite-vs-PostgreSQL difference taught |
| --- | --- | --- |
| p1: *One writer at a time: concurrency and `WAL`* | SQLite | `PRAGMA journal_mode=WAL` allows readers with one writer; `SQLITE_BUSY`/`SQLITE_LOCKED` and `busy_timeout`; PostgreSQL runs many writers under MVCC, so this is a different model, not a slower one. |
| p2: *`PRAGMA` vs `SET`* | SQLite | `PRAGMA journal_mode`, `foreign_keys`, `busy_timeout`, `cache_size`, `synchronous`, `user_version`, `application_id`, `table_info` return rows and are statements; PostgreSQL uses `SET`/`SHOW`/`pg_settings`. `PRAGMA` names are per-connection, which is why the executor sets them in the DSN (step 4). |
| p3: *Types that do not lie: `STRICT` tables* | SQLite | `STRICT` tables (3.37+) enforce the declared type from a fixed list (`INT`, `INTEGER`, `REAL`, `TEXT`, `BLOB`, `ANY`); ordinary tables only apply affinity. This is the chapter that makes s2 actionable; note `ANY` has no PostgreSQL equivalent and PostgreSQL has no per-table switch. |
| p4: *Constraints, foreign keys and `ON CONFLICT`* | SQLite | `PRAGMA foreign_keys` is **off by default** (so the constraint may exist and not fire); `RETURNING` (3.35+) and `ON CONFLICT DO NOTHING/UPDATE` (3.24+); `last_insert_rowid()` vs PostgreSQL's `RETURNING`. |
| p5: *JSON, generated columns and expression indexes* | SQLite | The JSON1 functions and `->`/`->>` (3.38+) exist without an extension in modern builds; generated columns (3.31+) and partial/expression indexes; `jsonb` in PostgreSQL stores a different representation: comparison is by behaviour, not by bytes. |
| p6: *Changing a schema with `user_version`* | SQLite | `ALTER TABLE` supports only `RENAME TO`, `RENAME COLUMN`, `ADD COLUMN` (with restrictions) and `DROP COLUMN` (3.35+, with restrictions); anything else is the documented table-rebuild dance; `PRAGMA user_version` versions the file where PostgreSQL migrations live in the server. |
| p7: *Backup, `ATTACH` and shipping the file* | SQLite | `.backup`/`VACUUM INTO`, `ATTACH` as the nearest thing to a schema, `PRAGMA integrity_check`, `VACUUM`; what "a backup" means when the database is a file, versus PostgreSQL's streaming/physical backups. |

### Course 3: *Porting PostgreSQL to SQLite* (`postgres-to-sqlite`)

Badge on every chapter: `SQLite`. Level: intermediate → advanced. A translation
course where every chapter is authored in SQLite (so it satisfies D15 and the
mismatch refusal in step 7) and each teaches one construct the current PostgreSQL
curriculum actually uses. It is built directly on the construct table in ADR 0004
and is the course that makes the existing `m1`–`m9` material portable.

| Chapter | Dialect badge | PostgreSQL construct → SQLite replacement |
| --- | --- | --- |
| x1: *Casts are gone: `::` → `CAST`* | SQLite | `DATE_TRUNC('month', o.ordered_at)::date` → `strftime('%Y-%m-01', o.ordered_at)` over ISO-8601 text; `CAST(x AS date)` cannot be the fix because SQLite has no `date` type. Current curriculum: 33 `::` uses, 30 `DATE_TRUNC`. |
| x2: *Conditional aggregation without `FILTER` assumptions* | SQLite | `COUNT(*) FILTER (WHERE …)` exists since 3.30, so teach the version gate and the `SUM(CASE WHEN … THEN 1 ELSE 0 END)` fallback; also `DISTINCT ON` is **absent** (2 uses in the curriculum): use `GROUP BY`/window alternatives. |
| x3: *Windows and ordering semantics* | SQLite | `PARTITION BY`/`OVER` exist since 3.25 but the value types and the default ordering differ; `NULLS FIRST/LAST` and `FILTER` on windows need checking on the shipped SQLite version. |
| x4: *Dates and intervals without types* | SQLite | `DATE '2026-01-01'`, `INTERVAL '1 month'`, `TO_CHAR` do not exist; the recursive-CTE `m9` example becomes integer/logical iteration plus `datetime(...)` arithmetic; `WITH RECURSIVE` itself is present. |
| x5: *Booleans, numerics and the value contract* | SQLite | `boolean` → declared `BOOLEAN` holding `0`/`1` (SQLite has no boolean type); `numeric(10,2)` money keeps scale as declared `NUMERIC` with the same seed scale; the recorded value-encoding rules from step 3; this is the chapter that makes cross-dialect grading legible. |
| x6: *Users, schemas and permissions do not exist* | SQLite | No roles/`GRANT`, no `search_path`, no per-schema namespaces; the isolation model is the file (step 5), not a privilege grant; `ATTACH` is not a substitute for a schema. |
| x7: *Porting the capstone* | SQLite | The `m8` capstone rebuilt end to end: `DATE_TRUNC` → `strftime`, window totals and `ROUND`, the same result contract shape. This is the evidence that the port is a translation, not a rewrite. |

A fourth, short course is optional and cheap once the three above exist: 
*SQLite for analysts* (read-only `SELECT`/`JOIN`/`GROUP BY`/window practice on
the SQLite dataset with no dialect talk). It is noted here so nobody invents it
twice; it is not part of the gate.

---

## Risks and open questions

- **D12: which browser engine.** Undecided; the step-6 spike decides it and
  records bundle size, cold start, OPFS and mid-statement termination. If no
  candidate can be pre-empted, the SQLite browser adapter must report
  `hardTimeout: false`, and the "same deadline kill as PGlite" gate clause holds
  only where the spike proved it. This is the largest unknown in M6.
- **Cross-dialect contracts.** The gate says both dialects "pass the same result
  contracts". SQLite has no boolean and no date type, so a literal, cell-for-cell
  shared contract is impossible for the constructs the current curriculum uses.
  This plan reads the clause as *same shape of question, dialect-correct values*
  and keeps per-dialect fixtures. If the owner wants literal shared contracts,
  that is a scope change to settle before step 8; it is listed here rather than
  assumed.
- **Value parity for booleans and numerics.** PostgreSQL returns `true` and
  `"118.00"`; SQLite returns `1` and a number or text that the encoding rule
  decides. Step 3 pins this; if it cannot be pinned so a lesson reads the same
  way on both dialects for the shared *shape*, the lesson text needs a dialect
  note. Unmeasured until step 3.
- **`LIKE` case-insensitivity.** ASCII-only in both directions, so a
  non-ASCII `'İ'`/`'é'` case pair behaves differently than a learner might
  expect; the course teaches it, but the evaluator must not assume ASCII.
- **Date/time stored as text.** Sorting and range comparisons depend on a
  consistent ISO-8601 encoding in the seed; the mapping manifest (step 2) must
  pin it, and a mixed encoding is a dataset defect, not a lesson.
- **Deadline enforcement on the gateway.** Whether `modernc.org/sqlite`
  interrupts a running statement promptly at every shape (recursive CTE, huge
  cross join, large scan) is a step-4 spike question. If it does not, the
  gateway must say `QUERY_TIMEOUT` only when it actually interrupted, and the
  worker/process must be bounded some other way; no invented guarantee.
- **Concurrency.** SQLite is one writer. Per-learner files make cross-account
  contention unlikely, but the shared anonymous read-only file and the template
  copy are shared resources; step 5 measures the behaviour under the load test
  rather than assuming it scales like PostgreSQL. Any load number for SQLite is
  unmeasured until then.
- **`/health`, `/capabilities`, `/diagnostics` shape.** Adding a dialect
  dimension must not change existing consumers (`make verify`,
  `probeGatewayEngine()`); the default remains PostgreSQL, so step 4 is additive.
- **`DIALECT_MISMATCH` on the closed enum.** A deliberate contract change; it
  must be added to the JSON Schema, the OpenAPI components, and both language
  unions in the same step, or a dialect refusal becomes an `EVALUATION_ERROR`.
- **Lock-in on a version.** SQLite's version-gated features (`STRICT` 3.37,
  `RETURNING` 3.35, `RIGHT`/`FULL JOIN` 3.39, `unixepoch()` 3.38, JSON `->`
  3.38) mean the shipped engine version is a teaching constraint, not an
  implementation detail. Step 3 must record the version the adapter ships, and
  `make audit-courses` must fail if a chapter uses a construct above it.
- **Anonymous SQLite workspace.** ADR 0003 gives anonymous visitors a read-only
  shared dataset. If the owner later wants anonymous writes, that is a new
  decision (the file names itself, but "no stable identity" still holds), not an
  M6 default.

## What this does NOT prove

- **That SQLite and PostgreSQL return the same values.** They are different
  engines with different type systems; the plan proves the same *shape* of
  question is runnable and gradeable on each, not that the two agree cell for
  cell. ADR 0004's whole argument is that pretending otherwise is the bug.
- **That a browser SQLite engine can be pre-empted.** Unmeasured until the step
  6 spike; until then no deadline guarantee is claimed for that adapter.
- **That SQLite scales.** No load or concurrency numbers exist for the SQLite
  path. The M3 load test's numbers are PostgreSQL's; step 5 measures what it
  measures and no more.
- **That the port is complete.** The three courses cover the constructs the
  current curriculum uses and the dialect differences named in the task; they do
  not claim to cover every SQLite feature or every PostgreSQL feature.
- **That error classification is exhaustive.** The step-3 table covers the
  reachable codes and the message patterns the executor can produce; an
  unlisted code falls back to `QUERY_RUNTIME_ERROR` and is recorded as such.
- **That learner SQL is safe in SQLite the way it is in PostgreSQL.** SQLite has
  no roles to confine a session; the boundary is the file and `query_only`
  (steps 4–5). A file-level escape (a path traversal, a symlink, a `ATTACH` to
  another learner's file if paths are ever exposed) is not exercised here and
  belongs in the adversarial suite as a follow-on, not in this plan's claims.
- **That the M5 performance lab works on SQLite.** It does not; SQLite has no
  `EXPLAIN ANALYZE` (ADR 0004; SQLite Essentials s6). M5 stays PostgreSQL-only.
- **That the single binary stays one binary.** `make check-cgo` proves
  cgo-freedom, not that the SQLite dataset compiles into the same binary; step 4
  embeds it and `make build` is the check.

## Constraints (do not violate)

- Docs and code for M6, but **PostgreSQL stays the default dialect**: no
  existing course, contract value or adapter changes behaviour.
- Greenfield: no migrations, no backward compatibility, no data backfill.
- Every executed thing goes through the `ExecutionService` seam
  (`web/src/api/`); a screen never calls `fetch` and never fabricates a result.
- The Go gateway stays honest with no database: `ENVIRONMENT_UNAVAILABLE`, never
  invented rows. That applies per dialect.
- The single binary stays **cgo-free and Docker-free**; `make check-cgo` is part
  of `make test` from step 4 onward.
- A new grading rule is added as fixtures first
  (`contracts/fixtures/`, `make audit-fixtures`), and the manifest/schema for
  each dialect has one source of truth.
- Version a thing when something stored refers to it: `dataset_version` covers
  both datasets, and a dialect-tagged exercise keeps its own `version`.
- No secrets, DSNs or host paths in logs, `QueryResult`, `/capabilities` or
  `/diagnostics`: the M3 rule carries into the SQLite profile.
- Do not commit or tag without asking.
