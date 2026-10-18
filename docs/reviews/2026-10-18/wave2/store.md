# store — wave 2 verification

## Verdict

Thirteen of the fifteen wave-1 items survive: nine as stated and four with a
corrected remedy. The core deletions are sound, but every one is a wider cascade
than the report drew. Deleting `Challenges`, `Runs` and `CountUsers` from the
`Store`/`schema.Depot` interfaces breaks the store package's own tests, which
call those methods through the interface, and leaves three SQL constants dead.
Wave 1 named the interface, the two AI/HTTP test depots and the parity lines, but
not `challenges_test.go`, `runs_test.go`, `sqlite_test.go`, `store_test.go`,
`httpapi/auth_test.go`, or the `userByIDSQL`/`countUsersSQL` constants. Two
Collapse items are already satisfied by the live code and drop. The final list is
one large interface-level deletion, three small doc/env fixes, and one bounded
New item about the parity method list drifting by hand.

## Confirmed actions — ordered by value

`[ ]` Delete `Store.UserByID` and its statement.
Files: `backend/internal/store/store.go:57`;
`backend/internal/store/metadata.go:436-448` and the `userByIDSQL` const at
`metadata.go:264`; `backend/internal/store/store_parity_test.go:22,120-129`.
Evidence: `grep UserByID --include=*.go` over the repo returns the definition at
`metadata.go:437`, the interface line `store.go:57`, and only
`store_parity_test.go:22,120-129`. Sessions resolve accounts through
`SessionUser` (`metadata.go:492`), and admin seeding through `UserByEmail`
(`auth/auth.go`). The parity harness is a documented seam, but `UserByID` is one
call in its script, not the seam's contract; the script keeps its coverage of the
other account reads. Safe now: nothing outside tests calls it.

`[ ]` Delete `ErrStaleSchema` and `hasLegacyMetadata`, and the branch that reads
the error.
Files: `backend/internal/store/postgres.go:30-34,46-58,73-88`;
`backend/cmd/api/main.go:437-442`; and the "stale or unreachable" phrasing in the
`reapOrphanedEnvironments` doc at `main.go:449-457`.
Evidence: `git grep ErrStaleSchema` over the whole tree returns only
`main.go:437`, `postgres.go:30,34,50,57` and the `hasLegacyMetadata` definition
at `postgres.go:73-88`. `migrate_test.go` seeds only a legacy `app.courses`
(`status`/`request`), never a pre-accounts `app.progress`. No code path in the
repo creates a `progress` table without `user_id`: `git grep app.progress` finds
one such test fixture at `dataset/roles_test.go:46`, and that test is gated on
`QC_TEST_DATABASE_URL` and never opens `store.NewPostgres`. The header at
`tables.go:8-13` states the repo is greenfield with no pre-accounts layout. The
check is a second copy of the reset policy on a path no test reaches. Safe now.

`[ ]` Delete the orphaned `// SaveEvent implements Store.` above the settings
divider.
File: `backend/internal/store/metadata.go:326`.
Evidence: the comment sits at 326, a blank line at 327, then
`/* ---------- settings ---------- */` at 328; the real `SaveEvent` and its own
comment are at `metadata.go:387-388`. Safe now: it is a comment with no function.

`[ ]` Rewrite the `tables.go` migration-policy header.
File: `backend/internal/store/tables.go:8-19`.
Evidence: the header says "there is no ALTER-based upgrade path here", while the
same file defines `additiveColumns` (`tables.go:328`) and `postgresMigrations`
(`tables.go:346`) and `sqlite.go:83` calls `migrateAdditiveColumns` from
`NewSQLite`. `migrate_test.go:47,112` prove a legacy `app.courses` opens and
gains `status`/`request`. The machine is earned, so the header is the wrong half:
keep "the DDL is final for new stores", say an existing store gains only
nullable-or-defaulted columns by `ALTER`, and keep `additiveColumns` as the one
source of the addable set.

`[ ]` Drop the stale method count in the `sqlStore` doc.
File: `backend/internal/store/metadata.go:107`.
Evidence: "the twelve methods it declares" sits above
`func (s *sqlStore)` bodies. `grep -c '^func (s \*sqlStore)' metadata.go` is 17
(16 `Store` methods plus `stmt`); the `Store` interface declares 16
context-taking methods (`store.go:51-84`) plus `Kind`/`Close`. Say "the methods
it declares arrive on both types" and let the number go.

`[ ]` Fix the `store.go` Memory description.
File: `backend/internal/store/store.go:43-44`.
Evidence: "Memory answers the same rules in process" reads as a third
implementation; `memory.go:22-41` makes `Memory` a `*SQLite` over `:memory:`, and
`sqlite_test.go:45` lists it as a SQLite factory. Say the SQLite statements run
against an in-memory database.

`[ ]` Rename `SQLiteSchema` to `sqliteSchema` (unexport).
File: `backend/internal/store/sqlite.go:32,79`.
Evidence: `grep SQLiteSchema --include=*.go` returns only `sqlite.go:32,79`
(plus `dataset.SQLiteSchemaSQL`, a different symbol). Its Postgres twin is
already unexported (`migrateSQL`, `postgres.go:28`). Safe now: no external
consumer.

`[ ]` Unexport `AppSchema` to `appSchema`.
Files: `backend/internal/store/postgres.go:24,83`;
`backend/internal/store/metadata.go:151`; `backend/internal/store/tables.go:282,291,294,298,350`;
`backend/internal/store/schema_parity_test.go:173`.
Evidence: `grep AppSchema --include=*.go` returns only store-package files and the
in-package test. No other package imports it; `docs/database-layout.md` is prose,
not a Go consumer. Unexport.

`[ ]` Cut the memory.go archaeology to one sentence.
File: `backend/internal/store/memory.go:8-17`.
Evidence: the block explains what the file "used to be" (a third hand-written
implementation) before the type it documents. State what it is now: SQLite over
an in-memory database, so degraded mode runs the same statements.

## Amended

`[ ]` Delete the dead all-lists `Challenges` and `Runs` as one cascade.
Files:
- `backend/internal/store/challenges.go:104-108` — delete `Challenges`.
- `backend/internal/store/runs.go:119-124` — delete `Runs`; keep `runsListSQL`
  (used by `runsWhere`).
- `backend/internal/schema/model.go:239,266` — drop the two interface lines, and
  fix the surrounding `Runs`/`RunsFor` comments at `model.go:258-270`.
- `backend/internal/ai/api.go:356-362` — delete `Service.Challenges`.
- `backend/internal/store/store.go:94-101` — the `schema.Depot` comment names
  `SaveRun/Run/Runs`; drop `Runs`.
- `backend/internal/store/challenges_test.go:97,186,207,235` — repoint
  `m.Challenges(ctx)` / `second.Challenges(ctx)` to `...ChallengesFor(ctx, "")`.
- `backend/internal/store/sqlite_test.go:185` and `:172,218,251` — repoint
  `st.Challenges(ctx)` / `st.Runs(ctx)` to `ChallengesFor/RunsFor(ctx, "")`.
- `backend/internal/store/runs_test.go:84,120,137,189,224,251` — repoint
  `m.Runs(ctx)` / `st.Runs(ctx)` to `RunsFor(ctx, "")`.
- `backend/internal/ai/helpers_test.go:86-88,145-147` — delete the `Runs` and
  `Challenges` methods; keep the `runsFor`/`challengesFor` helpers, which the
  `*For` methods use.
- `backend/internal/httpapi/ai_test.go:36,68` — delete the two no-op methods.

Evidence: `grep '\.Challenges\(' --include=*.go` returns only
`ai/api.go:361` and `store/challenges_test.go:97,186,207,235`,
`store/sqlite_test.go:185`; `grep '\.Runs\('` returns only
`store/runs_test.go:84,120,137,189,224,251` and `store/sqlite_test.go:172,218,251`.
The live routes run the owner-scoped form: `GET /challenges` →
`svc.ChallengesFor` (`httpapi/ai.go:359`), `GET /runs` → `depot.RunsFor`
(`httpapi/runs.go:70`). `routes.go` declares no `/admin/challenges` or
`/admin/runs`; `/admin/courses` (`routes.go:110`) uses `Courses`, which stays.
Why amended: the wave-1 remedy named the interface and the AI/HTTP test depots
but not the store package's own interface-level test calls, so deleting the
interface method would not compile. Repointing to `*For(ctx, "")` keeps every
ordering, owner and upsert assertion. `ChallengesFor`/`RunsFor` already return
the full set for an empty owner, so coverage is unchanged.

`[ ]` Delete `Store.CountUsers` and its statement.
Files: `backend/internal/store/store.go:58`;
`backend/internal/store/metadata.go:450-457` and the `countUsersSQL` const at
`metadata.go:266`; `backend/internal/store/store_test.go:85-87`;
`backend/internal/store/store_parity_test.go:22,131-133`;
`backend/internal/httpapi/auth_test.go:268-270`.
Evidence: `grep CountUsers --include=*.go` returns the definition, the interface
line, `store_test.go:85`, `store_parity_test.go:131-133`, and
`httpapi/auth_test.go:268`. No handler or boot path reads it. Why amended: the
wave-1 remedy stopped at the method and interface line. The HTTP test asserts
"exactly one admin after two idempotent seeds"; replace the call with the live
`st.UserIDs(ctx)` and check `len(ids) == 1`. Without that, the test does not
compile.

`[ ]` Fix the parity-harness coverage claim, and make the list honest.
File: `backend/internal/store/store_parity_test.go:18-19,265` and the
`storeMethods` list at `:20-25`.
Evidence: the comment calls `storeMethods` "the 14 methods of Store", but `Store`
declares 16 context-taking methods (`store.go:51-84`) plus `Kind` and `Close`
(`store.go:90,92`); the list omits `UserSetting`, `SaveUserSetting`, `Setting`,
`SaveSetting`, and `grep Setting store_parity_test.go` returns nothing. Why
amended: wave 1 said "Store declares 16 methods", which drops `Kind`/`Close` from
the denominator. Name the covered set as it actually is, and either add the four
settings calls to the script or say the script covers accounts/progress/sessions
only. The list also loses `UserByID` and `CountUsers` once those actions land.

## Dropped

- Timestamp format "written twice" (`sqliteNow` `tables.go:78` vs `timestamp()`
  `sqlite.go:151`): the comment at `tables.go:75-77` already names `timestamp()`
  and explains that the strftime form plus three zeroes equals its fixed-width
  microsecond text, so the proposed "one comment naming the pair" is already
  there. The alternative, driving the SQL default from the Go format constant, is
  not possible across the two format languages (`%Y-%m-%dT%H:%M:%f` vs
  `2006-01-02T15:04:05.000000Z`).
- Duplicated file rationale (`courses.go:13-16`, `runs.go:13-16`): both already
  say "(see the header of challenges.go)" and add one table-specific sentence.
  The remedy describes what the files already do.

## New

`[ ]` `storeMethods` is a hand-maintained copy of the interface and has already
drifted (14 listed, 18 declared). Either derive the required set from
`reflect.TypeOf((*Store)(nil)).Elem()` and subtract the methods the script
intentionally does not run, or drop the "every method ran" assertion and let the
step-by-step comparison carry the test.
File: `backend/internal/store/store_parity_test.go:20-25,280-287`.
Evidence: the drift is the same defect wave 1 filed at items 12; the comment says
14 while `store.go` declares 18, and the assertion at `:280-287` is the only
thing that would notice a new interface method. This is the bounded extra in the
domain; no other dead symbol surfaced in the production files (`grep -c '^func (s
\*sqlStore)' metadata.go` = 17, all live after the two cascades above).

## Still uncertain — needs runtime proof

- `timeColumn.Scan` `[]byte` branch (`metadata.go:233-239`): every value scanned
  through it is `time.Time` from pgx or `string` from SQLite; no store test scans
  a `[]byte`. Unchanged from wave 1; only running the suite with a driver that
  returns bytes would prove it unreachable.
- `sqliteNow` reachability (`tables.go:78`): only fires through an additive
  `ALTER ... DEFAULT` when a `nowDefault` column is added to an existing table.
  The migration machine is tested, the default-value path is not.
- Whether `Challenges`/`Runs` are reserved for a planned `/admin/challenges` or
  `/admin/runs`. The deletions above assume no such route is intended; the
  owner-scoped equivalent already ships.
- The parity harness's `sharedStore` concrete-type switch
  (`sqlite_test.go:300-311`) and raw `shared.stmt` reads
  (`store_parity_test.go:251-262`): test coupling or a narrower seam is an owner
  call, not proofable from code.
