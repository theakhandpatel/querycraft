# envdb-dataset — wave 2 verification

## Verdict

Most of wave 1 survives. The two perf-name/digest collapses, the `loaderFor`
collapse, the four spike-only dataset helpers, and the five test-only dialect
exports all re-check against the current code. Two wave-1 deletes are wrong: the
`ConnectAsLearner` and `LoadIntoTemplate` functions back a real regression test
of the read-only versus writable grant sets (`writable_test.go`), so they move
into the test package instead of being deleted. One wave-1 collapse is dropped:
the `dialect.Profile` fields have live test readers and are not a second truth.
The wave-1 report also missed the package-level `dialect.ClassifyError`, which
has the same zero-production-caller status as the five exports it did name.

Counts: 12 confirmed, 4 amended, 1 dropped, 2 new.

## Confirmed actions

- [ ] Carry the fixture loader on `envdb.envKind` and delete `loaderFor`;
  `ensure` and `reset` call `kind.load(m)`, and the two bootstrap paths call
  `challengeEnv.load(m)` / `perfEnv.load(m)`. Files:
  `backend/internal/envdb/manager.go` (`:212-214`, `:232-234`, `:380`, `:486`,
  `:555-568`). Evidence: `grep -rn --include='*.go' -w loaderFor backend/` returns
  only `manager.go:380,486,559`; `docs/target-architecture.md:54` and
  `docs/reviews/2026-10-17/03_engine_store_config.md:84-97` already specify this.
  Safe now because `envKind` already owns `template`, and the loader is the only
  per-kind difference left.

- [ ] Fix `dataset.perfTablesResponse` so the perf drawer reports the perf
  schema. Give the four perf relations their own column description (or read
  `perf.sql`), then update the test that currently enshrines the drift. Files:
  `backend/internal/dataset/dataset.go` (`:84-132`), `perf.sql` (`:85-95`),
  `perf_test.go` (`:304-313`). Evidence:
  `grep -n '"carrier"\|"tracking_code"\|"shipping_address"' backend/internal/dataset/perf.sql`
  shows five `orders` columns the drawer never emits; `dataset.go:97-100` claims
  "the same columns and types". Safe because this is a wrong answer today, not a
  refactor.

- [ ] Delete `envdb.NewSQLiteEnvironments`; call
  `NewSQLiteEnvironmentsWithOptions(root, SQLiteOptions{})` from the test, and
  fix the doc at `sqlite.go:65-67`. Files:
  `backend/internal/envdb/sqlite.go` (`:129-135`),
  `backend/internal/httpapi/dialect_test.go` (`:25`). Evidence:
  `grep -rn --include='*.go' -w NewSQLiteEnvironments backend/` shows the
  definition plus `dialect_test.go:25` and the doc comment; `cmd/api/main.go:256`
  calls `WithOptions`. Safe because no production path enters the zero-option
  form.

- [ ] Delete `dialect.Names`, `dialect.ByName`, `dialect.AffinityClasses`,
  `dialect.Affinity`, `dialect.ErrorCodes`; read the profile through
  `dialect.SQLite()`, read the enum from the `Code*` constants, and update the
  docs. Files: `backend/internal/dialect/dialect.go` (`:45-58`, `:173-216`),
  `dialect_test.go` (`:32-52`, `:60-70`, `:207`, `:482-525`, `:572`),
  `docs/sqlite-dialect.md` (`:14-15`, `:112-120`, `:213-218`),
  `backend/README.md` (`:427-430`). Evidence:
  `grep -rn --include='*.go' -w Names\|ByName\|AffinityClasses\|ErrorCodes backend/`
  shows production hits only for `metrics.Names`/`metrics.Registry`; `Affinity`
  appears only in `dialect_test.go`; `engine/sqlite.go:524` labels a column from
  the driver's declared type. Safe because each symbol restates a fact the
  profile file or the `Code*` constants already hold, and
  `scripts/sqlite-error-codes.mjs` and `scripts/dataset-map-audit.mjs` check the
  profile independently.

- [ ] Delete `dataset.EnvironmentDatabaseSize`, `dataset.ServerConnectionFacts`
  and `dataset.ConnectionFacts`; drop their calls in the spike test. Files:
  `backend/internal/dataset/template.go` (`:292-328`),
  `template_test.go` (`:107-113`, `:187-196`, `:383`). Evidence:
  `grep -rn --include='*.go' -w EnvironmentDatabaseSize\|ServerConnectionFacts\|ConnectionFacts backend/`
  returns the definitions and `template_test.go` only. Safe because
  `TestTemplateEnvironmentSpike` is a measurement spike, not a regression, and
  the reaper drops orphans by name.

- [ ] Collapse `envdb.digest` and `envdb.accountFileStem` into one helper; the
  file stem becomes `digest(accountID)[:32]`. Files:
  `backend/internal/envdb/manager.go` (`:818-821`),
  `backend/internal/envdb/sqlite.go` (`:238`, `:276`, `:468-477`). Evidence:
  `hex.EncodeToString(sum[:16])` is byte-identical to `digest(id)[:32]`; both
  reads at `sqlite.go:238,276` go through the one helper. Safe because the two
  expressions already produce the same string.

- [ ] Compute the perf keep-name in `ReapUnreferenced` from the kind, not a
  second literal: `keep = append(keep, perfEnv.database(m, id))`. Files:
  `backend/internal/envdb/manager.go` (`:335`, `:798`). Evidence: both lines
  build `dataset.EnvironmentDatabaseName("perf_"+digest(id)[:32])`; a change to
  `perfEnv.database` without the reaper would drop live plan environments. Safe
  because the two expressions are the same fact.

- [ ] Keep one perf scale name. Drop the alias `PerfScaleFactor` and have
  `FactRowCount` return `PerfScaleFactorOrders` (or the reverse), updating the
  four test reads. Files: `backend/internal/dataset/scale.go` (`:17-33`),
  `dataset/perf.go` (`:38`), `httpapi/dataset_test.go` (`:262-263`, `:338-339`),
  `dataset/scale_test.go` (`:43`). Evidence:
  `grep -rn --include='*.go' PerfScaleFactor backend/` shows
  `scale.go:22` is a pure alias. Safe because both names hold one number.

- [ ] Move `(*SQLiteEnvironments).cachedAccounts` into the test file; the test
  package is `package envdb`, so it can keep the lock-taking helper. Files:
  `backend/internal/envdb/sqlite.go` (`:169-175`),
  `sqlite_cache_test.go` (`:163`, `:167`, `:260`, `:296`, `:356`). Evidence:
  `head -3 backend/internal/envdb/sqlite_cache_test.go` is `package envdb`;
  `grep -rn --include='*.go' cachedAccounts backend/` has no non-test read. Safe
  because the method is unexported and test-only.

- [ ] Read the dialect profile verbatim for `max_safe_integer`, `non_finite`
  and the blob `prefix`; validate the keys once in `mustDecode` instead of
  defaulting at each call. Files: `backend/internal/dialect/sqlite.go` (`:98`,
  `:179-184`, `:190-210`, `:214-219`). Evidence:
  `contracts/dialects/sqlite.json` sets all three keys, and
  `TestEmbeddedProfileMatchesContract` pins the mirror. Safe because a missing
  key is a decode fault, not a runtime condition.

- [ ] Unexport `dataset.ByID` or inline the `id != "commerce"` check into
  `Tables`. Files: `backend/internal/dataset/dataset.go` (`:73-79`, `:144-151`).
  Evidence: `grep -rn --include='*.go' 'ByID(' backend/` shows only the
  definition and `Tables` (`:148`). Safe because nothing outside the package
  calls it.

- [ ] Split `backend/internal/dataset/dataset.go` into `commerce.go` (the
  generator) and `tables.go` (`Tables`/`Info`/`stringify`). Files:
  `dataset/dataset.go` (451 lines). Evidence: the file holds the model at
  `:34-55`, the generator at `:59-451`, and response building from `:143`. Safe
  because this is file hygiene with no behaviour change.

## Amended

- [ ] `dataset.ConnectAsLearner` — do not delete. The claim "no production
  caller" is true, but the consumer is not a spike: `writable_test.go` builds
  the writable and the read-only environments through it and asserts the
  withheld verbs (`TRUNCATE`, `DROP`, `CREATE SCHEMA`), the ACL invariance, and
  the read-only refusals (`writable_test.go:78`, `:109-184`, `:166`).
  `envdb.probeWritable` checks only `INSERT` on `customers`, so it does not cover
  that test. Change: move the function into a `dataset_test` helper
  (`template_test.go` and `writable_test.go` share `package dataset_test`), or
  keep it as the documented seam its own comment claims. Files:
  `backend/internal/dataset/template.go` (`:77-95`), `template_test.go`,
  `writable_test.go`.

- [ ] `dataset.LoadIntoTemplate` — do not delete. `writable_test.go:149-184`
  uses it to build the read-only template and then asserts that the copy refuses
  every write. That is the regression for the shared read-only dataset the
  gateway's own database uses. Change: keep the exported loader, or inline
  `SchemaSQL()` + `SeedSQL()` + `EnsureLearnerRole` into the test helper the way
  `localdb.go:293-300` builds the same read-only dataset. Files:
  `backend/internal/dataset/template.go` (`:125-142`), `writable_test.go:155`.

- [ ] `dialect.Profile.EncodeValue` unreachable arms — delete the `bool`,
  `int`/`int8`/`int16`/`int32`, unsigned, `float32` and `time.Time` arms and
  `encodeUint`, but also update the contract doc. The shipped read driver returns
  only `int64`, `float64`, `string`, `[]byte`, `nil` (`sqlite_raw.go:779-794`),
  so the claim holds. The amended part: `docs/sqlite-dialect.md` Table 2
  (`:144-145`) documents the boolean and `time.Time` answers as the encoder's
  defensive rule, and `dialect_test.go:553-555` pins them. Delete those rows and
  cases with the arms. Files: `backend/internal/dialect/sqlite.go`
  (`:112-158`), `dialect_test.go` (`:527-564`), `docs/sqlite-dialect.md`.

- [ ] Move `dataset/template.go` into `envdb` — amend the boundary. Move only
  the database-level DDL (`CreateDatabase`, `CreateDatabaseFromTemplate`,
  `SealTemplate`, `DropDatabase`, `ResetEnvironment`,
  `ListEnvironmentDatabases`, `ReapEnvironmentDatabases`, `execDDL`). Keep the
  fixture loaders in `dataset` (`LoadIntoTemplate`, `LoadWritableIntoTemplate`,
  `loadIntoTemplate`, `grantWritableDataset`, `ConnectToDatabase`), because
  `dataset/perf.go:56` also calls `ConnectToDatabase` and the loaders are
  dataset content. Files: `backend/internal/dataset/template.go` (`:100-290`),
  `perf.go` (`:56`), `backend/internal/envdb/manager.go`. The wave-1 list named
  the loaders as lifecycle, which would drag `perf.go` or duplicate
  `ConnectToDatabase`.

## Dropped

- `dialect.Profile` unread fields (`Engine`, `Capabilities`, `CapabilityNotes`,
  `Types`, `Source`) — dropped. `dialect_test.go:78-155` reads every one of
  them (`p.Engine`, `p.Source`, `p.Capabilities`, `p.CapabilityNotes`,
  `p.Types`) to pin the profile contract, and `Decode`'s own doc
  (`dialect.go:163-164`) names the tests as a consumer. These fields are not a
  second truth: the contract JSON is the one truth and the struct is the typed
  reader, unlike `Affinity`/`ErrorCodes`/`Names`, which restate data as code.

## New

- [ ] Delete the package-level `dialect.ClassifyError` (`sqlite.go:52`) or
  reduce it to a test helper; production classifies through
  `Profile.ClassifyError` (`engine/sqlite.go:298`). Evidence:
  `grep -rn --include='*.go' 'dialect.ClassifyError\|func ClassifyError' backend/`
  shows the definition, `dialect_test.go:350`, and engine tests
  `engine/sqlite_test.go:555,797,830,868,1040`; production calls the method at
  `engine/sqlite.go:298`. Files: `backend/internal/dialect/sqlite.go`,
  `dialect_test.go`, `engine/sqlite_test.go`. Wave 1 named the other five
  exports but missed this one; `docs/backend-decomposition-gemini4-argon.md:331`
  already lists it as test-only.

- [ ] One list of perf relations, not two. `dataset/perf.go:80` (`perfTables`)
  and `dataset/dataset.go:84-92` (`perfTableRows`) both name the same four
  relations in the same order. Evidence:
  `grep -rn --include='*.go' 'perfTables\b' backend/` returns `perf.go:80,92`
  and no other source. Fold the names into one list so the ownership transfer
  and the drawer cannot disagree. Files: `backend/internal/dataset/perf.go`,
  `dataset/dataset.go`.

## Still uncertain

- Deleting the five `dialect` exports and `EncodeValue` arms needs a policy
  call, because `docs/sqlite-dialect.md` names them as the "executable half" and
  `backend/README.md:427-430` records them as deliberately left. The code facts
  are settled; the owner decides whether the doc coverage outranks the line
  count.
- The `ConnectAsLearner`/`LoadIntoTemplate` move needs a `go test` run to prove
  the two external test files still compile against a single `dataset_test`
  helper; no behaviour changes.
