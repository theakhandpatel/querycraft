# platform — wave 2 verification

Scope and method: I re-read every file the wave-1 report cites and re-ran each
search it names. `rg` is not installed in this environment, so the searches below
are `grep -rn` equivalents over `backend`, `web`, `scripts`, `contracts` and
`docs`, with `.git`, `node_modules`, `dist`, `.data` and `.worktrees` excluded.
I did not edit code, run `make`, or run a test suite. No file changed except this
report.

## Verdict

Wave 1 largely survives. Of the 18 actionable items, 10 stand as written, 7 need
a corrected remedy (mostly because the report understates the cascade or names a
live symbol as dead), and 1 is dropped. The two collapses that carry the most
value, the duplicated metadata-store selection and the duplicated AI-provider
vocabulary in `dbStatus`, are real: `querycraft db status` is a documented CLI
contract (`README.md:104`, `docs/architecture.md:348`), and neither of its two
derivations is shared with the gateway. The depot cascade is bigger and simpler
than stated: there is one `sqlStore`, not three store implementations, so
`Depot.Challenges` and `Depot.Runs` each have a single method to delete and a
mechanical test retarget to the `*For` twin. The env-layer item is valid but
incomplete: the fiction reaches `cli.go` and four `engine`/`eval`/`perf`
comments, and one instance is an operator-facing runtime string. One wave-1 claim,
that stage declaration order duplicates `stageOrder`, is not slop.

## Confirmed actions

Ordered by value.

- [ ] One metadata-store selector, consumed by `newStore` and `dbStatus`. Files:
  `backend/cmd/api/main.go:415-447` and `backend/cmd/api/cli.go:187-196`. Add one
  pure `selectMetadataStore(settings config.Settings) metadataStoreChoice` in
  `cmd/api` returning `{kind, dsn, path}`, where `kind` is one of admin, sqlite,
  dsn, memory. `newStore` switches on `kind`; `dbStatus` prints the label from
  `kind`. Evidence: both encode `admin_database_url > metadata=sqlite >
  database_url > memory`, and both call `strings.EqualFold(settings.Metadata,
  "sqlite")` (`cli.go:190`, `main.go:420`); `grep -rn 'AdminDatabaseURL\|Metadata'
  backend/cmd/api` shows only these two sites. Why safe: `dbStatus` has no test,
  and `newStore`'s callers only need the resulting `store.Store`. The selector
  describes the intended store; the runtime stale-schema fallback
  (`main.go:437-441`) stays in `newStore`, since `db status` cannot know it.

- [ ] Delete `localdb.Server.adopted` and correct `Stop`'s contract. Files:
  `backend/internal/localdb/localdb.go:46-49` (field and doc), `:170` (drop
  `adopted: true` from the `adopt` literal), `:304-305` (`Stop` doc),
  `backend/internal/localdb/localdb_test.go:40-46`. `adopt` (`localdb.go:170`)
  builds its `Server` with no `pg`, so `Stop` returns early on `s.pg == nil`
  (`localdb.go:307`). Rewrite the test to build `&Server{}` (or `&Server{log:
  quiet()}`) and rename it, because today it proves the nil-handle path, not an
  adoption flag. Evidence: `grep -rn -w adopted backend --include='*.go'` returns
  the declaration, the one assignment, two doc lines and the test; no read. Why
  safe: the field has no reader, so behavior is unchanged.

- [ ] Delete `schema.Outcome.StageIndex`. Files:
  `backend/internal/schema/model.go:194-202`, and add a package-local helper in
  `backend/internal/ai/helpers_test.go` (the two callers at `:372` and `:380`).
  Evidence: `grep -rn 'StageIndex' backend web/src scripts contracts docs` returns
  the doc, the method, and those two test calls only. Why safe: `schema` is
  internal, so no out-of-repo caller can depend on the method.

- [ ] Delete the `webui` empty-FS fallback. Files:
  `backend/internal/webui/webui.go:24`, `:87-94`, `:123-125`. Use
  `var sub, _ = fs.Sub(dist, "dist")`; return `sub` from `distFS`; delete
  `emptyFS`. Evidence: `git ls-files backend/internal/webui/dist` lists
  `dist/.gitkeep`, `.gitignore` keeps it, so `//go:embed all:dist` (`:19`) always
  compiles with the directory present and `fs.Sub` cannot fail. Why safe:
  `Available` and the handlers already treat a missing `index.html` as "no SPA",
  which is the real degraded case.

- [ ] Unexport `metrics.Registry.Add`. File:
  `backend/internal/metrics/metrics.go:147-152`; update `Inc` at `:145`.
  Evidence: `grep -rn '\.Add(' backend --include='*.go'` shows the only
  `Registry.Add` caller is `Inc`. Why safe: package-internal rename.

- [ ] Unexport `auth.SessionTTL`, `auth.MinPasswordLen` and
  `metrics.DefaultBuckets`. Files: `backend/internal/auth/auth.go:31,37`,
  `backend/internal/metrics/metrics.go:31`. Evidence: `grep -rn 'SessionTTL\|
  MinPasswordLen' backend` returns only `auth.go` lines 31,37,41,64,191;
  `grep -rn 'DefaultBuckets' backend` returns only `metrics.go` 31,165. Why safe:
  each has one in-package reader and no external caller.

- [ ] Make `pgtest.EmbeddedBinariesEnv` the override the repo actually uses.
  Files: `backend/internal/pgtest/pgtest.go:45-47`, and the literal copies at
  `backend/internal/httpapi/adminpath_test.go:499` and
  `backend/internal/dataset/template_test.go:450`. Either use the const there, or
  unexport it. Evidence: `grep -rn 'QC_PG_BINARIES\|EmbeddedBinariesEnv' backend`
  shows the const is read only at `pgtest.go:297`; the other two callers hardcode
  the string. Why safe: both packages already import `pgtest` in other tests.

- [ ] Delete `pgtest.Server.password` and have `Password()` return the const.
  File: `backend/internal/pgtest/pgtest.go:64,118,258`; keep the call sites at
  `httpapi/environment_test.go:144`, `httpapi/dataset_test.go:244,299`,
  `httpapi/environment_setup_test.go:85`, `envdb/manager_close_test.go:40,95`,
  `envdb/manager_test.go:222` and `cmd/api/reap_test.go:26`. Evidence:
  `grep -rn 'password' backend/internal/pgtest/pgtest.go` shows the field is set
  once, at `:118`, from the exported const `Password`, and read at `:268`; the
  alternative remedy is to delete `Password()` and let those eight sites read the
  const. Why safe: the field cannot hold another value.

- [ ] Delete the duplicated parenthetical in the `run` comment. File:
  `backend/cmd/api/main.go:79-80`. Replace "with the sqlite_data key (or the
  sqlite_data key)" with "with the sqlite_data key". Evidence: the sentence at
  `:79-81` names the key twice.

- [ ] Drop the unused `root` parameter from `localdb.DefaultDataDir` and
  `localdb.BinariesDir`. Files: `backend/internal/localdb/localdb.go:317,325` and
  the three callers at `backend/internal/config/config.go:98-100`. Evidence:
  `grep -rn 'DefaultDataDir\|BinariesDir' backend --include='*.go'` shows every
  call passes `""`. Why safe: no caller passes a non-empty root.

## Amended

- [ ] Delete `schema.Artifact.LessonID`. The field (`model.go:126`) stays dead,
  but the wave-1 delete list names the wrong OpenAPI site. Drop only the
  `AIArtifact` property (`backend/internal/apidocs/apidocs.go:1505`). The property
  at `apidocs.go:1213` belongs to the `Exercise` request schema, where `lesson_id`
  is live (`backend/internal/eval/eval.go:81`, `contracts/exercise.schema.json:11`).
  Full delete: `model.go:126`, `contracts/ai.schema.json:104`,
  `backend/internal/apidocs/apidocs.go:1505`, `web/src/api/aiContract.ts:70`.
  Evidence that it is dead as an artifact field: `grep -rn 'LessonID' backend`
  returns only `model.go:126` and `eval.go:81` (a different type); `grep -rn
  'lesson_id' web/src` returns only exercise bundles and plans
  (`SqlCell.tsx:180`, `planContract.ts:186`, `execution/http.ts:422`).

- [ ] Delete the unfiltered `Depot.Challenges` and `Depot.Runs` surface. Files:
  `backend/internal/schema/model.go:236-271` (drop interface methods `Challenges`
  and `Runs`), `backend/internal/ai/api.go:356-362` (drop `Service.Challenges`),
  `backend/internal/store/challenges.go:104-108` and
  `backend/internal/store/runs.go:119-124` (drop `sqlStore.Challenges` and
  `sqlStore.Runs`), `backend/internal/ai/helpers_test.go:86,145` and
  `backend/internal/httpapi/ai_test.go:36,68` (drop the stubs on `testDepot` and
  `recordingDepot`). Retarget test calls rather than deleting the tests:
  `store/challenges_test.go:97,186,207,235`,
  `store/runs_test.go:84,120,137,189,224,251`, `store/sqlite_test.go:172,185,218,251`
  and `store/store_parity_test.go` (`parityNewestFirst`, `parityOwnerCarried`,
  `parityUpsert`). Change `.Challenges(ctx)` to `.ChallengesFor(ctx, "")` and
  `.Runs(ctx)` to `.RunsFor(ctx, "")`. Also update `store/store.go:98-100`, which
  names `SaveRun/Run/Runs`. Evidence: `grep -rn '\.Challenges(' backend web
  scripts` returns only `ai/api.go:361` plus store tests; `grep -rn '\.Runs('
  backend web scripts` returns only store tests; `grep -rn 'func .*SaveChallenge('
  backend` shows one production implementation. Why safe: `sqlStore.Challenges`
  (`challenges.go:116-123`) and `sqlStore.Runs` (`runs.go:131-138`) call the same
  `*Where` helper as their `*For` twins, with the same empty-owner branch, so the
  two are byte-identical for `""`. The only non-test caller is `Service.Challenges`,
  which no route or test calls; `GET /challenges` uses `ChallengesFor`
  (`httpapi/ai.go:359`, route `routes.go:117`) and `GET /runs` uses `RunsFor`
  (`httpapi/runs.go:70`, route `routes.go:124`). The wave-1 wording "the three
  store implementations plus the store tests" is wrong on both counts: there is
  one `sqlStore` (Memory embeds `*SQLite` at `store/memory.go:24-27`, Postgres
  embeds `sqlStore` at `store/postgres.go:14-16`), and the store tests still guard
  the live `*For` methods.

- [ ] Fix the env-layer comments; the fiction reaches further than wave 1 says.
  In scope, the wave-1 four sites stand, and `cmd/api/cli.go:110-112` ("overrides
  the environment and the config file") is a fifth. Rewrite
  `internal/config/config.go:30-31` and `:117-118`, `cmd/api/main.go:44-45`,
  `cmd/api/cli.go:110-112` and `internal/localdb/localdb.go:8` (name the
  `database_url` key, not `QC_DATABASE_URL`). Decide for fixing comments, not
  building the layer: `config.Load` applies defaults then the file only
  (`config.go:121-133`), and `grep -rn 'Getenv\|LookupEnv' backend` finds only
  `pgtest.go:297` and the `$TMPDIR` swap at `localdb.go:145`. The true order is
  defaults, config file, then the `--addr` flag on `serve` (`cli.go:98-118`),
  which `docs/architecture.md:319` already states, so the code comments
  contradict the architecture doc.

- [ ] Consolidate `pgtest.FreePort` and `CachedBinaries` on the `pgtest` exports,
  not by unexporting them. `adminpath_test.go` copied them, so the report's
  "unexport both" would leave the copy in place. Delete `adminPathCachedBinaries`
  and `adminPathFreePort` (`backend/internal/httpapi/adminpath_test.go:495-533`)
  and call `pgtest.FreePort()` / `pgtest.CachedBinaries()` at `:80-81`. Evidence:
  `grep -rn 'FreePort\|CachedBinaries' backend --include='*.go'` shows the exports
  are used only inside `pgtest` (`:79,84,141`), and the `adminpath` copies are
  token-for-token.

- [ ] One AI-provider classifier, not a label map. Files:
  `backend/cmd/api/ai.go:31-57` and `backend/cmd/api/cli.go:198-205`. The two
  lists are not equivalent today: `aiProviderFrom` handles
  `"openai"/"openai-compatible"` and degrades to off when `ai_base_url` or
  `ai_model` is missing (`ai.go:38-44`), while `dbStatus`'s `default` prints that
  setting as a live provider (`cli.go:203-204`). Add one pure
  `classifyAIProvider(settings) (kind, reason string)` returning off, fake, or
  openai-compatible including the base/model rule; `aiProviderFrom` builds from
  `kind` and `dbStatus` prints from `kind`. Evidence: `cli.go:198-205` repeats the
  `"off"/"none"/"disabled"` and `"fake"/"deterministic"` lists from
  `ai.go:33,36`; `grep -rn 'aiProviderFrom' backend` shows one production caller
  (`main.go:284`) and no test asserts the current wording.

- [ ] Split `run()` (`backend/cmd/api/main.go:46-368`) into helpers that preserve
  lifecycle. `docs/target-architecture.md:51` names `openDatabases`, `wireAI`,
  `serveAndDrain`, so the direction is right. Two corrections. First, the
  extraction must preserve defer order: `main.go:219-221` registers `m.Close()`
  after the local server's `Stop` defer specifically so it runs first (LIFO). A
  helper that owns a `defer` runs its cleanup when the helper returns, so the
  helpers must return closers or a small lifecycle value. Second, the three names
  do not cover the middle block (`main.go:192-269`: API construction, environment
  manager, SQLite environments), which is a fourth concern. Treat this as its own
  behavior-preserving change, not a deletion.

## Dropped

- `schema` stage declaration order duplicates `stageOrder` (`model.go:18-19`,
  `ai/pipeline.go:29-39`). The constants are declared in exactly the pipeline
  order and nothing reads declaration order; `stageOrder` is the only executable
  order, so there is no second truth that can drift into behavior. The comment is
  accurate. `grep -rn 'StageValidateRequest' backend web/src` shows `pipeline.go`
  and the web's own stage labels, not a second ordering.

## New

- The stale env-var vocabulary extends past the platform comments, and one
  instance is operator-facing. Files: `backend/internal/engine/unavailable.go:20`
  (the runtime string tells the operator to "set QC_DATABASE_URL";
  `grep -rn 'QC_DATABASE_URL' backend` shows no code reads it, and the real key is
  `database_url`), plus `backend/internal/engine/execute.go:26`,
  `backend/internal/engine/engine.go:129`, `backend/internal/eval/eval.go:173`,
  `backend/internal/perf/explain.go:101` (`QC_MAX_ROWS`, real key `max_rows`), and
  `backend/internal/httpapi/login_throttle.go:39` (`QC_LOGIN_*`, real keys
  `login_*`). These sit outside the platform domain, so they belong to those
  owners, but they are the same fact and should be fixed with the amended env
  item. Evidence: `grep -rn 'QC_[A-Z_]*' backend --include='*.go'` lists exactly
  these sites plus `pgtest`'s real `QC_PG_BINARIES`.

## Still uncertain

- Whether `schema.Artifact.LessonID` has a producer outside this repository.
  Nothing in `backend`, `web`, `scripts` or `contracts` writes it; if an external
  generator emits `lesson_id` on an artifact, deleting the field changes that
  wire. Same question for `Depot.Challenges` and `Depot.Runs` as a planned admin
  inventory: `routes.go` declares `/admin/courses`, `/admin/ai` and
  `/admin/ai/models` only, so no route exists today. Both need an owner decision,
  not a code read.
