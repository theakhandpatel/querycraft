# httpapi — slop audit (wave 1)

Scope: production files under `backend/internal/httpapi/`. Test files are evidence
only. Read-only pass: no repository file was changed except this report.
`cd backend && go build ./internal/httpapi/` is green (run 2026-10-18).

## Verdict

The wide 2026-10-17 review is largely landed: the route table is the one source
(`operations`, `operationOf`, `isStatementRoute` and the `errorCode` type switch
are gone), every writer names its code through `noteError`, and `requestErrorCode`
reads the table's `vocab` field. What remains is the part that move did not touch:
the "AI is off" refusal is still hand-built at ten sites in two shapes, and the
`runs.go` copy has drifted to a second sentence. `requireAI`, `requireCourseDepot`,
`submitJob` and `list` are named in `docs/target-architecture.md:52` and none
exists yet; the two duplicated job submissions and four list handlers are still
copy-pasted. Four small deletions are certain: a helper return value that is always
zero, an unreachable availability branch, an exported method with one in-package
caller, and a one-use marker type. The rest of the package is earned: the route
table, the single observability recorder, the owner-scoped depot, the login
throttle and the dialect/dataset refusal taxonomy each carry one fact once.

## Delete

### `sqliteTableColumns`'s `dataset.TableInfo` return — handlers.go:330

The function returns `([][]string, dataset.TableInfo, *engine.QueryError)`, and the
`TableInfo` half is `dataset.TableInfo{}` on both the success path (line 349) and
the failure path (line 333). The only caller overwrites all four fields it has:
`info.Name` (295), `info.Cols` (296), `info.Preview` (301), `info.Rows` (302).
`dataset.TableInfo` has exactly those four fields (dataset/dataset.go:42-47), so
the returned value is fully discarded. `grep sqliteTableColumns` finds no other
caller. Remedy: drop the second return, return `([][]string, *engine.QueryError)`,
and write `info := dataset.TableInfo{Name: name}` at the call site.

### `runsOff` — runs.go:47

A six-line helper that writes the AI-off envelope for `GET /runs`. It duplicates
`aiOff` (ai.go:798) with the same `{"ok":false, key:[], error:{...}}` shape, and
its sentence has drifted: `"Generation is off: no generation service is
configured."` (runs.go:54) versus `aiOffMessage` (ai.go:312). Its only caller is
`handleListRuns` (runs.go:67). Remedy: delete `runsOff` and call
`aiOff(w, "runs", []schema.RunSummary{})`. The test asserts only the code and
status, not the sentence (runs_test.go:245-258), so the message change is safe.

### `aiAvailability`'s second `if !ok` branch — ai.go:103-108

The function already returned when `!s.aiConfigured()` (ai.go:93).
`aiConfigured` is `_, ok := s.currentAI(); return ok` (ai.go:85-88), so reaching
line 103 means `ok` was true. `aiCfg` is written only at ai.go:46 (`s.aiCfg =
&cfg`, never nil) and read at ai.go:67, so the second `currentAI()` cannot report
false. Lines 103-108 are unreachable and byte-identical to 94-101. Remedy: delete
103-108 and keep `cfg, _ := s.currentAI(); return ai.NewService(cfg).Available()`.

### `UseDialect` export — server.go:147

Exported, documented as "records that the gateway can serve another dialect", but
`grep UseDialect` over the repo returns only this file: the definition and its one
caller, `UseSQLite` (server.go:173), in the same package. No test and no
`cmd/api` call it. The future-adapter it names does not exist. Remedy: make it
`useDialect` (unexported) or fold its three lines into `UseSQLite`. Keep the
behaviour.

### `perfMarker` — handlers.go:93

A one-field named struct used once, inside `evaluateRequest.UnmarshalJSON`
(handlers.go:236, 242), to give the shared `exercise` key a home during the second
decode. It names nothing a reader needs. Remedy: inline the field into the
anonymous routing struct:
`Exercise *planExercise \`json:"exercise"\``, and drop the type.

## Collapse

### The AI-off refusal family — ai.go and runs.go

Ten sites hand-build the same refusal. Nine use
`writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: aiOffMessage})`
at ai.go:375, 401, 503, 527, 567, 669, 694, 719, 751. The tenth, runs.go:96, is the
same call with the drifted sentence typed inline. The doc at ai.go:310 says
`aiOffMessage` is carried by "every service-gated handler", which `runs.go`
falsifies. The target architecture (`docs/target-architecture.md:52`) names the
fix, `requireAI`, and it does not exist yet. Exact shape:

```go
// requireAI resolves the caller and the owner-scoped generation service, or
// writes the one AI-off refusal and reports failure.
func (s *Server) requireAI(w http.ResponseWriter, r *http.Request) (store.User, *ai.Service, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return store.User{}, nil, false
	}
	svc := s.aiServiceFor(u.ID)
	if svc == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: aiOffMessage})
		return u, nil, false
	}
	return u, svc, true
}

// requireCourseDepot is the depot-gated twin: the course and run reads use the
// configured depot directly (ai.go:417), so they need the same refusal over a
// different zero value.
func (s *Server) requireCourseDepot(w http.ResponseWriter, r *http.Request) (store.User, schema.Depot, bool) {
	u, ok := s.requireUser(w, r)
	if !ok {
		return store.User{}, nil, false
	}
	depot := s.aiCourseDepot()
	if depot == nil {
		writeAIError(w, &schema.Error{Code: ai.CodeUnavailable, Message: aiOffMessage})
		return u, nil, false
	}
	return u, depot, true
}
```

`requireAI` replaces the six `svc == nil` sites (375, 401, 669, 694, 719, 751)
and the service lookup inside `runGeneration` (ai.go:190) and `handleBuildCourse`
(ai.go:616). `requireCourseDepot` replaces the three depot sites (503, 527, 567),
runs.go:96, and the `depot == nil` guards feeding `aiOff` (ai.go:439, 468;
runs.go:66). Note `handlePatch` (ai.go:748) needs `u.Email`, which the returned
`store.User` carries.

### `submitGenerationJob` / `submitCourseBuild` — ai.go:260 and ai.go:632

Same twenty lines: build `jobs.Submission`, `Submit`, type-assert `*schema.Error`,
write `unavailableOutcome` with the caller's code, else `writeAIError`, then set
the running gauge and answer `202`. The only differences are
`Request: req` versus `BuildCourseID: courseID` and the kind literal. Remedy: one
`submitJob(w http.ResponseWriter, sub jobs.Submission)` behind both, with
`OnFinish: s.recordJob` set by the helper.

### The four list handlers — ai.go:349, ai.go:433, ai.go:463, runs.go:60

`handleListChallenges`, `handleListCourses`, `handleAdminCourses` and
`handleListRuns` share one shape: resolve the caller (`requireUser` or
`requireAdmin`), guard the store, fetch, map each record through a `*View` that
blanks `OwnerID`, write `{"ok":true, <key>:[...]}`. `handleListCourses` and
`handleAdminCourses` differ by two tokens (`requireUser`/`CoursesFor` versus
`requireAdmin`/`Courses`). Remedy: one `list(w, r, listSpec)` taking the require
function, the off-writer, the JSON key and a fetch closure. This is the same item
the 2026-10-17 review raised as finding 4; it did not land.

### One sentence for "generation is off" — runs.go:54, runs.go:96

`aiOffMessage` (ai.go:312) is the canonical sentence and `runs.go` types a second
one twice. Delete the literal and use `aiOffMessage` through `requireCourseDepot`
or `aiOff`. (Cross-reference: `internal/ai/jobs/jobs.go:208` types the same
sentence a third time, out of this domain.)

### `planExecutor`'s perf branch — plan.go:159-170

The `dataset.PerfDatasetID` case repeats the resolution in `perfExecutor`
(dataset.go:99-116): check signed-in, check `s.envs == nil`, call
`EnsurePerf(ctx, u.ID)`. Only the refusal type differs (a `*perf.PlanBundle` from
`refusedPlan` versus an `*engine.QueryResult`). Remedy: have `perfExecutor` return
the executor and an `error`, and let each caller build its own refusal from it,
or return the resolved `(engine.Executor, string reason, error)`.

### `planRequest` re-declares the routing fields — plan.go:36-43

`planRequest` lists `Dialect string` and `Dataset string` directly, while
`queryRequest` and `evaluateRequest` embed `statementRequest` (handlers.go:85).
The doc at handlers.go:81-84 admits `/explain` "declares the same two fields in its
own body". It resolves them through the same helpers, so the JSON decode is
identical if it embeds `statementRequest`. Remedy: embed `statementRequest` and
drop the two fields.

### The error-envelope shapes — environment.go:93, 106, 120; ai.go:766, 798; runs.go:47; ai.go:585

Seven writers produce five shapes for the same fact: `errorEnvelope` and
`courseSetupFailed` and `environmentUnavailable` write
`{ok:false,error:{code,message[,(detail)]}}`; `writeAIError` writes
`{error: aiErr}` with no `ok`; `aiOff` and `runsOff` add the route's empty
collection; `handleBuildCourse` (ai.go:585-590) hand-builds the
`writeAIError` shape to force `409` because `writeAIError` maps
`ai.CodeInvalidRequest` to `400`. The target architecture's "one typed envelope,
code passed explicitly" (`docs/target-architecture.md:39`) is not landed. This is
the highest-leverage collapse in the package, and it is a wire change, so it needs
`contract_test.go` behind it.

### `handleAdminAISave`'s repeated `AI_INVALID_REQUEST` refusals — admin_ai.go:142, 149, 154, 160, 165, 187, 191

Seven `errorEnvelope(w, status, "AI_INVALID_REQUEST", msg)` calls. The first five
are 400s with distinct sentences and want a `badAIRequest(w, message)` helper. The
last two are `500`s carrying the code `AI_INVALID_REQUEST`, which names a client
fault for a server fault. The message at admin_ai.go:160-161 lists only
`off, fake, openai-compatible`, but the switch above accepts `pi-ai` (133, 147)
and `providerFromAISetting` implements it (245). Remedy: extract the 400 helper,
and either name `pi-ai` in the message or state that aliases are accepted, so the
message matches the switch.

### `"Sign in to continue."` refused twice — auth.go:79, auth.go:97

`withRouteAuth` and `requireUser` each write
`errorEnvelope(w, 401, "AUTH_REQUIRED", "Sign in to continue.")`. One body, two
call sites. Remedy: one `authRequired(w)` helper. The code is also asserted by
tests (auth_test.go:174, server_test.go:578) and by the log
(logging_test.go:118), so the writer must stay `errorEnvelope`.

### Course reset setup block — environment.go:226-245 and environment.go:314-328

Both `handleEnvironmentReset` (PostgreSQL) and `resetSQLiteEnvironment` resolve the
named course through `s.courseSetup`, refuse with the same `notFound` sentence,
then run `applyCourseSetup`. The resolve-and-refuse half is duplicated. Remedy:
one helper `resolveCourseSetup(w, r, ownerID) (schema.SetupPlan, string, bool)`
returning the course id and whether to continue.

### `defaultAICallTimeoutMS` — admin_ai.go:53

`const defaultAICallTimeoutMS = 120000` duplicates the shipped
`config.Settings.AITimeoutMS = 120000` (config/config.go:112). No drift guard
exists: `grep defaultAICallTimeoutMS` matches only the definition and its one use
(admin_ai.go:170). The dependency runs `config -> httpapi`, so httpapi cannot read
`config`. Remedy: move the number to a neutral owner (for example
`ai.DefaultCallTimeoutMS`) that both read.

### `handleAdminAIModels`'s local `entry` — admin_ai.go:108-112

The local `entry{Provider,Model,Name}` copies `piai.CatalogEntry`
(pi_ai.go:98-102) field for field; the loop at 114-116 only re-tags it because
`CatalogEntry` has no JSON tags. Remedy: add `json:"provider"` etc. to
`CatalogEntry` and return `piai.Catalog()` directly.

### `handleGenerateChallenge` / `handleGenerateCourse` — ai.go:168 and ai.go:173

Two one-line functions, each with one caller in the route table (routes.go:106-107),
each binding a constant kind. Remedy: inline as closures in the table, or keep if
the named handler is judged clearer. Low value either way.

## Move / split

### `login_throttle.go` is sign-in policy in the transport package — login_throttle.go:1-225

The throttle keys on `auth.NormalizeEmail` (auth.go:197) and its `clientIP`
(login_throttle.go:109) takes an `*http.Request` only to read `RemoteAddr`. The
2026-10-17 review's finding 6 raised the same move; it did not land. Remedy: move
the file to `internal/auth` and reduce `clientIP` to the address string. Optional,
behaviour-preserving.

### `handlers.go` mixes statement routes with SQLite introspection — handlers.go:246-378

`handleSQLiteTables` and the `sqliteTableNames`/`sqliteTableColumns`/
`sqliteRowCount`/`quoteSQLiteIdent` helpers are a self-contained concern inside the
statement-handler file. Remedy: move them to `sqlite_tables.go`, leaving
handlers.go with the statement, grading, progress and event routes.

### `admin_ai.go` mixes HTTP handlers with the boot-time config contract — admin_ai.go:233-286

`providerFromAISetting` and `AIConfigFromSetting` are the setting-to-config
mapping that `cmd/api` calls at boot (cmd/api/main.go:300); they are not HTTP.
Remedy: move the mapping beside the provider construction so `cmd/api` does not
reach into the transport package for it. Optional.

## Earned (checked, keep)

- Route table as the single source: `routes()` (routes.go:69) carries `operation` and `vocab`; `Handler` builds the `operations` map once (server.go:204-207). `grep isStatementRoute|operationOf|func errorCode` returns nothing.
- `requestErrorCode` reads the table's `vocab` (server.go:266-284) rather than the URL; exercised by server_test.go:652.
- Explicit `noteError` at every code-known writer (metrics.go:86; 12 call sites); `writeJSON` no longer introspects (server.go:300-309).
- One observability recorder: `statusRecorder` + `withObservability` (metrics.go:68-148); one request, one log line, one counter.
- `aiOffMessage` as the single sentence for the capability view and the four `aiOff` sites (ai.go:100, 312, 356, 440, 469).
- `Registry()` (server.go:178) has a production consumer: envdb instrumentation at cmd/api/main.go:209.
- `DefaultLoginMaxFailures`/`WindowMS`/`MaxTracked` (login_throttle.go:43-49) feed `config.Defaults()` (config/config.go:95-97), so one number per fact.
- `ownerScopedDepot` (ai.go:127-163) stamps the session owner and scopes list reads; the wrapper-overrides-only-writes split is documented and real.
- `statementExecutorFor` / `resolveDataset` / `refusingPerfDialect` (dialect.go:43, dataset.go:51, 85) keep the three statement routes on one taxonomy.
- `handleSQLiteTables` returns declared types and an empty header preview rather than invented rows (handlers.go:270-306).
- `writeSQLiteTablesError` (handlers.go:376) is a one-line forward, but it names the 400 status for two call sites; keep.
- Login throttle: `clientIP` deliberately ignores `X-Forwarded-For` (login_throttle.go:106-115); the bound and whole-second `Retry-After` are tested.

## UNCERTAIN

- Changing the `runs.go` sentence to `aiOffMessage` looks safe (tests assert code/status only, runs_test.go:245-258), but a client could read the text. Needs one contract run, or an owner decision to treat the message as non-contractual.
- `UseDialect` may be a deliberate adapter seam (server.go:86-91). It has no consumer today; the owner decides whether the export buys anything.
- The AI and preference codes the writers emit (`AI_UNAVAILABLE`, `AI_INVALID_REQUEST`, `PREFERENCES_UNAVAILABLE`, ai.go:792, admin_ai.go:142, preferences.go:32) are absent from the `ErrorEnvelope.code` enum in apidocs.go:2049. Whether the wire document should list them is an owner decision; the handlers are honest either way.
- `handleBuildCourse`'s `409` body (ai.go:585) hand-builds `writeAIError`'s shape to override the status. Whether `writeAIError` should gain a status parameter or the 409 stays a one-off is an owner call.
- `planRequest` embedding `statementRequest` is behaviour-preserving in JSON terms; confirm no schema tool reads the struct tags for `/explain` before changing it.
