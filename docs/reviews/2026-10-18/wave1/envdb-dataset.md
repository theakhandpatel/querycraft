# envdb-dataset — slop audit (wave 1)

## Verdict

The three packages each have a defensible centre, but `dataset` is the one that
does not hold to its own doc: the package says it owns the rebuildable
`commerce` fixture (`dataset/dataset.go:1-4`), yet `dataset/template.go` (328
lines) is PostgreSQL environment lifecycle (create/seal/drop/reap) that `envdb`
calls, and its package doc never admits it. The clearest slop is a block of
exported functions with no live consumer at all: `dataset.ConnectAsLearner`,
`LoadIntoTemplate`, `EnvironmentDatabaseSize`, `ServerConnectionFacts`, and
`dialect.Names`/`ByName`/`AffinityClasses`/`Affinity`/`ErrorCodes`. Four of them
carry a `TEST/SPIKE-ONLY` comment that admits it. The real earners are the
`envdb` lock/in-flight/reaper machinery, the `Scratch`/`Drop` AI seam, the
fixture plumbing `envdb` genuinely calls, and the dialect profile reader, all of
which have live call sites in `cmd/api`, `httpapi`, `ai` or `engine`. Within
that, three facts are written twice inside the same package: the perf database
name, the account-id hash, and the perf row counts / column list. `go build
./internal/envdb/ ./internal/dataset/ ./internal/dialect/` is green.

## Delete

- `envdb.NewSQLiteEnvironments` — `backend/internal/envdb/sqlite.go:133`.
  Exists as a zero-option convenience over
  `NewSQLiteEnvironmentsWithOptions`. The gateway never calls it: it calls
  `NewSQLiteEnvironmentsWithOptions(sqliteDataDir, sqliteOptions(settings))`
  (`backend/cmd/api/main.go:256`). Only callers are
  `backend/internal/httpapi/dialect_test.go:24`; its own doc says it exists "so
  NewSQLiteEnvironments and the existing tests need no change"
  (`sqlite.go:65-67`). Remedy: delete it, have the test call `WithOptions`.

- `envdb.(*SQLiteEnvironments).cachedAccounts` — `backend/internal/envdb/sqlite.go:171`.
  Unexported, no production caller; the only readers are
  `backend/internal/envdb/sqlite_cache_test.go:163,167,260,296,356`. Remedy:
  move the method into the test file (same package).

- `dataset.ConnectAsLearner` — `backend/internal/dataset/template.go:85`.
  Doc admits `TEST/SPIKE-ONLY` (`template.go:81-84`). No production caller:
  `grep -rn 'ConnectAsLearner' backend --include='*.go' | grep -v _test` returns
  only the definition. Test callers: `template_test.go:206,268,306,564`,
  `writable_test.go:78,166`. Remedy: delete; the probe it supports is already
  done in production by `envdb.probeWritable` (`manager.go:725`).

- `dataset.LoadIntoTemplate` — `backend/internal/dataset/template.go:140`.
  Doc admits `TEST/SPIKE-ONLY` (`template.go:136-139`). No production caller;
  the shared read-only dataset is built by `localdb` directly from
  `dataset.SchemaSQL()`+`SeedSQL()`+`EnsureLearnerRole`
  (`backend/internal/localdb/localdb.go:294-296,267`), not through this
  function. Test callers: `template_test.go:121`, `writable_test.go:155`.
  Remedy: delete the read-only variant (keep `LoadWritableIntoTemplate`).

- `dataset.EnvironmentDatabaseSize` — `backend/internal/dataset/template.go:298`.
  Doc admits `TEST/SPIKE-ONLY` (`template.go:296-297`). No production caller;
  test callers `template_test.go:187,191,383`. Remedy: delete.

- `dataset.ServerConnectionFacts` and `dataset.ConnectionFacts` —
  `backend/internal/dataset/template.go:315` and `:308`. No production caller;
  the only reader is `template_test.go:107`. Unlike the three above this pair
  carries no spike-only label, which is itself evidence the label was forgotten.
  Remedy: delete both.

- `dialect.Names` — `backend/internal/dialect/dialect.go:175`. Returns the
  literal `[]string{"sqlite"}`. Production reaches the profile through
  `dialect.SQLite()` (`backend/internal/engine/sqlite.go:253`); only
  `dialect_test.go:33` calls `Names`. Remedy: delete.

- `dialect.ByName` — `backend/internal/dialect/dialect.go:179`. A one-case
  switch no production path enters; only `dialect_test.go:36,46,49`. Remedy:
  delete, callers keep `SQLite()`.

- `dialect.AffinityClasses` — `backend/internal/dialect/dialect.go:190`.
  Returns a literal list; only `dialect_test.go:138,491,514,572`. Remedy: delete.

- `dialect.Affinity` — `backend/internal/dialect/dialect.go:202`. The SQLite
  affinity algorithm reimplemented in Go. No production caller: column labels
  come from the driver's declared type
  (`backend/internal/engine/sqlite.go:533-545`), and values are encoded from
  the storage class with the affinity argument deliberately ignored
  (`sqlite.go:555-558,581`). Only `dialect_test.go:482-522`. Because the
  algorithm is also stated in the profile (`affinity_rules`), this is a second
  truth as well. Remedy: delete, and drop `TestAffinity`/the affinity half of
  the profile check or read the algorithm from the profile.

- `dialect.ErrorCodes` — `backend/internal/dialect/dialect.go:46`. Lists the
  same codes the `Code*` constants already declare one line above. No
  production caller (`grep 'ErrorCodes()' backend --include='*.go' | grep -v
  _test` empty); only `dialect_test.go:67,207`. The contract test already pins
  the enum from source text (`backend/internal/httpapi/contract_test.go:1175`,
  `goCodeConstants` at `:1362`), so `TestErrorCodesAreClosed`
  (`dialect_test.go:60`) is tautological. Remedy: delete the function and the
  tautological test; the closure check reads the constants directly.

- `dialect.Profile.EncodeValue` unreachable arms —
  `backend/internal/dialect/sqlite.go:116-157`. The shipped read driver returns
  only `int64`, `float64`, `string`, `[]byte`, `nil`
  (`backend/internal/engine/sqlite_raw.go:786-791`, the `sqlite3_column_type`
  switch). It never produces `bool`, `int`/`int8`/`int16`/`int32`, unsigned
  types, `float32`, or `time.Time` (the raw driver exists precisely to avoid
  modernc's DATE→time.Time coercion). Only `TestEncodeValue`
  (`dialect_test.go:527`) feeds those classes. Remedy: keep the `nil`, `int64`,
  `float64`, `string`, `[]byte` arms; delete the rest and their test cases.

- `dialect` profile-default fallbacks — `backend/internal/dialect/sqlite.go:98`
  (`defaultMaxSafeInteger`), `:179-184` (`maxSafeInteger`), `:193-207`
  (`encodeFloat` fallback strings), `:215-218` (`encodeBlob` backslash prefix).
  Each guards a key the shipped profile always sets:
  `contracts/dialects/sqlite.json:108` (`max_safe_integer`), `:118-121`
  (`non_finite`), `:134` (`prefix`). Remedy: read the profile verbatim; if a
  missing key must fail, validate it in `mustDecode` (`sqlite.go:28`) once
  instead of defaulting at every call.

## Collapse

- `envdb.loaderFor` vs `envdb.envKind.template` — `backend/internal/envdb/manager.go:559`
  and `:317-338`. The kind already knows its template name, and `ensure`
  (`:380`) and `reset` (`:486`) re-derive the loader from that name string by
  comparing it to `opts.PerfTemplateName`. Two truths for "which fixture loads
  into which template". Remedy is the one in `docs/target-architecture.md:54`
  and review 03 Finding 3: carry `load func(context.Context, string) error` on
  `envKind` and delete `loaderFor`; still unlanded.

- `envdb.digest` vs `envdb.accountFileStem` — `backend/internal/envdb/manager.go:818`
  and `backend/internal/envdb/sqlite.go:474`. Two SHA-256 account-id helpers in
  one package; the second is the first truncated to 16 bytes. One fact ("hash
  the account id to a safe name") written twice. Remedy: one helper; the file
  stem becomes `digest(accountID)[:32]` (matching the database name's use at
  `manager.go:335,770`).

- Perf environment database name — `backend/internal/envdb/manager.go:335`
  (`perfEnv.database`) and `:798` (`ReapUnreferenced`). The reaper reconstructs
  the perf name with the same `dataset.EnvironmentDatabaseName("perf_" +
  digest(id)[:32])` expression the kind already owns. If one changes, the
  reaper drops live plan environments. Remedy: call the kind's `database`
  function (or one shared `perfDatabaseName(id)`).

- `dataset.PerfScaleFactor` vs `dataset.PerfScaleFactorOrders` —
  `backend/internal/dataset/scale.go:22` and `backend/internal/dataset/perf.go:38`.
  `PerfScaleFactor = PerfScaleFactorOrders` is a pure alias; `FactRowCount`
  (`scale.go:28`) returns the former, `perfTableRows` (`dataset.go:90`) uses the
  latter. One number, two exported names. Remedy: keep one.

- Perf column list vs `perf.sql` — `backend/internal/dataset/dataset.go:108`
  (`perfTablesResponse`). It builds the perf tables drawer's columns from
  `Commerce()` (`:109-127`) while `dataset/perf.sql:85-110` declares `orders`
  with five extra columns (`carrier`, `tracking_code`, addresses, note). The
  same fact (the perf schema) is described twice and already drifted; this is
  review 03 Finding 8, still unlanded. `perfTableRows` (`dataset.go:84`) also
  restates the perf row counts from `perf.sql` comments. Remedy: drive the
  drawer from one description of the perf schema, or drop the derived preview.

- `dialect.Profile` unread fields — `backend/internal/dialect/dialect.go:152-160`.
  `Engine`, `Capabilities`, `CapabilityNotes`, `Types`, `Source` are decoded and
  never read by production (`grep -rn 'profile\.\(Engine\|Capabilities\|...\)'
  backend --include='*.go' | grep -v _test` empty; the only production reads are
  `profile.Dialect`, `profile.EngineLabel`, `profile.ValueEncoding`,
  `profile.ErrorClassification`). The web reads them from the contract file, not
  from Go. Remedy: drop the Go fields (`json.Unmarshal` ignores unknown keys) or
  state why the mirror is complete.

- `dataset.ByID` — `backend/internal/dataset/dataset.go:74`. Exported lookup
  whose only caller is `Tables` in the same file (`dataset.go:148`). No
  external caller. Remedy: unexport, or inline the `id != "commerce"` check
  into `Tables`.

## Move / split

- `backend/internal/dataset/template.go` (328 lines). Its contents
  (`ConnectToDatabase`, `CreateDatabase`, `CreateDatabaseFromTemplate`,
  `SealTemplate`, `DropDatabase`, `ResetEnvironment`, `ListEnvironmentDatabases`,
  `ReapEnvironmentDatabases`, `EnvironmentDatabaseSize`, `ConnectionFacts`,
  `ServerConnectionFacts`) are PostgreSQL environment lifecycle, not dataset
  content. The package doc (`dataset/dataset.go:1-4`) claims the package owns
  the fixture and introspection, and `envdb`'s doc (`envdb/manager.go:14-17`)
  claims it owns "the sealed templates and the environment databases". The
  split is real. Dependency direction already runs `envdb → dataset`, so moving
  the lifecycle into `envdb` (a `lifecycle.go`) is feasible. File hygiene only:
  no behaviour change.

- `backend/internal/dataset/dataset.go` (451 lines) mixes the `Dataset` model
  (`:34-55`), the `commerce` generator (`:59-451`), and response building
  (`Tables`/`Info`/`stringify`). Split generator from response into
  `commerce.go` and `tables.go`. File hygiene only.

## Earned (checked, keep)

- `envdb` manager lock discipline, in-flight marker, reaper — proven by
  `manager_close_test.go`, `stall_proxy_test.go` and ADR 0002 timing comments;
  `Ensure`/`Reset` are distinct enough that collapsing them would hide the
  build/reset split.
- `envdb.(*Manager).Scratch`/`Drop` — live: `ai/pipeline.go:558`,
  `ai/review.go:178`, `ai/course_pipeline.go:37` call `Runner.Scratch`.
- `envdb.Facts` + `resetAt` map — live: read by
  `httpapi/environment.go:77,289`.
- `envdb.(*SQLiteEnvironments).Root/MaxAccounts/IdleTTL` — live:
  `cmd/api/main.go:266-268`.
- SQLite bound/LRU/idle sweep — live (`ForAccount`/`Reset` from
  `httpapi/dialect.go:102`, `httpapi/environment.go:329`) and pinned by
  `sqlite_cache_test.go`.
- `dataset.EnvironmentDatabaseName`, `CreateDatabase`,
  `CreateDatabaseFromTemplate`, `SealTemplate`, `DropDatabase`,
  `ResetEnvironment`, `ReapEnvironmentDatabases`, `ListEnvironmentDatabases` —
  called from `envdb/manager.go:252,259,537,657,685,809`.
- `dataset.LoadWritableIntoTemplate`, `LoadPerfIntoTemplate` — called from
  `envdb/manager.go:213,233`.
- `dataset.SchemaSQL`, `SeedSQL`, `SQLiteSchemaSQL`, `SQLiteSeedSQL` — applied in
  `localdb/localdb.go:294-295` and `envdb/sqlite.go:431-432`.
- `dataset.EnsureLearnerRole`, `LearnerRole`, `SchemaName` — `localdb.go:267`
  and `envdb/manager.go:697,709,715`.
- `dataset.Commerce`, `Tables`, `TableInfo`, `TablesResponse` — live:
  `httpapi/handlers.go:258`, `ai/pipeline.go:895`.
- `dataset.FactRowCount` and the scale constants — live: `httpapi/plan.go:115`.
- `dataset.RenderSchemaSQL`/`RenderSeedSQL` — the only writer of the embedded
  `schema.sql`/`seed.sql`, run by `make dataset` (`Makefile:271`); checked, but
  they are reachable only through tests.
- `dialect.SQLite`, `Profile.ClassifyError`, `Decode` — live:
  `engine/sqlite.go:253,298`.
- `dialect` embedded `sqlite.json` mirror — `go:embed` cannot contain `..`;
  `diff backend/internal/dialect/sqlite.json contracts/dialects/sqlite.json`
  exits 0, and `TestEmbeddedProfileMatchesContract` pins it.
- `dataset/schema.sqlite.sql`, `seed.sqlite.sql`, `mapping.json` — checked by
  `scripts/dataset-map-audit.mjs` (`make audit-dataset-map`) and
  `dataset/perf_test.go`.
- `envdb.Options.withDefaults` and the three metric helpers — live via
  `cmd/api/main.go:205-219` and `manager.go:928-948`.

## UNCERTAIN

- Whether `dialect.Names`/`ByName`/`AffinityClasses`/`Affinity`/`ErrorCodes`
  count as a documented seam the owner wants kept: `docs/sqlite-dialect.md`
  describes them as "the executable half" of the doc, so deleting removes
  coverage the project may value more than the line count.
- `dialect.Profile.EncodeValue` is exported; deleting its non-driver arms changes
  the function's behavior for any future caller that hands it `bool` or
  `time.Time`. Owner call on whether the general encoder is contract surface.
- Moving `dataset/template.go` into `envdb` needs a build after the move to prove
  no import cycle; I did not attempt it (no edits allowed).
- `dataset.ByID` may be intended as public API for a second dataset; there is no
  live consumer today (`Turbo`/perf go through `Tables` and `PerfDatasetID`).
- Whether `RenderSchemaSQL`/`RenderSeedSQL` count as having a live consumer: they
  run only under `go test -update`; the owner decides if a regeneration target
  is a consumer.
