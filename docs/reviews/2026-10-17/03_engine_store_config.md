# Thermo-nuclear review 03: the substrate (engine, store, envdb, localdb, config, dataset, eval, metrics, schema)

Scope: `backend/internal/{engine,store,envdb,localdb,config,dataset,eval,metrics,schema}` and `cmd/api`.
Read-only. `cd backend && go build ./...` is green. I did not run `go test` (it boots a real
PostgreSQL and the brief forbids it), so every claim below is from reading the source, the
dependency's source, and counts produced with the commands quoted inline.

## Verdict

The substrate is honest and unusually well-reasoned: the `Executor` seam, `engine.Execute`,
the eval rules, the metrics registry and the localdb bootstrap are the right size and shape, and
the comments explain measured decisions rather than restating the code. The user's hypothesis is
half right. It is not over-abstracted at the *interfaces*; it is under-factored at exactly one
place and over-implemented at another. The `Store`/`Depot` seam has **three** full
implementations (Postgres, SQLite, Memory), and the package already contains the mechanism that
makes one of those two-SQL-dialect implementations collapse into one: `sqlStore` + `sqlDialect`
in `metadata.go` renders one set of `?`-placeholder statements for both engines. The `Depot`
methods right next to it ignore that mechanism and write the same 17 queries twice
(`challenges.go`/`courses.go`/`runs.go` vs `sqlite_depot.go`). `Memory` is the third copy, sits
outside every cross-backend parity harness, and has already drifted from the SQL backends.
That is the code-judo: delete two implementations, not polish them. The one place I expected a
code-judo and found none is `engine/sqlite_raw.go`: its 906-line hand-rolled driver is genuinely
earned, and I verified that against the dependency's own source. Everything else is small.

## Finding 1 (structural, highest conviction): the Depot writes two SQL dialects beside a seam that already solved that

`internal/store/metadata.go` (508 lines) implements the 18 `Store` methods once for both SQL
engines. It does so with three pieces: a `querier` interface with a per-driver adapter
(`pgxQuerier`, `sqlQuerier`), a `sqlDialect` that knows the four real differences
(`table`, `bind`, `stamp`, `encodeJSON`, `noRows`, `emailTaken`), and `sqlStore.stmt`, which
qualifies the table and rewrites `?` into `$n` for Postgres. One statement text, two engines,
proved by `TestStoreMethodsMatchAcrossBackends` (`store_parity_test.go:32`).

The `schema.Depot` methods do not use any of it. `internal/store/challenges.go` (187 lines),
`courses.go` (156) and `runs.go` (171) write Postgres SQL with `$n` and `pgx.ErrNoRows`;
`internal/store/sqlite_depot.go` (402 lines) writes the same 17 methods again with `?`,
`sql.ErrNoRows`, `sql.NullString` and `timestamp(time.Now())`. That is 916 lines implementing
one smaller interface (17 methods) twice, against 508 lines implementing a larger one (18
methods) once. The duplication is not theoretical: `challenges.go:144-149` selects
`proposal, approved_at` and scans `approved_at` into a variable nothing reads, while
`sqlite_depot.go:247` selects only `proposal`; `ApprovePatch` writes `now()` on Postgres
(`challenges.go:171`) and `timestamp(time.Now())` on SQLite (`sqlite_depot.go:265`). The
`schema.Depot` interface is embedded in `Store` (`store.go:101`), so both types satisfy it, and
`var _ schema.Depot = (*SQLite)(nil)` (`sqlite_depot.go:402`) is the only reason the SQLite copy
exists at all.

The remedy is to move the 17 depot methods onto the shared `sqlStore` (or a sibling
`depotStore` embedding it), reuse `dialect.stamp`/`encodeJSON`/`noRows`/`stmt`, and delete
`sqlite_depot.go` outright. The `decodeRecord`/`decodeCourse`/`decodeRun` helpers are already
shared, so nothing new has to be invented. Expected net: roughly 400 lines deleted, one SQL
dialect instead of two, and the drift class (`approved_by` semantics, `NULL` handling, timestamp
format) removed by construction. `TestPersistentStoreParity` (`sqlite_test.go:21`) already runs
the same cases against both engines, so the refactor is protected.

## Finding 2 (code-judo that deletes a whole layer): `store.Memory` is a third implementation that has drifted and is outside every parity harness

`internal/store/memory.go` is 609 lines: it re-states the record defaulting rules
(`SaveChallenge`, `SaveCourse`, `SaveRun`), the owner filter and newest-first ordering
(`challengesFor`, `coursesFor`, `runsFor`), the upsert-keeps-`started_at` rule
(`memory.go:331-337`), and ~90 lines of hand-written deep clones (`cloneRecord`, `cloneCourse`,
`cloneRun`) that exist only because the store hands out references. The memory-specific tests
(`store_test.go`, and the `TestMemory*` cases in `challenges_test.go`/`courses_test.go`/`runs_test.go`)
pin some of those rules, but the cross-backend harness does not include Memory:
`TestPersistentStoreParity` builds only `postgres` and `sqlite` factories (`sqlite_test.go:23-43`),
and `TestStoreMethodsMatchAcrossBackends` compares only those two. The drift is therefore real
and unprotected: `Memory.ApprovePatch` returns `errors.New("unknown patch: " + id)` for an
unknown id (`memory.go:296-298`) while both SQL backends silently return nil. An in-memory store
that is supposed to answer "the same rules in process" (`store.go:44`) already answers a
different rule.

The judo move: make `NewMemory` open SQLite with `:memory:`. That path exists, is tested
(`schema_parity_test.go:155` opens `NewSQLite(ctx, ":memory:")`), and `store/sqlite.go:41-53`
already builds the DSN (`mode=memory`, `cache=shared`, unique name, `MaxOpenConns(1)`). `Memory`
then becomes a wrapper that only overrides `Kind() string { return "memory" }`, which preserves
the one behavioural distinction callers rely on: `reapOrphanedEnvironments` skips the boot
reaper when `st.Kind() == "memory"` (`cmd/api/main.go:464`). Delete `memory.go` and the
memory-only tests; the SQLite/Postgres parity harness then covers degraded mode by construction
because degraded mode *is* SQLite. Cost to weigh honestly: `NewMemory()` currently returns no
error, and a `:memory:` open can (in principle) fail, so the signature gains an error or the
constructor panics on an impossible failure. It also means the no-DB store is a pure-Go SQLite
instead of a map, which changes nothing observable. This is the single largest net deletion
available in the scope.

## Finding 3 (boundary/abstraction): `envdb.envKind` invents a template-name indirection that `loaderFor` then has to undo

`envdb/manager.go` is 965 lines, the largest production file in scope, and its two long
functions `ensure` (92 lines) and `reset` (69 lines) share the same lock/in-flight scaffolding
almost verbatim (`manager.go:346-430` vs `463-523`): the `m.building[key]` wait loop, the
"release the lock, do DDL, re-acquire, delete marker, close(done)" dance, and the closed check.
That is a candidate for a single `withInflight(key, fn)` helper, but it is the smaller half of
the problem. The real smell is that `envKind` carries `template func(*Manager) string` and a
`database func(...) string` (`manager.go:317-338`), and then both `ensure` and `reset` call
`m.loaderFor(template)` (`manager.go:380`, `486`) to *re-derive the fixture loader from the
template's name string* (`manager.go:555-568`). So the kind already knows which template it
copies from, and a second function re-decides which loader that template implies by comparing
strings. Carry the loader on the kind (`load func(context.Context, string) error`) instead, and
both the inline closures at `manager.go:212-214`/`232-234` and `loaderFor` disappear. The rest of
the manager's complexity is earned: the ADR 0002 measurements, the stall-proxy test
(`stall_proxy_test.go`) and `manager_close_test.go` prove the lock-release-during-DDL rule and the
close-during-build case are real hazards, not speculation.

## Finding 4 (keep the hand-rolled driver; delete the dead escape hatch beside it): `engine/sqlite_raw.go` is earned

I tried hard to delete this file. `sqlite_raw.go` (906 lines) plus `sqlite.go` (609) is a custom
`database/sql/driver` over `modernc.org/sqlite/lib`, written because modernc converts any TEXT
result column whose declared type is `DATE`/`DATETIME`/`TIMESTAMP` into a `time.Time`, destroying
the stored text the dialect contract promises. I read the pinned dependency to test that premise:
in `modernc.org/sqlite@v1.38.2`, `sqlite.go:259-275` does exactly that conversion inside the
driver's `Next`, keyed on `ColumnTypeDatabaseTypeName(i)`, and `sqlite.go:368-370` uppercases the
decltype, so the case trick ("declare `date` lowercase") cannot dodge the switch either. There is
no option, hook or `_time_format` that reaches the read path (`_time_format` is write-side only,
and `RegisterConnectionHook` runs after connect, not per value). The contract also forbids the
other shortcut: `sqlite_test.go:248` asserts the column label is `DATE`, and the profile's
`column_type_label` rule says the declared type is reported verbatim, so declaring the column as
plain `TEXT` would change the wire. The custom driver is, on the evidence, the honest workaround,
and its own file comment already documents each measured mechanic. Keep it.

What is not earned is the escape hatch beside it. `SQLiteConfig.DSN` (`sqlite.go:126-135`)
documents itself as "UNUSED BY PRODUCTION", and I confirmed no literal anywhere sets it: the only
`SQLiteConfig{}` literals are `sqlite_test.go:65,87,752,1184,1202` and `envdb/sqlite.go:193,310`,
all using `Path`. It is exercised only indirectly through `parseSQLiteDSN`
(`sqlite_raw.go:188-230`), which has no caller other than the DSN branch of
`sqliteConnectorFor` (`sqlite_raw.go:163-172`), a branch no production or test path enters. Per
the repo's own rule ("don't preserve speculative abstractions"), delete the field, the branch and
the parser: roughly 55 lines gone with no behaviour change (the `sqliteDSN` builder on line 187 is
the one that ships and is tested at `sqlite_test.go:1133`).

## Finding 5 (minor, deletable indirection): the two `Executor` readers are identity wrappers

`DialectOf(ex) string { return ex.Dialect() }` and `EngineLabelOf(ex) string { return ex.EngineLabel() }`
(`engine.go:91,96`) are the same call under a second name. Every caller already holds the
`Executor`, and `Dialect`/`EngineLabel` are on the interface (`engine.go:206-209`), so
`execute.go:36-37,49-51` and `httpapi/handlers.go:30` can call the methods directly. The doc
comments say the wrappers "stay exported because /health reports the default dialect's name
through" them, but that only explains who calls them, not what they add. Inline them.

## Finding 6 (decomposition): `run()` is 328 lines with about a dozen responsibilities

`cmd/api/main.go` `run()` (the largest function in scope, measured at 328 lines) maps settings
into `httpapi.Config`, installs signal handling, starts the embedded PostgreSQL, ensures the
learner role, builds the executor, selects the store, seeds the admin, refuses a blank-password
bind off loopback, builds the envdb manager and both templates, builds the SQLite environments,
assembles the AI config, applies the persisted AI setting, sweeps interrupted builds, starts the
HTTP server, and drains jobs. The logic is correct and the ordering comments are load-bearing, so
this is not spaghetti; it is one function carrying every boot concern. Extracting
`openDatabases(settings, log)`, `wireAI(...)` and `serveAndDrain(api, addr)` would keep the
ordering explicit while letting a reader see the boot phases. Medium conviction: Go `main`
packages often look like this, but at 328 lines it is past the point where the ordering can be
held in one head.

## Finding 7 (keep as-is, with reasoning): `config.applyKey`'s 78-line switch

`config.go:175-252` is a 78-line switch with one near-identical `unmarshalInto` case per key. It
could shrink to a `map[string]any` of field pointers in about 20 lines. I do not recommend it: the
switch *is* the file schema, it is the thing `backend/README.md` points at as the accepted-key
list, and a pointer-map loses the per-key doc comment and the compile-time "the target is the
right kind" check that the switch's explicit `unmarshalInto(value, &s.Field)` gives. This is a
case where the hand-rolled version is the right call; the win would be line count, not
comprehension.

## Finding 8 (accuracy nits, cheap to fix)

The parity harness's own doc comment overstates its coverage. `store_parity_test.go:18-25` says
`storeMethods` is "the 14 methods of Store", but `Store` declares 18 methods plus the embedded
17-method `schema.Depot`; the list omits `UserSetting`, `SaveUserSetting`, `Setting` and
`SaveSetting`, and the script never calls them (grep count: 0). The settings/preferences seam is
tested elsewhere, but a reader trusting this comment would believe the parity harness covers a
seam it does not. Also `dataset.perfTablesResponse` still reports the commerce column list for
the perf relations while `dataset/perf.sql` adds five columns, which the README already flags as
a bug rather than a refactor; it belongs on the list because it is the same class of drift
(two descriptions of one dataset) that Finding 1 removes from the store.

## Third-party packages: the honest verdict is "none"

I weighed the usual suspects, because the brief asks. sqlc generates typed queries per dialect;
it would not remove the duplication in Finding 1 (it emits two packages for two engines), it adds
a code-generation step to a repo whose policy is a minimal, pinned dependency set, and the
existing `sqlDialect` already solves the same problem in ~120 lines that are green under a
parity test. Reject. testify would add a dependency to replace assertions whose current value is
`t.Fatalf("%#v")` against the exact value under test, and it cannot express the bespoke
record-and-diff parity harness at all. Reject. `pkg/errors` is superseded by the stdlib `%w` +
`errors.Is/As` the repo already uses consistently. Reject. A struct-tag validator
(go-playground) would fight the stable-code error surface in `httpapi` and the explicit
`NewError(code, message, detail)` contract. Reject. The one candidate worth a *future* spike is a
different cgo-free SQLite binding (`ncruces/go-sqlite3`) whose low-level API reads a column by
storage class without the DATE coercion, which could retire `sqlite_raw.go`; but swapping the
engine behind the dialect contract is a larger, measured risk than the driver it would replace,
and the current pin is already validated, so doing it now would be churn. Verdict: keep every
hand-rolled piece, and spend the effort on Findings 1-3 instead.

## Remediation sequence

1. Collapse the `Depot` onto the existing `sqlStore`/`sqlDialect` and delete `sqlite_depot.go`
   (Finding 1). Land it alone; `TestPersistentStoreParity` and `TestStoreMethodsMatchAcrossBackends`
   are the safety net. This is the biggest net deletion and unblocks Finding 2.
2. Replace `store.Memory` with a thin wrapper over `NewSQLite(ctx, ":memory:")` and delete
   `memory.go` plus the memory-only tests (Finding 2). Add `memory` to the parity factory map so
   degraded mode is covered by the same script as the real backends.
3. Delete `SQLiteConfig.DSN`, `parseSQLiteDSN` and the DSN branch in `sqliteConnectorFor`
   (Finding 4). Small, independent, zero risk.
4. Carry the fixture loader on `envdb.envKind`, delete `loaderFor`, then extract the shared
   in-flight scaffolding out of `ensure`/`reset` (Finding 3).
5. Inline `DialectOf`/`EngineLabelOf` (Finding 5) and decompose `run()` (Finding 6) in small,
   separate changes.
6. Fix the two documentation/description inaccuracies (Finding 8).

## What I did not establish

`go test` was not run, so the parity harnesses are read, not observed green, and the `ApprovePatch`
divergence (Finding 2) is argued from source, not from a failing test. The line-count deltas in
Findings 1 and 2 are estimates from the files as they stand, not measurements of the finished
refactor. `envdb`'s lock discipline and `sqlite_raw.go`'s mechanics are accepted on the strength
of the ADR, the stall-proxy test and the dependency source; I did not reproduce the timing
measurements.
