# Backend architecture and simplification design

This document answers four structural questions about the Querycraft Go backend. It covers unnecessary hand-rolled logic, entangled dependencies, third-party package replacements, and over-complicated code.

## Clean packages: one job per package

A package is clean when it owns one concept and when its importers need that concept.

| Package | Stated one-sentence job | True today | Why |
| --- | --- | --- | --- |
| `cmd/api` | Reads environment variables, wires services, and manages process shutdown. | Mostly | `cmd/api/main.go:404-445` also parses DSNs and runs `dataset.EnsureLearnerRole` over `pgx.Connect`. |
| `internal/httpapi` | Translates HTTP requests into service calls and writes JSON or HTML responses. | No | It also owns SQLite workspaces (`httpapi/sqlite.go`), SQLite `PRAGMA` introspection (`httpapi/handlers.go:254-362`), login throttling (`httpapi/login_throttle.go`), owner wrappers (`httpapi/ai.go:73-109`), course setup execution (`httpapi/environment.go:375-390`), and a hand-written OpenAPI string (`httpapi/openapi.go`). |
| `internal/ai` | Verifies untrusted model output against a scratch database before activating courses or challenges. | No | It also owns an HTTP client (`ai/http_provider.go`), a fake generator (`ai/fake.go`), a job scheduler (`ai/jobs.go`), a SQL lexer (`ai/validate.go:220-376`), redaction regexes (`ai/transcript.go:157-179`), and store record types (`ai/api.go:406-451`). |
| `internal/engine` | Executes one SQL statement against PostgreSQL or SQLite under row, byte, and time caps. | No | `engine/explain.go` also parses PostgreSQL `EXPLAIN (FORMAT JSON)` trees, queries `pg_class` and `pg_indexes`, and builds `perf.PlanBundle` grading summaries. |
| `internal/store` | Persists accounts, sessions, progress snapshots, events, and generated records in PostgreSQL, SQLite, or memory. | No | `store` imports `internal/ai` in 10 files and defines its persistence interface in terms of `ai.Depot` (`store/store.go:83`). |
| `internal/dataset` | Provides the deterministic `commerce` and `perf` schemas, seeds, and static table metadata. | No | `dataset/template.go` and `dataset/roles.go` also run live `CREATE DATABASE`, `DROP DATABASE`, `ALTER DATABASE`, and `CREATE ROLE` DDL over `pgx.Conn`. |
| `internal/eval` | Compares a learner's `QueryResult` against an exercise's `ResultContract`. | Yes | `eval` owns result comparison (`compare.go`, `detail.go`) and optional regex check matching (`regex.go`). |
| `internal/perf` | Grades a captured `EXPLAIN` plan summary against a structural `PerfContract`. | No | `perf` holds `PlanBundle` and `Grade` (`perf/grade.go`), while `engine/explain.go` produces `PlanBundle` and `httpapi/plan.go` orchestrates capture. |
| `internal/envdb` | Manages per-account and scratch PostgreSQL databases copied from sealed templates. | Half | It manages PostgreSQL workspaces (`envdb/manager.go`), while the identical lifecycle for SQLite workspaces lives in `httpapi/sqlite.go`. |
| `internal/dialect` | Decodes `contracts/dialects/sqlite.json` to classify SQLite errors and encode SQLite values. | Yes | It is a self-contained leaf reader of the dialect profile. |
| `internal/metrics` | Tracks counters, gauges, and histograms and formats them as Prometheus text. | Yes | Zero internal imports and one clear responsibility. |
| `internal/localdb` | Downloads, starts, adopts, and stops the embedded PostgreSQL server. | Yes | Wraps `embedded-postgres` and seeds the initial cluster. |
| `internal/webui` | Serves the `//go:embed` React build or a fallback HTML page. | Yes | Zero internal imports and one clear responsibility. |
| `internal/auth` | Hashes passwords with bcrypt, validates emails, seeds the default admin, and mints session tokens. | Mostly | The login failure throttle that protects `auth.Authenticate` lives in `httpapi/login_throttle.go` instead of `internal/auth`. |
| `internal/pgtest` | Starts an isolated embedded PostgreSQL cluster for integration tests. | Yes | Test-only helper used by `dataset`, `engine`, `envdb`, `httpapi`, and `store`. |

## 1. Necessity

Each entry below names a piece of hand-rolled logic and the problem it claims to solve. It then states whether the problem is real and whether to delete or replace the code.

### Hand-authored OpenAPI document restating the router (`internal/httpapi/openapi.go:12-1855`)

The constant `openAPISpec` in `httpapi/openapi.go:12` is a hand-written JSON string describing 33 endpoints and their schemas. It claims to document the live HTTP surface for `/openapi.json` and `/docs`. Serving `/openapi.json` is a real requirement, but maintaining a parallel JSON string by hand causes the drift it claims to prevent:

1. `POST /courses/generate` (`httpapi/openapi.go:354`) and `POST /challenges/generate` (`httpapi/openapi.go:537`) omit `"security": [{"bearerAuth": []}]` and document no `401` response. Yet `runGeneration` (`httpapi/ai.go:124`) calls `s.requireUser` and rejects anonymous callers with `401 AUTH_REQUIRED`.
2. `CourseGenerateRequest` and `ChallengeGenerateRequest` (`httpapi/openapi.go:1748-1759`) are dead schema definitions referenced by no path in the spec.
3. The `components/schemas` block (`httpapi/openapi.go:861-1851`) restates `contracts/query-result.schema.json`, `contracts/evaluation.schema.json`, `contracts/plan.schema.json`, `contracts/perf-contract.schema.json`, `contracts/ai.schema.json`, and `contracts/run.schema.json` by hand.

Verdict: Delete the hand-written `openAPISpec` string. Embed `contracts/*.schema.json` and build the OpenAPI `paths` map from the single route registration slice in `internal/httpapi`.

### Double-pass JSON decoder on `/answers/evaluate` (`internal/httpapi/handlers.go:204-228`)

`evaluateRequest.UnmarshalJSON` (`httpapi/handlers.go:214-228`) unmarshals the request body into `eval.Request` and then unmarshals the same bytes into a second routing struct:

```go
func (req *evaluateRequest) UnmarshalJSON(data []byte) error {
	if err := json.Unmarshal(data, &req.Request); err != nil {
		return err
	}
	var routing struct {
		statementRequest
		perfMarker
	}
	if err := json.Unmarshal(data, &routing); err != nil {
		return err
	}
	req.statementRequest = routing.statementRequest
	req.Exercise = routing.perfMarker.Exercise
	return nil
}
```

The comment at `httpapi/handlers.go:199-203` claims one reflection decode cannot read both `eval.Request.Exercise` and `perfMarker.Exercise` from the `"exercise"` JSON key. That problem is self-inflicted. `eval.Exercise` (`internal/eval/eval.go:78-95`) omitted the `Perf *perf.PerfContract` field, and `eval.Request` (`internal/eval/eval.go:138-142`) omitted `Dialect` and `Dataset`.

Verdict: Delete `evaluateRequest`, `statementRequest`, `perfMarker`, and `UnmarshalJSON` (`httpapi/handlers.go:85-96, 204-228`). Add `Dialect` and `Dataset` to `eval.Request` and `Perf` to `eval.Exercise` so `readJSON` decodes the body once.

### Per-request `ownerScopedDepot` wrapper (`internal/httpapi/ai.go:58-109`)

On every AI request, `s.aiServiceFor(ownerID)` (`httpapi/ai.go:60-69`) allocates an `ownerScopedDepot` (`httpapi/ai.go:73-109`) that wraps `ai.Depot`. It stamps `rec.OwnerID = d.owner` on `SaveChallenge`, `SaveCourse`, and `SaveRun`. It also overrides `ChallengesFor`, `CoursesFor`, and `RunsFor` to ignore their `ownerID` parameter and substitute `d.owner`. Meanwhile, `handleListCourses`, `handleGetCourse`, `handleDeleteCourse`, and `handleBuildCourse` (`httpapi/ai.go:364-503`) bypass `ownerScopedDepot` and call `s.aiCourseDepot()` directly. `handleListRuns` and `handleGetRun` (`httpapi/runs.go:56-107`) also bypass it because the wrapper cannot scope single-record reads.

Verdict: Delete `ownerScopedDepot` and `s.aiServiceFor` (`httpapi/ai.go:58-109`). Pass `ownerID string` explicitly to `Service.GenerateChallenge`, `Service.GenerateCourse`, and `Service.BuildCourse`, and construct `*ai.Service` once in `UseAI`.

### Stage snapshot copied on every pipeline transition (`internal/ai/pipeline.go:151-185` and `internal/ai/jobs.go:398-440`)

`newJob` (`ai/jobs.go:402-415`) and `newPipeline` (`ai/pipeline.go:79-86`) each allocate their own 9-element `[]Stage` slice. Every `begin`, `mark`, `timed`, `setChapter`, `setVerified`, `storeBuilding`, and `publish` call invokes `p.notify()` (`ai/pipeline.go:151-185`). `p.notify()` allocates three new slices (`stages`, `attempts`, `chapters`) and rewrites `StateSkipped` entries with empty `Detail` to `StateQueued`. It then calls `jb.observe(Progress{...})` (`ai/jobs.go:421-440`), which overwrites `jb.stages`, `jb.attempts`, and `jb.chapters` under `jb.mu`. In `saveChapterTool` (`ai/course_agent.go:1105, 1118`), `p.storeBuilding` and `p.publish` call `p.notify()` twice in succession for the same chapter. When a poller calls `GET /generation/{id}`, `jb.view()` (`ai/jobs.go:499-522`) copies `jb.stages` and `jb.attempts` again. `jb.view()` also forgets to copy `jb.chapters` at line 508, sharing the backing array with the running pipeline goroutine.

Verdict: Delete `p.notify()`'s eager slice cloning and delete `WithProgress` and `progressFrom` (`ai/jobs.go:109-130`). Update stage state in place on `job` under `jb.mu`, and allocate copies only in `jb.view()` when a poller calls `GET /generation/{id}`.

### Manual deep-cloning of `CourseRecord` and `Record` in memory store (`internal/store/memory.go:201-255, 387-401`)

`cloneCourse` (`store/memory.go:201-255`) and `cloneRecord` (`store/memory.go:387-401`) are hand-written slice-by-slice deep copy functions in `store.Memory`. They run on every `SaveCourse`, `Course`, `CoursesFor`, `SaveChallenge`, `Challenge`, and `ChallengesFor` call. They exist only because `Memory` stores live Go structs in `challenges` and `courses`, whereas `Memory.progress` and `Memory.events` (`store/memory.go:23-24`) store `json.RawMessage` and clone with `bytes.Clone` (`store/memory.go:412, 424`).

Verdict: Delete `cloneCourse`, `cloneRecord`, and `cloneRun` (`store/memory.go:201-255, 379-401`). Store serialized JSON bytes in `Memory` so callers cannot alias internal slices and `store/memory.go` stops depending on `CoursePlan` internals.

### Re-decoding and re-marshaling `json.Marshal` output in SQLite store (`internal/store/sqlite.go:183-199` and `internal/store/sqlite_depot.go`)

In `store/sqlite_depot.go`, `SaveChallenge` (`lines 25-40`), `SaveCourse` (`lines 120-147`), `SavePatch` (`lines 232-239`), and `SaveRun` (`lines 290-313`) call `jsonText(v)` (`json.Marshal`). They then pass that JSON string to `normalizeJSON` (`store/sqlite.go:183-199`):

```go
plan, err := jsonText(rec.Plan)
if err != nil {
	return fmt.Errorf("encode course plan: %w", err)
}
plan, err = normalizeJSON(json.RawMessage(plan))
```

`normalizeJSON` creates a `json.NewDecoder`, decodes the JSON string into `var value any`, checks for trailing tokens, and calls `json.Marshal(value)` again. `normalizeJSON` was built for `SaveProgress` and `SaveEvent` (`store/sqlite.go:208, 236`), which receive untrusted `json.RawMessage` bytes from HTTP requests. Calling `normalizeJSON` on `json.Marshal` output re-parses and re-serializes JSON that `json.Marshal` just produced.

Verdict: Delete all 8 `normalizeJSON` calls in `store/sqlite_depot.go` (`lines 33, 37, 128, 132, 143, 236, 294, 309`). Keep `normalizeJSON` only in `SaveProgress` and `SaveEvent`.

### Duplicating `CoursePlan` and `Artifact` inside the stored `outcome` column (`internal/ai/course_agent.go:1469-1483`, `internal/ai/course_pipeline.go:126-141`, `internal/ai/pipeline.go:778-784`)

When `storeBuilding` (`ai/course_agent.go:1469-1483`) and `activateCourse` (`ai/course_pipeline.go:126-141`) persist a course to `app.courses`, they set `out.Plan = &plan` inside `Outcome` and pass `CourseRecord{Plan: plan, Outcome: out}` to `SaveCourse`. `SaveCourse` (`store/courses.go:31-39`) marshals `rec.Plan` into the `plan` column and `rec.Outcome` into the `outcome` column. Every row in `app.courses` therefore stores two identical copies of the entire `CoursePlan`. `activate` (`ai/pipeline.go:778-784`) does the same for `app.challenges`, storing `Artifact` in both `artifact` and `outcome.artifact`.

Verdict: Clear `Outcome.Plan` and `Outcome.Artifact` before writing `outcome` to `app.courses` and `app.challenges`. Re-attach `rec.Outcome.Plan = &rec.Plan` and `rec.Outcome.Artifact = &rec.Artifact` in `decodeCourse` (`store/courses.go:142`) and `decodeRecord` (`store/challenges.go:180`).

### Synchronous `UPDATE sessions SET last_seen_at` on every authenticated request (`internal/store/postgres.go:352` and `internal/store/sqlite.go:335`)

`SessionUser` in `store/postgres.go:352` and `store/sqlite.go:335` executes `UPDATE app.sessions SET last_seen_at = now() WHERE token = $1` on every authenticated request. No query, handler, diagnostic, or test in the repository ever reads `last_seen_at`. In `QC_METADATA=sqlite` mode (`store/sqlite.go:142`, `db.SetMaxOpenConns(1)`), this write acquires the SQLite lock on every `GET` request.

Verdict: Delete the `UPDATE` statement in `store/postgres.go:352` and `store/sqlite.go:335` and drop `last_seen_at` from `migrateSQL` and `SQLiteSchema`.

### Second SQL comment and quote scanner `isBlankSQL` (`internal/engine/execute.go:133-175`)

`isBlankSQL` in `engine/execute.go:133-175` hand-rolls a scanner for single quotes, double quotes, `--` line comments, and `/* */` block comments. It uses that scanner only to check whether a SQL string is empty. `tokenizeSQL` in `ai/validate.go:243-292` already lexes SQL tokens, including nested block comments (`skipBlockComment`) and PostgreSQL `$tag$` dollar-quoted strings (`readDollarQuoted`), which `isBlankSQL` misses.

Verdict: Delete `isBlankSQL` (`engine/execute.go:133-175`) and use the shared SQL tokenizer (`len(TokenizeSQL(sql)) == 0`).

### Six catalog probe queries on every freshly copied scratch database (`internal/ai/pipeline.go:600-628`)

When the AI pipeline verifies a challenge or course, `Runner.Scratch` (`envdb/scratch.go:28-49`) creates a fresh database from `qc_template` and probes it with `probeWritable` (`envdb/manager.go:627-651`). Immediately after, `p.prepare` calls `p.checkDataset` (`ai/pipeline.go:600-628`). `checkDataset` runs 6 more SQL queries (`SELECT version FROM dataset.dataset_meta`, `SELECT table_name FROM information_schema.tables`, and `SELECT * FROM dataset.<table> LIMIT 0` for each of the 4 tables) to check the template copy. When `art.Setup.Statements` is empty, nothing touches the database after `Runner.Scratch` copies it from the sealed `qc_template`.

Verdict: Verify `qc_template` once when sealing it in `EnsureTemplate` (`envdb/manager.go:205`), and run `checkDataset` in `ai/pipeline.go:592` only when `len(art.Setup.Statements) > 0`.

### Regenerating all 290 `commerce` dataset rows on every prompt and agent turn (`internal/ai/pipeline.go:838-856` and `internal/ai/course_agent.go:970`)

`datasetBrief()` (`ai/pipeline.go:838-856`) calls `dataset.Tables("commerce")`, which calls `dataset.Commerce()` (`dataset/dataset.go:59-71`). That call builds all 30 customers, 20 products, 80 orders, and 160 order_items in memory and formats 5-row string previews. `datasetBrief()` then discards the previews and prints only the table names, column definitions, and row counts. Worse, `runCourseAgent` (`ai/course_agent.go:968-975`) evaluates `Prompt: courseOutlinePrompt(build.request)` (which calls `datasetBrief()`) inside `for round := 0; round < maxAgentRounds; round++` on every turn of the conversation, even though `HTTPProvider.messages` (`ai/http_provider.go:293`) ignores `req.Prompt` whenever `len(req.Messages) > 0`.

Verdict: Compute `datasetBrief` once from `commerceTables` metadata, and move `courseOutlinePrompt(build.request)` outside the `for round` loop in `ai/course_agent.go:960`.

### Legacy migration detectors in a greenfield schema (`internal/store/postgres.go:178-191` and `internal/localdb/localdb.go:220-254`)

`store/postgres.go:29` states that the metadata schema has no pre-accounts layout to migrate from. An older database resets via `make db-reset`. Yet `hasLegacyMetadata` (`store/postgres.go:178-191`) queries `information_schema` on every boot for pre-accounts `app.progress`. And `dropLegacyPublicTables` (`localdb/localdb.go:220-254`) runs 7 `to_regclass` queries on every boot for tables in `public`.

Verdict: Delete `dropLegacyPublicTables` (`localdb/localdb.go:208-254` and call at lines 275-282). Delete `hasLegacyMetadata` (`store/postgres.go:160-191`) once you retire `TestPostgresRejectsLegacySchema` in `store/store_test.go`.

## 2. Entanglement

`go list` reports 32 internal import edges across `backend/`. The table below gives the verdict for every edge.

| Importer | Imported package | Verdict | Reason, what the importer actually needs, and where it belongs |
| --- | --- | --- | --- |
| `internal/store` | `internal/ai` | **Remove** | Persistence must not depend on the AI pipeline; `store` needs only the record structs (`Record`, `CourseRecord`, `PatchProposal`, `RunTranscript`, `RunSummary`), which belong in `internal/schema` (or `internal/store` itself). |
| `internal/engine` | `internal/perf` | **Remove** | `engine/explain.go` imports `perf` to return `*perf.PlanBundle`; move `explain.go` and `perf` into `internal/eval` (with `PlanBundle` in `internal/schema`) so `engine` only executes SQL. |
| `internal/httpapi` | `internal/perf` | **Remove** | `httpapi/plan.go` and `httpapi/metrics.go` only need `PlanBundle`, `PerfContract`, and `Explain`, which belong in `internal/schema` and `internal/eval`. |
| `internal/httpapi` | `internal/dataset` | **Remove** | `httpapi` imports `dataset` for `Tables(id)`, `FactRowCount(id)`, and `PerfDatasetID`; move table introspection (including SQLite `PRAGMA table_info` in `httpapi/handlers.go:254-362`) and dataset resolution into `internal/envdb` (renamed `workspace`). |
| `internal/envdb` | `internal/metrics` | **Remove** | `envdb` imports `metrics` only to record 3 metrics (`envdb/manager.go:44-46, 773-793`); return `(exec, createdDuration, err)` from `Ensure` and let `httpapi` record the metrics where it already declares all other gateway metrics (`httpapi/metrics.go:46-56`). |
| `internal/ai` | `internal/dataset` | **Keep** | `ai/pipeline.go:839` calls `dataset.Tables("commerce")` to embed the schema brief into generation prompts. |
| `internal/ai` | `internal/engine` | **Keep** | `ai` runs candidate reference solutions, cross-checks, and setup statements through `engine.Execute` (`ai/pipeline.go:586, 656`). |
| `internal/ai` | `internal/eval` | **Keep** | `ai` verifies reference solutions, cross-checks, and adversarial fixtures with `eval.CompareDetailed` and `eval.Evaluate` (`ai/pipeline.go:537, 558, 681`). |
| `internal/auth` | `internal/store` | **Keep** | `auth.Authenticate`, `auth.StartSession`, and `auth.EnsureDefaultAdmin` read and write users and sessions through `store.Store` (`auth/auth.go:114, 174, 186`). |
| `internal/engine` | `internal/dialect` | **Keep** | `engine/sqlite.go:260` uses `dialect.SQLite()` to encode SQLite values and classify SQLite errors. |
| `internal/envdb` | `internal/dataset` | **Keep** | `envdb` uses `dataset` DDL scripts and template helpers to build `qc_template` and `qc_perf_template` (`envdb/manager.go:210, 230`). |
| `internal/envdb` | `internal/engine` | **Keep** | `envdb` constructs per-account `*engine.PgxExecutor` pools (`envdb/manager.go:421`) and should also own `*engine.SQLiteExecutor` pools moved from `httpapi/sqlite.go`. |
| `internal/eval` | `internal/engine` | **Keep** | `eval.Evaluate` executes the learner query and optional reference query via `engine.Execute` (`eval/eval.go:172, 180`). |
| `internal/localdb` | `internal/dataset` | **Keep** | `localdb.Start` applies `dataset.SchemaSQL()`, `dataset.SeedSQL()`, and `dataset.EnsureLearnerRole` on first cluster boot (`localdb/localdb.go:268, 294`). |
| `internal/pgtest` | `internal/dataset` | **Keep** | `pgtest.Server.LearnerDSN` uses `dataset.LearnerRole` (`pgtest/pgtest.go:102`). |
| `internal/pgtest` | `internal/localdb` | **Keep** | `pgtest.Start` boots a test PostgreSQL instance via `localdb.Start` (`pgtest/pgtest.go:79`). |
| `internal/httpapi` | `internal/ai` | **Keep** | `httpapi/ai.go` dispatches generation, tutor, critique, report, and patch routes to `ai.Service` and `ai.Jobs` (while `/courses`, `/runs`, and `courseSetup` should read `s.store` directly). |
| `internal/httpapi` | `internal/auth` | **Keep** | `httpapi/auth.go` calls `auth.Authenticate`, `auth.StartSession`, `auth.Hash`, and the login throttle (moved from `httpapi/login_throttle.go` to `internal/auth`). |
| `internal/httpapi` | `internal/engine` | **Keep** | `httpapi/handlers.go` calls `engine.Execute` for `POST /query` and reads `s.exec.Ready` and `s.exec.Stats` for `/health` and `/diagnostics`. |
| `internal/httpapi` | `internal/envdb` | **Keep** | `httpapi` routes authenticated statements and `/environment/reset` to the workspace manager (`envdb`). |
| `internal/httpapi` | `internal/eval` | **Keep** | `httpapi/handlers.go` calls `eval.Evaluate` for `POST /answers/evaluate` (and `eval.Explain` for `POST /explain`). |
| `internal/httpapi` | `internal/metrics` | **Keep** | `httpapi/metrics.go` records HTTP request, statement, plan, and generation metrics and serves `GET /metrics`. |
| `internal/httpapi` | `internal/store` | **Keep** | `httpapi` reads and writes sessions, users, progress, events, courses, challenges, and runs through `store.Store`. |
| `internal/httpapi` | `internal/webui` | **Keep** | `httpapi/server.go:223-227` mounts `webui.Handler()` for the embedded SPA. |
| `cmd/api` | `internal/ai` | **Keep** | `cmd/api/ai.go` wires `ai.Config` and `aiProviderFromEnv`. |
| `cmd/api` | `internal/auth` | **Keep** | `cmd/api/main.go:156` calls `auth.EnsureDefaultAdmin` on boot and `auth.Verify` for the loopback blank-password guard. |
| `cmd/api` | `internal/dataset` | **Keep** | `cmd/api/main.go:135, 281, 442` reads `dataset.LearnerRole`, `dataset.DatasetVersion`, and `dataset.EnsureLearnerRole`. |
| `cmd/api` | `internal/engine` | **Keep** | `cmd/api/main.go:348-366` constructs `engine.NewPgxExecutor` or `engine.UnavailableExecutor`. |
| `cmd/api` | `internal/envdb` | **Keep** | `cmd/api/main.go:202` starts the workspace manager and runs `ReapUnreferenced`. |
| `cmd/api` | `internal/httpapi` | **Keep** | `cmd/api/main.go:189` constructs `httpapi.New` and serves `api.Handler()`. |
| `cmd/api` | `internal/localdb` | **Keep** | `cmd/api/main.go:112` starts the embedded PostgreSQL server when `QC_DATABASE_URL` is unset. |
| `cmd/api` | `internal/store` | **Keep** | `cmd/api/main.go:368-399` opens `store.NewPostgres`, `store.NewSQLite`, or `store.NewMemory`. |

Removing those 5 accidental edges (`store -> ai`, `engine -> perf`, `httpapi -> perf`, `httpapi -> dataset`, `envdb -> metrics`) and moving `httpapi/sqlite.go` into `envdb` eliminates every upward or sideways dependency:

1. Leaf packages (`schema`, `dialect`, `metrics`, `webui`, `dataset`) have zero internal dependencies.
2. Storage and execution (`store`, `engine`) depend only on `schema` and `dialect`.
3. Domain services (`auth`, `eval`, `envdb`) depend on `store`, `engine`, and `dataset`.
4. `ai` depends on `engine`, `eval`, `dataset`, and `store` (via `schema`).
5. `httpapi` and `cmd/api` sit at the top.

## 3. Hand-rolled versus an established package

Each subsystem below compares our hand-rolled code against maintained Go packages on correctness, simplicity, and fit with Querycraft's invariants.

### 1. OpenAPI document (`internal/httpapi/openapi.go`)

- Hand-rolled today: `openAPISpec` (`httpapi/openapi.go:12-1855`), a raw JSON string literal, plus `swaggerUIDocs` (`httpapi/openapi.go:1859-1882`).
- Candidates:
  - `github.com/danielgtaylor/huma/v2` (v2.32.0, MIT licence). Generates OpenAPI 3.1 from Go structs and typed handler functions `func(ctx, *In) (*Out, error)`.
  - `github.com/swaggo/swag` (v1.16.4, MIT licence). Generates OpenAPI 2.0/3.0 from comment annotations via a CLI build step.
  - Assembling `/openapi.json` in Go from `contracts/*.schema.json` (`//go:embed`) plus a declarative `[]routeSpec` slice in `httpapi`.
- Comparison: `swaggo/swag` targets Swagger 2.0 (our spec is OpenAPI 3.1.0, tested by `httpapi/openapi_test.go:65`) and adds a codegen build step. `huma/v2` requires rewriting all 33 handlers into Huma's generic signature and fighting Huma's default RFC 9457 `application/problem+json` error model. Querycraft returns `QueryResult` on 400 for `/query` (`httpapi/handlers.go:141-143`) and `{ok:false, error:{code,message}}` with route-specific error codes (`httpapi/server.go:292-306`) on other routes. Meanwhile, our canonical schemas already exist as JSON Schema files in `contracts/*.schema.json`.
- Verdict: Do not adopt `huma/v2` or `swag`. Delete the hand-written `openAPISpec` string in `httpapi/openapi.go`. Build the OpenAPI 3.1 document in Go by embedding `contracts/*.schema.json` into `components/schemas` and iterating one `[]routeSpec` slice.

### 2. Prometheus exposition format (`internal/metrics/metrics.go`)

- Hand-rolled today: `internal/metrics/metrics.go` (`Registry`, `Inc`, `Set`, `Observe`, `WritePrometheus`, `Value`, `Histogram`, `Names`).
- Candidate: `github.com/prometheus/client_golang/prometheus` + `promhttp` (v1.21.1, Apache-2.0 licence).
- Comparison: `prometheus/client_golang` adds 7 transitive modules (`beorn7/perks`, `cespare/xxhash/v2`, `prometheus/client_model`, `prometheus/common`, `prometheus/procfs`, `munnerz/goautoneg`, `google.golang.org/protobuf`). More importantly, `client_golang` does not provide a direct `r.Value(name, labels)` or `r.Names()` method without collecting `dto.Metric` protobuf pointers. Our gateway uses `s.metrics.Names()` directly in `GET /diagnostics` (`httpapi/metrics.go:346`) and `r.Value` / `r.Histogram` across `httpapi/metrics_test.go` and `envdb/manager_test.go`. Our hand-rolled `metrics.Registry` has 9 methods, zero dependencies, and full test coverage of Prometheus text escaping and histogram buckets (`metrics/metrics_test.go`).
- Verdict: Keep `internal/metrics`. It buys direct in-process `Value()`, `Histogram()`, and `Names()` reads for `/diagnostics` and tests without pulling in `google.golang.org/protobuf`.

### 3. Hosted-model provider client and retry (`internal/ai/http_provider.go`)

- Hand-rolled today: `HTTPProvider` (`ai/http_provider.go:31-348`), an OpenAI-compatible `POST /chat/completions` client over `net/http` with zero retry logic.
- Candidates:
  - `github.com/openai/openai-go` (v0.1.0-alpha.62+, Apache-2.0 licence, official OpenAI SDK).
  - `github.com/hashicorp/go-retryablehttp` (v0.7.7, MPL-2.0 licence) or `github.com/cenkalti/backoff/v4` (v4.3.0, MIT licence).
- Comparison: ADR 0005 (`docs/adr/0005-ai-provider-and-sdk.md`) rejected vendor SDKs (`openai-go`) because `HTTPProvider` enforces policy before JSON decoding. It caps response bytes with `io.LimitReader(resp.Body, int64(limit)+1)` (`ai/http_provider.go:214`), strips `response_format` when `Tools` are advertised (`ai/http_provider.go:179-187`), and scrubs `p.apiKey` and DSN credentials from every error (`ai/http_provider.go:335-341`). However, `HTTPProvider.Complete` (`ai/http_provider.go:200-224`) currently does zero retries on HTTP `429`, `502`, `503`, or `504`. `completeRaw` (`ai/pipeline.go:404-409`) maps any provider error to `CodeUnavailable`, which aborts the pipeline outside the content-repair loop. A single transient `429` or `503` on chapter 4 therefore kills the entire course generation run. `go-retryablehttp` introduces MPL-2.0 licensing and logs raw URLs to its own logger, while `cenkalti/backoff` does not parse HTTP `Retry-After` headers.
- Verdict: Keep `HTTPProvider` over `net/http` (rejecting `openai-go` and `go-retryablehttp`). Add a bounded 2-retry loop inside `HTTPProvider.Complete` for status `429`, `502`, `503`, and `504` that honors `Retry-After` and `callCtx.Done()`.

### 4. Raw SQLite driver glue (`internal/engine/sqlite_raw.go`)

- Hand-rolled today: `sqliteRawDriver`, `sqliteConnector`, `sqliteRawConn`, and `sqliteRawRows` (`engine/sqlite_raw.go:1-906`), which call `modernc.org/sqlite/lib` (`lib.Xsqlite3_open_v2`, `lib.Xsqlite3_prepare_v2`, `lib.Xsqlite3_step`, `lib.Xsqlite3_column_type`, `lib.Xsqlite3_interrupt`) and allocate C memory via `modernc.org/libc` (`tls.Alloc`) using `unsafe.Sizeof(uintptr(0))`.
- Candidates:
  - `modernc.org/sqlite`'s standard `database/sql` driver (already in `go.mod`, BSD-3-Clause).
  - `github.com/ncruces/go-sqlite3` (v0.25.0, MIT licence, cgo-free SQLite via `tetratelabs/wazero` WebAssembly runtime).
  - `github.com/mattn/go-sqlite3` (MIT licence, requires `cgo`).
- Comparison: Why does `engine/sqlite_raw.go` exist when `modernc.org/sqlite` is already in `go.mod`? In `modernc.org/sqlite` v1.38.2 (`sqlite.go:265-275`), the driver's `rows.Next` inspects `sqlite3_column_decltype` on every `SQLITE_TEXT` cell. When the declared type is `DATE`, `DATETIME`, or `TIMESTAMP`, `modernc` parses the string into a `time.Time` (`2025-11-02T00:00:00Z` instead of `'2025-11-02'`). `modernc.org/sqlite` provides no option or hook to disable this read-side conversion. Returning `2025-11-02T00:00:00Z` violates `contracts/dialects/sqlite.json` (`value_encoding.date`), breaks parity with `@sqlite.org/sqlite-wasm` in the browser, and contradicts chapter `s2`. `mattn/go-sqlite3` requires `cgo`, which violates `CGO_ENABLED=0` (`make check-cgo`). `ncruces/go-sqlite3` avoids `unsafe` C-memory pointers by running SQLite inside a `wazero` WASM interpreter. However, it adds two large dependencies (`ncruces/go-sqlite3` and `tetratelabs/wazero`) and alters driver error framing (`sqliteUnframe` in `engine/sqlite.go:348`).
- Verdict: Keep `engine/sqlite_raw.go` over `modernc.org/sqlite/lib`. It is the only way in `modernc.org/sqlite` to read `SQLITE_TEXT` columns with `DATE` decltypes verbatim while preserving `sqlite3_column_decltype` labels and `sqlite3_interrupt` deadlines. Delete `parseSQLiteDSN` (`engine/sqlite_raw.go:194-230`) and `SQLiteConfig.DSN` (`engine/sqlite.go:135`), which have zero production callers.

### 5. SQL classification and lexing (`internal/ai/validate.go`, `internal/engine/execute.go`, `internal/dialect/sqlite.go`)

- Hand-rolled today:
  - `tokenizeSQL`, `validateReference`, `validateSetupStatement` (`ai/validate.go:220-546`): lexes SQL tokens to enforce single-statement `SELECT`/`WITH` allowlists and `CREATE TABLE`/`INSERT INTO` setup rules.
  - `pgCodeFor` (`engine/execute.go:257-278`): maps `pgconn.PgError.Code` SQLSTATEs to Querycraft's closed error enum.
  - `Profile.ClassifyError` (`dialect/sqlite.go:69-94`): matches SQLite error messages against `contracts/dialects/sqlite.json`.
- Candidates:
  - `github.com/pganalyze/pg_query_go/v6` (v6.1.0, BSD-3-Clause, wraps PostgreSQL's C parser).
  - `github.com/xwb1989/sqlparser` (Apache-2.0, MySQL-dialect AST parser).
- Comparison: `pg_query_go` requires `cgo` (`CGO_ENABLED=1`) to compile PostgreSQL's C grammar, breaking `make check-cgo`. `xwb1989/sqlparser` parses MySQL syntax and rejects valid PostgreSQL queries in our curriculum (`::date`, `DATE_TRUNC`, `COUNT(*) FILTER (WHERE ...)`, `DISTINCT ON`, `$tag$`). Furthermore, Querycraft's security boundary is the restricted PostgreSQL role `querycraft_learner` (`dataset/roles.go:18` and `technical-design.md` §5.4). `validateReference` in `ai/validate.go` is only a fast policy pre-gate on AI-generated SQL before `engine.Execute` runs it in a scratch database.
- Verdict: Keep `tokenizeSQL`, `pgCodeFor`, and `Profile.ClassifyError` hand-rolled, and delete the duplicate scanner `isBlankSQL` in `engine/execute.go:133-175`.

### 6. Hand-written SQL per store backend (`internal/store/*.go`)

- Hand-rolled today: `store.Postgres` (`postgres.go`, `courses.go`, `challenges.go`, `runs.go`), `store.SQLite` (`sqlite.go`, `sqlite_depot.go`), and `store.Memory` (`memory.go`) each write every CRUD method by hand.
- Candidates:
  - `github.com/sqlc-dev/sqlc` (MIT, code generator) or `github.com/jmoiron/sqlx` (MIT, struct scanning over `database/sql`).
  - Consolidating record validation defaults and row-scanning helpers inside `internal/store`.
- Comparison: Neither `sqlc` nor `sqlx` eliminates having both `Postgres` and `SQLite`. `Postgres` uses `pgxpool.Pool` (`$1, $2` placeholders and `timestamptz`) while `SQLite` uses `database/sql` (`?, ?` placeholders and RFC3339 text timestamps). However, `Postgres` (`challenges.go:85-116`, `courses.go:97-129`, `runs.go:121-155`) and `SQLite` (`sqlite_depot.go:72-102, 186-221, 361-400`) repeat the same `WHERE owner_id` scan and JSON-decode loop 6 times.
- Verdict: Reject `sqlc` and `sqlx` (adding a third database layer alongside `pgx` and `database/sql` increases surface area). Extract shared record-default initializers and row-scan helpers across `Postgres`, `SQLite`, and `Memory` inside `internal/store`.

### 7. Session handling and login throttling (`internal/auth/auth.go`, `internal/httpapi/auth.go`, `internal/httpapi/login_throttle.go`)

- Hand-rolled today:
  - Bearer token generation (`auth.NewToken`, `crypto/rand` 32 bytes base64url in `auth/auth.go:91-98`) and database lookup (`store.SessionUser`).
  - Fixed-window login failure counter with bounded map eviction (`loginThrottle` in `httpapi/login_throttle.go:64-225`).
- Candidates:
  - `github.com/alexedwards/scs/v2` (v2.8.0, MIT licence, cookie/header session manager).
  - `golang.org/x/time/rate` (BSD-3-Clause, token-bucket rate limiter).
- Comparison:
  - `scs/v2` targets HTTP cookies and `http.ResponseWriter` commit middleware. Querycraft's frontend contract (`web/src/api/`) sends `Authorization: Bearer <token>` from `POST /auth/login` and `POST /auth/signup` JSON bodies (`{ok, user, token, expiresAt}` in `httpapi/auth.go:76-81`).
  - `golang.org/x/time/rate` implements a continuous token bucket per key, but does not bound map cardinality (requiring a hand-rolled eviction map anyway). It also lacks the contract's fixed-window rule (`backend/README.md` and `httpapi/login_throttle_test.go`). That rule counts `QC_LOGIN_MAX_FAILURES` in `QC_LOGIN_WINDOW_MS`, resets on login (`httpapi/auth.go:168`), and sets `Retry-After`.
- Verdict: Keep `auth.NewToken` and `loginThrottle` hand-rolled, and move `login_throttle.go` + `login_throttle_test.go` out of `internal/httpapi` into `internal/auth`.

### 8. HTTP routing (`internal/httpapi/server.go`)

- Hand-rolled today: `http.NewServeMux()` with Go 1.22+ method and wildcard patterns (`"POST /challenges/{id}/patches/{patchID}"` in `httpapi/server.go:182-227`).
- Candidate: `github.com/go-chi/chi/v5` (v5.2.1, MIT licence).
- Comparison: Go 1.25's `net/http.ServeMux` natively supports method routing, `{id}` path parameters via `r.PathValue("id")`, and exact root matching `"GET /{$}"`. In addition, `routeOfMux` (`httpapi/metrics.go:380-390`) calls `_, pattern := mux.Handler(r)` before the handler chain runs. That ensures requests rejected by `withAuth` still get a bounded route label in `querycraft_requests_total` instead of `"unmatched"`. `chi` only populates `chi.RouteContext(r.Context()).RoutePattern()` after routing inside the mux.
- Verdict: Keep `net/http.ServeMux`.

## 4. Unnecessary complication

### Simple problems made hard

1. Two deadlines over one statement (`internal/httpapi/handlers.go:115` and `internal/engine/execute.go:42`): `handleQuery`, `handleEvaluate` (`handlers.go:153`), and `handleExplain` (`plan.go:58`) create `ctx, cancel := context.WithTimeout(r.Context(), limits.Timeout())`. `engine.Execute` immediately wraps `ctx` in a second `qctx, cancel := context.WithTimeout(ctx, limits.Timeout())` with the same duration. Simpler equivalent: in `engine.Execute`, only apply `context.WithTimeout` when `ctx` has no deadline (or a later deadline), while still creating `context.WithCancel(ctx)` for row/byte-cap cancellation (`execute.go:110`).
2. Request body decoded twice on `/answers/evaluate` (`internal/httpapi/handlers.go:214-228`): `evaluateRequest.UnmarshalJSON` calls `json.Unmarshal` twice on the same payload because `eval.Exercise` lacked `Perf` and `eval.Request` lacked `Dialect` and `Dataset`. Simpler equivalent: put `Dialect`, `Dataset`, and `Perf` on `eval.Request` and `eval.Exercise` and decode once.
3. Result rows marshalled twice (`internal/engine/execute.go:83-96`). In `rows.Next()`, `Execute` calls `json.Marshal(vals)`, adds `len(b)` to `byteCount`, and drops `b`. Then `writeJSON` (`httpapi/server.go:342`) marshals `res.Rows` to JSON a second time. Simpler equivalent: compute the JSON byte length of each normalized cell value in a helper `rowJSONBytes(vals)` without allocating or running reflection `json.Marshal` on every row.
4. Five sequential `SET LOCAL` round-trips before every PostgreSQL statement (`internal/engine/pgx.go:113-123, 169-199`). `PgxExecutor.Query` loops over `e.sessionSettings(ctx)` and executes 5 separate `tx.Exec` network round-trips before sending the learner's query. `search_path` is already set on `querycraft_learner` in `dataset/roles.go:63`. Simpler equivalent: set `lock_timeout = '2s'`, `idle_in_transaction_session_timeout = '10s'`, and `work_mem = '16MB'` once on `querycraft_learner` in `dataset/roles.go:57`, and execute only `SET LOCAL statement_timeout = <ms>` per query.
5. Server limits clamped to defaults (`internal/eval/eval.go:170` and `internal/engine/explain.go:99`). `handleEvaluate` (`httpapi/handlers.go:152`) and `handleExplain` (`httpapi/plan.go:55`) merge request limits with `s.cfg`. Then `eval.Evaluate` (`eval/eval.go:170`) and `engine.Explain` (`engine/explain.go:99`) call `limits.Merge(engine.DefaultLimits())`. That clamps any higher server limit back down to `DefaultLimits()`, repeating the bug `engine/execute.go:23-27` fixed by switching from `Merge` to `Fill`. Simpler equivalent: call `limits.Fill(engine.DefaultLimits())` in `eval.Evaluate` and `engine.Explain`.
6. Decoding PostgreSQL text columns only to discard them (`internal/engine/pgx.go:338-366` and `internal/engine/normalize.go:121-129`). `pgxRows.Values()` calls `r.rows.Values()` to parse `numeric` and date/time types into Go structs. Then `normalizeColumn` checks `if _, ok := textOIDs[oid]; ok { return string(raw) }`, throwing away the decoded value. Simpler equivalent: check `textOIDs` before decoding, or decode only non-`textOIDs` columns.
7. Copying the entire result matrix when columns are already aligned (`internal/eval/detail.go:60-61` and `internal/eval/compare.go:156-168`). `CompareDetailed` always calls `projectRows(qr.Rows, order)`. That allocates a new `[][]any` and a new `[]any` per row even when `order` is `[0, 1, ..., k-1]`. Simpler equivalent: if `order[i] == i` for all `i`, return `qr.Rows` without copying.
8. Re-sealing the template database on every lookup and reset (`internal/envdb/manager.go:240-260, 377, 455`). `ensure` and `reset` call `ensureTemplateFromLocked`, which queries `pg_database` and runs `SealTemplate`. That runs even when `EnsureTemplate` already built and sealed the template at boot (`cmd/api/main.go:224-229`). Simpler equivalent: record `sealed map[string]bool` on `envdb.Manager` so `ensureTemplateFromLocked` returns immediately once sealed.
9. Formatting the provider prompt twice per single-shot call (`internal/ai/transcript.go:334` and `internal/ai/http_provider.go:307`). `completeRaw` calls `rec.recordRequestMessages(req)`, which calls `userContent(req)` to concatenate prompt, repair context, and JSON Schema. Then `HTTPProvider.Complete` calls `p.messages(req)` -> `userContent(req)` a second time. Simpler equivalent: build the message slice once before calling `Provider.Complete`.

### Abstraction, not over-abstraction

The table below audits every interface, option struct, and forwarding layer in `backend/` against the two-implementation rule.

| Symbol and location | Implementations / callers | Verdict | Action |
| --- | --- | --- | --- |
| `engine.Executor` (`engine/engine.go:242`) | `PgxExecutor`, `SQLiteExecutor`, `UnavailableExecutor`, test fakes | **Keep** | Core execution seam; fold `Dialect() string` and `EngineLabel() string` into `Executor`. |
| `engine.Rows` (`engine/engine.go:203`) | `pgxRows`, `sqliteRows`, `fakeRows` | **Keep** | Core cursor seam across `pgx` and `sqlite_raw`. |
| `engine.Dialecter` (`engine/engine.go:93`) | 1 implementation (`SQLiteExecutor`) | **Delete** | Add `Dialect() string` to `Executor` (or return `"postgresql"` on `PgxExecutor` and `UnavailableExecutor`). |
| `engine.LabeledExecutor` (`engine/engine.go:113`) | 1 implementation (`SQLiteExecutor`) | **Delete** | Add `EngineLabel() string` to `Executor`. |
| `engine.ErrorClassifier` (`engine/engine.go:138`) | `SQLiteExecutor` and `PgxExecutor` (where `PgxExecutor.ClassifyError` at `pgx.go:284` just calls `pgCodeFor`, duplicating `classify`'s own fallback at `execute.go:224`) | **Delete** | Delete `PgxExecutor.ClassifyError`; dispatch error classification by `ex.Dialect()` in `classifyFor` (`execute.go:237`). |
| `engine.Diagnostician` (`engine/engine.go:237`) | 1 caller (`httpapi/metrics.go:348`) | **Delete** | Put `Diagnostics(ctx) map[string]any` on `Executor` (returning `nil` on `SQLiteExecutor`). |
| `engine.float64Valuer` (`engine/normalize.go:13`) | 0 production callers (`numeric` is in `textOIDs` and returns `string(raw)` before `normalizeValue`) | **Delete** | Delete `float64Valuer` (`normalize.go:13-15, 45-49`) and update `TestNormalizeValue` (`engine_test.go:314`). |
| `store.Store` (`store/store.go:47`) | `Postgres`, `SQLite`, `Memory` | **Keep** | Three real implementations. |
| `ai.Depot` (`ai/api.go:416`) | Implemented by the same 3 `store.Store` types | **Delete as `ai` type** | Move record types to `schema` and define `Depot` in `store` so `store` does not import `ai`. |
| `envdb.AccountLister` (`envdb/manager.go:678`) | 1 caller (`ReapUnreferenced` at `envdb/manager.go:688`) | **Delete** | Pass `keepAccountIDs []string` (from `st.UserIDs(ctx)` in `cmd/api/main.go:236`) directly to `ReapUnreferenced(ctx, ids)`, deleting the interface. |
| `ai.Provider` (`ai/provider.go:117`) | `HTTPProvider`, `FakeProvider`, test wrappers | **Keep** | Core model seam (ADR 0005). |
| `ai.secretRedactor` (`ai/transcript.go:185`) and anonymous `interface{ MissingCredential() bool }` (`ai/api.go:602`) | 1 implementation (`*HTTPProvider`) | **Delete** | Type-assert `*HTTPProvider` directly or add `Redact(string) string` to `Provider`. |
| `ai.Runner` (`ai/api.go:462`) and `ai.ProvidedRunner` (`ai/api.go:466`) | `*envdb.Manager` and test-only `ProvidedRunner` | **Keep `Runner`, move `ProvidedRunner`** | Move `ProvidedRunner` (a 1-field struct used only in tests) into `ai/helpers_test.go`. |
| `perfMarker` (`httpapi/handlers.go:94`) and `planExercise` (`httpapi/plan.go:45`) | 1-field wrapper structs `{ Perf *perf.PerfContract }` | **Delete** | Add `Perf *perf.PerfContract` directly to `eval.Exercise`. |
| 6 forwarding methods on `ai.Service`: `GenerateChallenge`, `GenerateCourse`, `Tutor`, `Critique`, `ReportDefect`, `ApplyPatch` (`ai/api.go:630-659`) | Each method only forwards its arguments to `s.generate`, `s.tutor`, `s.critique`, `s.reportDefect`, or `s.applyPatch` | **Delete** | Export `tutor`, `critique`, `reportDefect`, and `applyPatch` directly in `ai/review.go` instead of forwarding from `ai/api.go`. |
| `PgxExecutor.Explain` (`engine/explain.go:187-189`) | 0 callers in production (only called in `explain_test.go`) | **Delete** | Call `Explain(ctx, ex, ...)` directly. |

### Simple code over complex constructs

1. Named struct field over closures carrying state: `httpapi.UseAI` (`httpapi/ai.go:36`) passes `s.aiServiceFor` (a `ServiceFor` closure) into `ai.NewJobs`. `submitGenerationJob` (`httpapi/ai.go:210`) passes `OnFinish: s.recordJob` (another closure) on every `Jobs.Submit` call. Simpler construct: pass `ownerID string` to `GenerateCourse` and `GenerateChallenge`, give `ai.Jobs` one `*ai.Service`, and configure `OnFinish` once on `NewJobs`.
2. Parameter over wrapper type: `ownerScopedDepot` (`httpapi/ai.go:73-109`) wraps `ai.Depot` on every HTTP request just to set `rec.OwnerID = d.owner`. Simpler construct: pass `ownerID string` into `GenerateChallenge(ctx, ownerID, req)` and `GenerateCourse(ctx, ownerID, req)`.
3. Struct fields on `pipeline` over 3 private `context.Context` keys: `internal/ai` defines three private context keys (`progressKey` in `ai/jobs.go:111`, `transcriptKey` in `ai/transcript.go:201`, and `runIDKey` in `ai/transcript.go:220`). They pass `*transcriptRecorder`, `runID`, and `report func(Progress)` between functions inside the same `internal/ai` package. Simpler construct: store `recorder *transcriptRecorder` and `onProgress func(Progress)` as fields on `pipeline`. Delete all 3 context keys and their 6 helper functions (`WithProgress`, `progressFrom`, `WithTranscript`, `transcriptFrom`, `WithRunID`, `runIDFrom`).
4. Boolean parameter over function-pointer struct: `envKind` (`envdb/manager.go:314-335`) defines a struct with two closures (`template func(m *Manager) string` and `database func(m *Manager, accountID string) string`) to distinguish `challengeEnv` from `perfEnv`. Simpler construct: pass `perf bool` to `m.ensure(ctx, accountID, perf)` and `m.reset(ctx, accountID, perf)`.
5. Direct value over a 1-entry dispatch table: `dialect.Names()` and `dialect.ByName(name)` (`dialect/dialect.go:175-185`) implement a registry over a single hardcoded `"sqlite"` case and have zero production callers. Simpler construct: call `dialect.SQLite()` directly (as `engine/sqlite.go:260` already does) and delete `Names` and `ByName`.

### DRY: facts stated more than once

1. HTTP route table stated in 4 places: `httpapi/server.go:183-218` (`ServeMux` registration), `httpapi/server.go:292-306` (`requestErrorCode` prefix switch), `httpapi/metrics.go:185-221` (`operations` map), and `httpapi/openapi.go:31-860` (`openAPISpec` paths), plus `httpapi/openapi_test.go:11-48, 94-156`. Single place it should live: `internal/httpapi/routes.go` (`var routes = []routeSpec{...}`).
2. Contract JSON Schemas stated in both `contracts/*.schema.json` and Go string literals: 6 files in `contracts/` are restated inside `httpapi/openapi.go:861-1851`. `contracts/ai.schema.json` is also restated in `ai/fake.go:389-424` (`challengeSchema`), `ai/course.go:444-498` (`courseChapterSchema`), and `ai/course_agent.go:609-647` (`courseOutlineSchema`). Single place it should live: `contracts/*.schema.json`, embedded via `//go:embed`.
3. `QueryResult` error-code enum stated in 4 places: `contracts/query-result.schema.json:24`, `engine/engine.go:32-46`, `dialect/dialect.go:28-58`, and `httpapi/openapi.go:1842`. Single place in Go: `internal/schema` (or `internal/dialect`, imported by `engine`).
4. Metadata store SQL and list/decode logic stated 3 times: `store/postgres.go` + `challenges.go` + `courses.go` + `runs.go`, `store/sqlite.go` + `sqlite_depot.go`, and `store/memory.go`. Single place for record validation defaults and JSON column decoding: `internal/store/records.go` (`decodeRecord`, `decodeCourse`, `decodeRun`, and default status/timestamp initialization shared by all three backends).
5. Commerce table column definitions stated in 2 places in `internal/dataset`: `dataset/dataset.go:255-445` (`commerceCustomers`, `commerceProducts`, `commerceOrders`, `commerceOrderItems`) and `dataset/schema.go:51-80` (`commerceTables`). Single place it should live: `dataset/schema.go` (`commerceTables`), read by `dataset.go` when constructing `TableSpec.Columns`.
6. DSN credential-swapping logic stated in 3 places: `cmd/api/main.go:417-428` (`swapDSNUser`), `envdb/manager.go:656-664` (`learnerDSN`), and `pgtest/pgtest.go:96-105` (`LearnerDSN`). Single place it should live: `dataset.LearnerDSN(adminDSN, database, password string) (string, error)`.
7. SQL identifier quoting `quoteIdent` stated in 3 places: `dataset/schema.go:12-14`, `ai/pipeline.go:908-910`, and `httpapi/handlers.go:356-358` (`quoteSQLiteIdent`). Single place it should live: `dialect.QuoteIdent(name string) string`.
8. Secret redaction regexes split across 2 packages: `engine/redact.go:13-27` (`RedactSecrets` for URL userinfo and `password=`) and `ai/transcript.go:157-179` (`redactText` adding `Bearer`, named `api_key`/`secret`/`token`, and `sk-`/`pk-`/`rk-` prefixes). Single place it should live: `schema.RedactSecrets(s string) string` (so `QueryError` and logs also scrub bearer tokens and API keys, not only DSN passwords).

### YAGNI: dead knobs, unused options, and unreferenced exports

1. `ai.Config.AdversarialFixtures` (`internal/ai/api.go:557`) and `ai.Fixture` (`internal/ai/api.go:561-564`): never set in production (`cmd/api` never sets it; only `pipeline_test.go` uses it).
2. `ai.GenerationRequest.Steps` (`internal/ai/api.go:156`) and `maxSteps` validation (`internal/ai/validate.go:28, 58-60, 69`): validated on input and deep-copied in `store/memory.go:209`, but never read by `requestPrompt`, `coursePrompt`, or any pipeline stage.
3. `ai.CodeAIAvailable` (`internal/ai/api.go:34`): exported constant with zero references in the repository.
4. `ai.ErrNotConfigured` (`internal/ai/api.go:133`) and `ai.ProvidedRunner` (`internal/ai/api.go:466-477`): `ProvidedRunner` is a test stub in production code, and `ErrNotConfigured` is referenced nowhere else.
5. `ai.validateCoursePlan` (`internal/ai/course.go:228-271`), `CoursePlan.sectionCount` (`internal/ai/course.go:215-221`), and `FakeProvider.courseBundle` (`internal/ai/fake.go:497-503`): M7 whole-course-in-one-reply leftovers; the M8 course agent validates outlines with `validateCourseOutline` and chapters with `validateCourseChapter`. None of the three has a production caller.
6. `engine.SQLiteConfig.DSN` (`internal/engine/sqlite.go:135`) and `parseSQLiteDSN` (`internal/engine/sqlite_raw.go:194-230`): `sqlite.go:130` explicitly notes `UNUSED BY PRODUCTION: no production path sets it`.
7. `engine.float64Valuer` (`internal/engine/normalize.go:13-15, 45-49`) and `case time.Time:` (`internal/engine/normalize.go:38-39`): unreachable from `PgxExecutor`. `numeric` and all date/time OIDs are in `textOIDs` (`normalize.go:105-113`) and return `string(raw)` in `normalizeColumn` before `normalizeValue` runs.
8. `dataset.LoadIntoTemplate` (`internal/dataset/template.go:140-142`), `dataset.ConnectAsLearner` (`internal/dataset/template.go:85-95`), `dataset.EnvironmentDatabaseSize` (`internal/dataset/template.go:298-304`), and `dataset.ServerConnectionFacts` / `dataset.ConnectionFacts` (`internal/dataset/template.go:308-328`): spike helpers with zero production callers. Move to `dataset/template_test.go`.
9. `dialect.Names`, `dialect.ByName`, `dialect.Affinity`, `dialect.AffinityClasses`, `dialect.ErrorCodes`, and package-level `dialect.ClassifyError` (`internal/dialect/dialect.go:46, 175, 179, 190, 202` and `internal/dialect/sqlite.go:52`): zero production callers; move to `dialect/dialect_test.go`.
10. `eval.Compare` (`internal/eval/compare.go:90`), `eval.CompareColumns` (`internal/eval/compare.go:73`), and `eval.ErrorResponse` (`internal/eval/eval.go:161-163`): zero production callers (`eval` and `ai` call `CompareDetailed` and `CompareColumnsDetailed`; `ErrorResponse` is unreferenced).
11. Un-scoped `Depot.Challenges(ctx)`, `Depot.Courses(ctx)`, `Depot.Runs(ctx)` (`internal/ai/api.go:419, 428, 444`) and `Service.Challenges(ctx)` (`internal/ai/api.go:662-667`): zero production callers across `Postgres`, `SQLite`, and `Memory` (`httpapi` always calls `ChallengesFor`, `CoursesFor`, and `RunsFor`).

### Where DRY is wrong: near-duplicates that must stay separate

1. `pipeline.exhausted` (`internal/ai/pipeline.go:449-463`) versus `pipeline.courseRefused` (`internal/ai/course_agent.go:1275-1318`): Both switch on `last.Stage` to select an error code when a run is rejected, but they differ on `StageValidateArtifact`. In `exhausted`, a challenge that fails `StageValidateArtifact` after repairs reports `CodeRepairExhausted`. In `courseRefused` (`ai/course_agent.go:1307-1308`), a course in which no chapter verified after `StageValidateArtifact` reports `CodeInvalidArtifact`. Merging them changes the wire error code and fails `course_agent_test.go`.
2. `sqliteUnframe` in `internal/engine/sqlite.go:348-359` versus `sqliteUnframe` in `web/src/api/sqlite.ts` versus `Profile.ClassifyError` in `internal/dialect/sqlite.go:69-80`: Both unframers strip driver wrappers so `Profile.ClassifyError` sees raw `sqlite3_errmsg` sentences. They must stay separate from `dialect` and from each other. `modernc.org/sqlite` formats errors as `"<errstr>: <msg> (<rc>)"`, `@sqlite.org/sqlite-wasm` formats them as `"<CODE>: sqlite3 result code <n>: <msg>"`, and `sqlite_raw.go` emits raw `sqlite3_errmsg` strings.
3. `challengeEnv` versus `perfEnv` per-account PostgreSQL databases (`internal/envdb/manager.go:321-334`) and `LoadWritableIntoTemplate` (`internal/dataset/template.go:157`) versus `LoadPerfIntoTemplate` (`internal/dataset/perf.go:55`): `LoadPerfIntoTemplate` runs `ALTER TABLE ... OWNER TO querycraft_learner` (`dataset/perf.go:91-99`) because PostgreSQL requires table ownership for `CREATE INDEX`. `LoadWritableIntoTemplate` withholds ownership so a learner in a standard course cannot `DROP` or `TRUNCATE` the seeded `commerce` tables. Merging the two templates or databases would either break `CREATE INDEX` in performance lessons or allow `DROP TABLE` in commerce lessons.
4. `eval.multisetEqual` (`internal/eval/compare.go:303-335`) versus `eval.compareRowsDetailed` (`internal/eval/detail.go:78-143`): Both compare row sets under `duplicates: "ignored"` and `"preserved"`. However, `compareRowsDetailed` returns three checklist pointers (`RowsCountOK`, `ValuesOK`, `RowOrderOK`). It calls `multisetEqual` inside the `exact` row-order failure branch (`detail.go:105`) to distinguish wrong row order (`VALUE_MISMATCH` + `ROW_ORDER_MISMATCH`) from wrong values (`VALUE_MISMATCH` alone).
5. `Limits.Merge` (`internal/engine/engine.go:165-176`) versus `Limits.Fill` (`internal/engine/engine.go:184-195`): `Merge` clamps a client-supplied limit down to the server ceiling (`l.MaxRows > fallback.MaxRows`), preventing a request body from raising the cap. `Fill` populates unset zero fields (`l.MaxRows <= 0`) without clamping an operator's higher `QC_MAX_ROWS` ceiling down to `DefaultLimits()`.

## What could not be determined

1. This environment has no `QC_AI_KEY` or `QC_AI_BASE_URL`, so live hosted AI provider latency and rate-limit retry behavior remain unmeasured.
2. The task brief forbids running `make test`, `make verify`, or state-changing commands against the uncommitted working tree, so `go test ./...` did not run.
