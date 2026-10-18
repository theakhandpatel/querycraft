# platform — slop audit (wave 1)

Scope: `backend/cmd/api/**`, `backend/internal/{config,localdb,auth,metrics,webui,pgtest,schema}/**`.
`go build ./cmd/... ./internal/...` is green (exit 0). Every claim below carries the
`grep`/`sed` that produced it. Read-only; no file was edited except this report.

## Verdict

The substrate is earned: `config.applyKey` is the file schema and every key has a
non-test reader (37 fields, each `settings.<Field>` count >= 2), the login throttle
lives once in `httpapi/login_throttle.go` with its defaults owned by `httpapi` and
read back by `config.Defaults`, and `webui`, `pgtest` and `auth` are each read by a
live caller I traced. The real slop is three things. First, dead surface in `schema`:
`Artifact.LessonID` has no writer, `Outcome.StageIndex` has only test callers, and
`Depot.Challenges`/`Depot.Runs` have no production route (the list endpoints call the
`*For` variants). Second, a fact written twice in `cmd/api`: `dbStatus` re-derives the
metadata-store selection and the AI provider vocabulary that `newStore` and
`aiProviderFrom` already own, so `db status` can lie. Third, doc comments assert a
configuration environment layer that does not exist in the code. `localdb.Server.adopted`
is a write-only field whose docs and test describe behaviour `Stop` does not implement.
`run()` is still one ~320-line function; the target architecture already names its split.

## Delete

- `localdb.Server.adopted` — `internal/localdb/localdb.go:49`. Assigned once
  (`localdb.go:170`) and never read: `Stop` gates on `s.pg == nil` (`localdb.go:307`),
  not on `adopted`. `grep -rn -w adopted backend` returns the field decl, the
  assignment, the log line, the doc, the test, and a comment in another package.
  The field's doc (`localdb.go:46-48`) and `Stop`'s doc (`localdb.go:305`) both claim
  Stop keys off adoption; it does not. Remedy: delete the field and fix both comments
  to say an adopted server has no `pg` handle. Also rewrite `TestStopIsNoOpForAdoptedServer`
  (`internal/localdb/localdb_test.go:40-46`): it builds `&Server{adopted: true}` with a
  nil `pg`, so it proves the nil-handle path, not the adoption path.

- `schema.Outcome.StageIndex` — `internal/schema/model.go:194-202`. `grep -rn StageIndex`
  over the whole repo returns four hits: the doc, the method, and two calls in
  `internal/ai/helpers_test.go:372,380`. No production caller. Remedy: delete the method
  and inline the loop into the two test helpers, or have the helpers index a local map.

- `schema.Artifact.LessonID` — `internal/schema/model.go:126`. No writer:
  `grep -rn 'LessonID\|lesson_id' backend` returns the field, a different field on
  `eval.Contract` (`internal/eval/eval.go:81`), and two `apidocs` schema strings. The
  web reads `lesson_id` only on exercise cells and plans (`web/src/api/planContract.ts:186`,
  `web/src/components/SqlCell.tsx:180`, `web/src/api/execution/http.ts:422`), never on an
  artifact. The field is always `""`. Remedy: delete it and drop `lesson_id?` from
  `web/src/api/aiContract.ts:70` and the OpenAPI artifact schema. If an out-of-repo
  producer sets it, that producer does not exist here (see UNCERTAIN).

- `schema.Depot.Challenges` — `internal/schema/model.go:239`, with its only caller
  `ai.Service.Challenges` (`internal/ai/api.go:357-362`). The route that would use it,
  `GET /challenges`, calls `svc.ChallengesFor` (`internal/httpapi/ai.go:359`, route at
  `internal/httpapi/routes.go:117`). `grep -rn '\.Challenges('` (non-store, non-schema)
  returns only `internal/ai/api.go:361`; no test calls `Service.Challenges` either.
  Remedy: delete the interface method, the `Service` method, and the three store
  implementations plus the store tests that pin them.

- `schema.Depot.Runs` — `internal/schema/model.go:266`. `grep -rn '\.Runs('` returns only
  store tests (`internal/store/runs_test.go:84,120,137,189,224,251`,
  `internal/store/sqlite_test.go:172,218,251`). `GET /runs` uses `RunsFor`
  (`internal/httpapi/runs.go:70`), and `routes.go:124` declares no `/admin/runs`. Remedy:
  delete the method, its three store implementations, and the tests that exist only to
  exercise it.

- `webui` empty-FS fallback: `subErr`/`sub == nil` and `emptyFS` — `internal/webui/webui.go:24`,
  `:89-94`, `:123-125`. `//go:embed all:dist` (`webui.go:19-20`) compiles only when `dist`
  has an embeddable file, and `dist/.gitkeep` is committed (`git ls-files` lists it;
  `.gitignore:17-18` keeps it). `fs.Sub` then cannot fail, so `subErr` is always nil and
  `emptyFS` is unreachable. Remedy: `var sub, _ = fs.Sub(dist, "dist")`, return `sub` from
  `distFS`, delete `emptyFS`.

- `metrics.Registry.Add` — `internal/metrics/metrics.go:148`. `grep -rn '\.Add('` shows the
  only caller is `Inc` on the next line (`metrics.go:145`). Nothing outside the package
  calls it. Remedy: unexport to `add`, or inline it into `Inc`.

- Environment-layer doc fictions — `internal/config/config.go:30-31`, `:117-118`,
  `cmd/api/main.go:44-45`, `internal/localdb/localdb.go:8`. No configuration environment
  layer exists: `grep -rn 'Getenv\|LookupEnv' backend` returns only
  `internal/pgtest/pgtest.go:131` and the `$TMPDIR` swap at `internal/localdb/localdb.go:145`.
  `config.Settings` comments name no environment variable, and `Load` applies defaults
  then the file only. `localdb.go:8` points readers at `QC_DATABASE_URL`, a name no code
  reads (the key is `database_url`; `internal/engine/unavailable.go:20` repeats the stale
  name; `internal/httpapi/login_throttle.go:39` says cmd/api reads `QC_LOGIN_*`, which it
  does not). Remedy: delete the env mentions and state the real order, defaults then file
  then flag.

- Duplicated comment phrase — `cmd/api/main.go:79-81`. "can be moved independently with
  the sqlite_data key (or the sqlite_data key)". Remedy: delete the parenthetical.

## Collapse

- Metadata-store selection written twice — `cmd/api/cli.go:187-196` (`dbStatus`) describes
  the choice, and `cmd/api/main.go:415-447` (`newStore`) makes it. Both encode
  `admin_database_url` > `metadata=sqlite` > `database_url` > memory, and both compare
  `strings.EqualFold(settings.Metadata, "sqlite")` (`cli.go:190`, `main.go:420`). One can
  drift from the other, and then `querycraft db status` reports a store the gateway will
  not open. Remedy: one pure selector (`selectStore(settings) description`) that both
  `newStore` and `dbStatus` render.

- AI provider vocabulary written twice — `cmd/api/ai.go:32-38` decides
  `"", "off", "none", "disabled"` and `"fake", "deterministic"`, and
  `cmd/api/cli.go:198-205` repeats the same two case lists to print a label. Remedy: one
  helper that maps the setting to a provider or an off-label, consumed by both.

- `pgtest.Server.password` plus `Password()` — `internal/pgtest/pgtest.go:44,88,92`. The
  field is only ever set from the exported const `Password` (`pgtest.go:31,88`), so it is a
  copy of a constant behind a method call. Remedy: delete the field and return the const,
  or delete the method and let the seven call sites use the const.

- `pgtest.EmbeddedBinariesEnv` — `internal/pgtest/pgtest.go:35`. The doc says "the override
  the rest of the repo uses", but the only user is `pgtest.go:131`; the other readers
  hardcode the literal (`internal/httpapi/adminpath_test.go:499`,
  `internal/dataset/template_test.go:450`). Remedy: unexport, or make those two callers use
  the const.

- `pgtest.FreePort` / `pgtest.CachedBinaries` — `internal/pgtest/pgtest.go:109,130`. Both are
  exported for reuse but used only inside `pgtest` (`:55,60`); `internal/httpapi/adminpath_test.go`
  keeps its own copies at `:495-522`. Remedy: one helper, used by both, or unexport both.

- `localdb.DefaultDataDir(root)` / `BinariesDir(root)` — `internal/localdb/localdb.go:317,325`.
  The `root` argument receives `""` at every production call (`internal/config/config.go:98-100`);
  `BinariesDir` only forwards it. Remedy: drop the parameter and return the constant paths.

- Stage order described twice — `internal/schema/model.go:18-19` says the `Stage*` constants
  are "in pipeline order", while the executable order is `stageOrder`
  (`internal/ai/pipeline.go:29-39`). Declaration order is load-bearing in neither place.
  Remedy: export the order from `schema` and have `pipeline` consume it, or drop the
  ordering claim from the comment.

- Export surface no one consumes — `auth.SessionTTL` (`internal/auth/auth.go:31`),
  `auth.MinPasswordLen` (`internal/auth/auth.go:37`), `metrics.DefaultBuckets`
  (`internal/metrics/metrics.go:31`). Each has one internal reader and zero external ones
  (`grep -rn 'auth.SessionTTL\|auth.MinPasswordLen'` outside `auth`: none;
  `DefaultBuckets` at `metrics.go:165` only). Remedy: unexport them; they read as knobs an
  operator can set, and none can be set.

## Move / split

- `run()` — `cmd/api/main.go:46-368` (about 320 lines). It carries twelve boot concerns:
  config mapping (`:49-59`), signals (`:71`), embedded PostgreSQL (`:82-130`), the learner
  role (`:136-142`), executor and store (`:144-152`), admin seeding and the blank-password
  refusal (`:154-190`), the API and environment manager (`:192-246`), SQLite environments
  (`:248-269`), AI wiring and the persisted setting (`:271-322`), the HTTP server
  (`:324-354`), and the job drain (`:355-367`). The comment blocks already mark these
  boundaries, and `docs/target-architecture.md:24,51` names the split:
  `openDatabases`, `wireAI`, `serveAndDrain`. Remedy: extract those three along the existing
  comments; keep the ordering comments in the extracted functions, because they are the
  load-bearing part.

## Earned (checked, keep)

- `config.applyKey` switch — `internal/config/config.go:175-252`: it is the file schema, and prior review 03 (Finding 7) argues for the explicit switch over a pointer map. Every one of the 37 `Settings` fields has a non-test reader (`settings.<Field>` grep, all >= 2).
- `config.Defaults` sources own their defaults — `config.go:91-100,113`: `engine.DefaultMaxRows`, `httpapi.DefaultLogin*`, `localdb.DefaultDataDir`, `provider.DefaultMaxOutputBytes`. One truth per default.
- Login throttle — `internal/httpapi/login_throttle.go`: one implementation, no copy in `auth` (`grep -rn throttle internal/auth`: 0). Settings flow `main.go:56-58` -> `httpapi.Config` -> `newLoginThrottle` (`server.go:138`).
- `localdb.dropLegacyPublicTables` + `legacyPublicTables` — `localdb.go:212-254`: live via `applyDataset` (`localdb.go:281`).
- `localdb.adopt` / `waitReady` / `startWithTempDir` / `applyDataset` — `localdb.go:166-206,138-158,257-302`: all reached from `Start`; the `$TMPDIR` rationale is measured, not speculation.
- `webui.Handler` / `Available` / `Placeholder` — `webui.go:32-85`: reached from `httpapi/routes.go:132-135`.
- `webui` gzip cache — `compress.go:39-94`: reached from `Handler` (`webui.go:64`).
- `pgtest.Start` / `LearnerDSN` / `QuietLogger` / `Server.Database` — `pgtest.go:49,96,123,41`: live consumers in `store`, `engine`, `envdb`, `httpapi` and `cmd/api` tests (grep `pgtest.` shows 30+ call sites).
- `auth` API — `auth.go:47-206`: `NormalizeEmail`, `ValidateEmail`, `ValidatePassword`, `Hash`, `EnsureDefaultAdmin`, `Authenticate`, `StartSession`, `BearerToken` each have an `httpapi/auth.go` or `cmd/api/main.go` caller (grep cited per symbol).
- `metrics.Observe` / `Inc` / `Names` / `WritePrometheus` — `metrics.go:163,145,207,228`: live at `httpapi/metrics.go:122-125,266,202` and `envdb/manager.go:932,939`.
- `schema` stages, states, statuses and run kinds — `model.go:20-30`, `status.go:10-52`, `transcript.go:7-35`: every constant has a live writer or reader (grep per constant; e.g. `StateQueued` at `pipeline.go:48,229`, `StatusDiscarded` at `jobs/jobs.go:279`).
- `schema` owner-scoped factory methods — `model.go:242,252,270`: live at `httpapi/ai.go:156,159,162` and `httpapi/runs.go:70`.
- `reapOrphanedEnvironments` memory guard — `cmd/api/main.go:458-464`: live, and `store.NewMemory` is SQLite `:memory:` (`internal/store/memory.go:27-41`, `Kind()` returns `"memory"`), so the `Kind() == "memory"` test still holds.
- `isLoopbackAddr` — `main.go:376-393`: used at `main.go:187`, tested at `main_test.go:22-50`.
- `passwordFromDSN` / `swapDSNUser` / `newExecutor` / `newStore` / `ensureLearnerRole` / `sqliteOptions` / `reapOrphanedEnvironments` — `main.go:395-522`: each has a call in `run()` or a test; none is a pass-through.
- `metrics.Value` / `metrics.Histogram` — `metrics.go:184,195`: read only by tests (`httpapi/jobs_test.go:330`, `envdb/manager_test.go:156`), but the target architecture keeps `metrics` as the testing story (`docs/target-architecture.md:86`). Keep under that decision.

## UNCERTAIN

- Whether `schema.Artifact.LessonID` has a producer outside this repository. No in-repo writer exists; if an external generator writes it, deleting the field changes that wire.
- Whether `Depot.Challenges` and `Depot.Runs` are budgeted for a future admin route. No route exists today (`routes.go` has `/admin/courses` only), so on the current evidence they are dead, but an owner may be keeping them for a planned inventory view.
- `metrics.Value` / `metrics.Histogram` and the whole `pgtest` surface: both are read only by tests. The target architecture keeps `metrics` and calls `pgtest` a test helper, but a strict reading of "no live consumer" would unexport or fold them. Needs an owner decision, not a code read.
