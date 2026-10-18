# store — slop audit (wave 1)

Scope: every non-test file under `backend/internal/store/` (`store.go`,
`tables.go`, `metadata.go`, `sqlite.go`, `postgres.go`, `memory.go`,
`challenges.go`, `courses.go`, `runs.go`). Read-only; `go build
./internal/store/` is green. Evidence below is `grep`/`rg` and line numbers from
the working tree, which carries the uncommitted 2026-10-18 collapse
(`git diff --stat -- backend/internal/store/` shows `memory.go -622`,
`sqlite_depot.go -402`).

## Verdict

The 2026-10-17 collapse landed and is correct: the Depot methods are written
once on `sqlStore` over the `sqlDialect` shim, `sqlite_depot.go` is gone, and
`memory.go` is a 43-line wrapper (`NewMemory` over `NewSQLite(":memory:")` with
`Kind() == "memory"`). `sqlStore`, `Postgres`, `SQLite` and `Memory` each earn
their shape. The remaining slop is small and mostly at the edges. Four interface
methods have no live consumer: `Depot.Challenges` and `Depot.Runs` are reached
only by tests and one uncalled `ai.Service.Challenges` wrapper, and
`Store.UserByID`/`Store.CountUsers` are reached only by tests. The
pre-accounts `ErrStaleSchema`/`hasLegacyMetadata` path is speculative under the
greenfield law and untested. The doc comments carry two stale facts (`tables.go`
claims "no ALTER-based upgrade path" over an additive-migration machine;
`metadata.go` says "the twelve methods" over sixteen) and the parity harness's
own comment claims coverage it does not have. The parity harness does test
promises (same behavior across Postgres/SQLite/Memory), not implementation, but
it reaches into unexported state through a concrete-type switch to do it.

## Delete

`sqlStore.Challenges` — challenges.go:106 (interface `schema.Depot` at
schema/model.go:239). Why it exists: the pre-collapse all-challenges list. The
only caller anywhere is `ai.Service.Challenges` (ai/api.go:357), which itself has
zero callers: `rg -n '\.Challenges\(' --include=*.go` returns only
store tests, that one wrapper body, and the two definitions. The live route
`GET /challenges` runs `svc.ChallengesFor` (httpapi/ai.go:359, routes.go:117), so
the owner-scoped method is the one that ships. Remedy: delete `Challenges` from
the `sqlStore`, drop it from `schema.Depot` (schema/model.go:239), delete
`ai.Service.Challenges` (ai/api.go:357-362), and drop the `Challenges` methods
from the test depots (ai/helpers_test.go:145, httpapi/ai_test.go:36). If an admin
"all challenges" surface is planned, say so; the admin-courses route
(httpapi/ai.go:463) is the only asymmetry today.

`sqlStore.Runs` — runs.go:122 (interface schema/model.go:266). Why it exists: the
all-runs list. `rg -n '\.Runs\(' --include=*.go` returns only store tests
(sqlite_test.go:172,218,251; runs_test.go:84,120,137,189,224,251). The live route
`GET /runs` runs `depot.RunsFor` (httpapi/runs.go:70) and `GET /runs/{id}` runs
`Run` (runs.go:99). Remedy: delete `Runs` from `sqlStore`, `schema.Depot`
(schema/model.go:266) and the test depots (ai/helpers_test.go:86,
httpapi/ai_test.go:68). `runsWhere` stays, used by `RunsFor`.

`sqlStore.UserByID` — metadata.go:437 (interface store.go:57). Why it exists:
account lookup by id. `rg -n 'UserByID' --include=*.go backend/` shows only the
definition, the interface, and `store_parity_test.go:120-129`. Sessions resolve
accounts through `SessionUser` (metadata.go:492), and admin seeding through
`UserByEmail` (auth/auth.go:124). Remedy: delete the method and the interface
line (store.go:57); drop the parity lines.

`sqlStore.CountUsers` — metadata.go:451 (interface store.go:58). Why it exists:
an account count. `rg -n 'CountUsers' --include=*.go backend/` shows only the
definition, interface, `store_test.go:85`, `store_parity_test.go:131-133`, and
`httpapi/auth_test.go:268`. No handler or boot path reads it. Remedy: delete the
method and the interface line (store.go:58).

`ErrStaleSchema` and `hasLegacyMetadata` — postgres.go:34, postgres.go:75. Why
they exist: to detect a pre-accounts `app.progress` and refuse it. The package's
own DDL header says this layout cannot exist: tables.go:8-10 "This is
greenfield: there is no pre-accounts layout to migrate from. A database created
before this shape existed is simply reset (`make db-reset`)". No test builds the
pre-accounts shape (`grep -rn "ErrStaleSchema\|hasLegacyMetadata" --include=*.go`
returns only postgres.go and the `main.go:437` branch), and the 2026-10-17
migration test seeds legacy *course* columns, not pre-accounts progress. This is
a second description of the reset policy, in code, on a path nothing reaches.
Remedy: delete both, and the `errors.Is(err, store.ErrStaleSchema)` branch in
cmd/api/main.go:437-441.

Stale duplicate doc comment `// SaveEvent implements Store.` — metadata.go:326.
It sits above the `/* ---------- settings ---------- */` divider; the real
`SaveEvent` and its comment are at metadata.go:387-388. A reader looking for the
event writer finds a comment with no function. Remedy: delete the two lines.

## Collapse

`tables.go` migration policy — tables.go:8-19 vs tables.go:328/346 and
sqlite.go:98. The header promises "no ALTER-based upgrade path here"; the file
below it defines `additiveColumns` and `postgresMigrations`, and `sqlite.go`
calls `migrateAdditiveColumns` from `NewSQLite` (sqlite.go:83). The additive
machine is earned (`migrate_test.go:47,112` prove a legacy `app.courses` opens and
migrates), so the machine stays and the header is the wrong half. Remedy: rewrite
the header to one fact: the DDL is final for new stores, and an existing store
gains only nullable-or-defaulted columns by `ALTER`. Keep `additiveColumns` as
the one source of the addable set.

`metadata.go:107` method count — "so the twelve methods it declares arrive on
both types". `sqlStore` now declares 16 Store methods in metadata.go
(SaveProgress…DeleteSession) plus the 17 Depot methods spread across
challenges.go/courses.go/runs.go. The number is stale and will drift again.
Remedy: drop the count ("the methods it declares arrive on both types").

`store.go:43-44` Memory description — "Memory answers the same rules in
process". After the collapse, `Memory` is SQLite over `:memory:` (memory.go:22-41),
not an in-process map. Remedy: say the SQLite statements run against an
in-memory database (or delete the clause and let memory.go carry it).

Timestamp format written twice — `sqliteNow` (tables.go:78,
`strftime('%Y-%m-%dT%H:%M:%f000Z','now')`) and `timestamp()` (sqlite.go:151,
`"2006-01-02T15:04:05.000000Z"`). Both render the same canonical microsecond UTC
text; tables.go:75-77 admits they must agree by hand. The Go formatter is used by
`dialect.stamp` on every write; the SQL form is only reachable through a column
default. Remedy: one comment naming the pair, or drive the DDL default from the
Go format constant so a change to `timestamp()` cannot silently desync the
column default.

Duplicated file rationale — challenges.go:13-18, courses.go:13-16, runs.go:13-16
each restate the same "written once for both backends; see stmt/stamp/encodeJSON"
paragraph. Remedy: keep the paragraph in challenges.go (it is the most detailed)
and let courses.go/runs.go point to it in one line, as they half do already.

Parity-harness coverage claim — store_parity_test.go:18-19 and :265 say
`storeMethods` is "the 14 methods of Store" and that the script covered them.
`Store` declares 16 methods; the list omits `UserSetting`, `SaveUserSetting`,
`Setting`, `SaveSetting`, and `grep -n "Setting" store_parity_test.go` returns
nothing, so the script never exercises the settings seam. Remedy: fix the comment
to name the covered set (or add the settings calls). This is the 2026-10-17
Finding 8 nit, still open.

## Move / split

`SQLiteSchema` exported with no external consumer — sqlite.go:32. `grep -rn
"SQLiteSchema" --include=*.go .` finds only sqlite.go:32/79 (`dataset.SQLiteSchemaSQL`
is a different symbol). Its Postgres twin is already unexported (`migrateSQL`,
postgres.go:28). Remedy: rename to `sqliteSchema`.

`AppSchema` exported with no external consumer — postgres.go:24.
`grep -rn "AppSchema" --include=*.go .` returns only store-package files
(tables.go, metadata.go, postgres.go) and the in-package tests. Remedy: unexport
unless an out-of-package consumer is intended for `docs/database-layout.md`.

`memory.go:8-17` historical narration. The block explains what the file "used to
be" (a third hand-written implementation) before saying what it is. The writing
rules prefer "state what a thing does". Remedy: keep one sentence (SQLite over an
in-memory database so degraded mode runs the same statements) and drop the
archaeology.

## Earned (checked, keep)

`sqlStore` + `sqlDialect` (`stmt`/`bind`/`table`/`stamp`/`encodeJSON`/`noRows`/
`emailTaken`) — metadata.go:106-212; one statement text, two engines, both
constructors use it (`postgresStore` metadata.go:114, `sqliteStore` metadata.go:119),
and `TestStoreMethodsMatchAcrossBackends` proves parity.
`querier`/`scanner`/`cursor` and the `pgxQuerier`/`sqlQuerier`/`sqlCursor`
adapters — metadata.go:34-102; required because pgx and database/sql name their
calls differently.
`Postgres` shape — postgres.go:15; pgxpool, `Kind`, `Close`, `isUniqueViolation`
(postgres.go:105) all used by `NewPostgres`/`CreateUser`.
`SQLite` shape — sqlite.go:23; file DSN, chmod 0600, schema Exec, additive
migration (sqlite.go:79-86), `isSQLiteUniqueViolation` (sqlite.go:184) all used.
`Memory` wrapper — memory.go:22-43; `NewMemory` has a live consumer in
cmd/api/main.go:433/441/444, `Kind()=="memory"` is read by the reaper gate
(cmd/api/main.go:459), and it is in the parity factory map (sqlite_test.go:45).
`appTables` single DDL description plus `postgresDDL`/`sqliteDDL`/
`tableBody`/`columnSQL`/`indexSQL`/`defaults` — tables.go:112-445; `schema_parity_test.go`
reads the rendered scripts and both live catalogs back against it.
Depot statements written once on `sqlStore` — challenges.go:59-191,
courses.go:34-138, runs.go:44-166 (17 methods); `sqlite_depot.go` is deleted and
`git diff --stat` confirms the -402 lines.
`jsonArg` — challenges.go:50; used by all seven Depot writers.
`decodeRecord`/`decodeCourse`/`decodeRun` — challenges.go:196, courses.go:144,
runs.go:169; each decodes only the JSON columns and is used by both the single
read and the list loop.
`challengesWhere`/`coursesWhere`/`runsWhere` with `ChallengesFor`/`CoursesFor`/
`RunsFor` — challenges.go:112, courses.go:97, runs.go:127; owner filtering runs in
SQL and the live routes read them (httpapi/ai.go:359,443; httpapi/runs.go:70).
`SaveRun` upsert and `ApprovePatch` first-approval-only — runs.go:44-89,
challenges.go:185-191; the conflict clauses encode the rules, and the parity
harness checks the original-`started_at` and first-`approved_by` outcomes
(sqlite_test.go:248,287-295).
`normalizeJSON`/`parseTimestamp`/`timestamp` — sqlite.go:151-180; SQLite's JSON
and timestamp storage, exercised by the parity harness (`canonical`, `writtenNow`).
`ErrEmailTaken` mapping — store.go:18, metadata.go:414-416, postgres.go:105,
sqlite.go:184; the handler maps it to 409 (httpapi/auth.go:169).
Parity harness semantics — store_parity_test.go:32, sqlite_test.go:21; it compares
one behavior script across backends (a promise) rather than asserting column
names or SQL text.

## UNCERTAIN

`timeColumn.Scan` `[]byte` branch — metadata.go:233-239. Every column scanned
through `timeColumn` is timestamptz on Postgres (returns `time.Time`) or a
TEXT-declared timestamp on SQLite (modernc returns `string`); no store test scans
a `[]byte`. I cannot run the suite to prove the branch unreachable.

`sqliteNow` reachability — tables.go:78. No INSERT omits a `nowDefault` column
(every writer binds `stamp(...)`), so the default is only exercised through an
additive `ALTER ... DEFAULT`. Whether that path can fire depends on the migration
decision below.

Whether `Depot.Challenges`/`Depot.Runs` are reserved for a planned admin
surface. The admin-courses route exists without an admin-challenges one; the
deletion assumes no such route is intended.

Whether to keep the additive-migration machine at all. The greenfield law points
at reset-only, but `migrate_test.go` proves live drift (added `status`/`request`),
so this is an owner call: keep the additive ALTERs and fix the header, or drop
them and rely on `make db-reset`.

Whether the parity harness's `sharedStore` concrete-type switch
(sqlite_test.go:300-311) and raw `shared.stmt` reads (store_parity_test.go:251-262)
are acceptable test coupling or should be replaced by a narrower test seam.
