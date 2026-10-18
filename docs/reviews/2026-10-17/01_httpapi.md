# HTTP layer review: `backend/internal/httpapi`, `cmd/api`, `webui`, `apidocs`

Scope: the HTTP seam only. `internal/httpapi` (3,925 non-test lines, 40 handlers,
7,811 test lines), `cmd/api` (main.go + ai.go), `internal/webui` (embed +
compression), `internal/apidocs` (the OpenAPI document). Read-only pass: no file
under the repository was changed. `cd backend && go build ./... && go vet ./...`
is green (run 2026-10-17); the test suite was not run, so every remedy below is
verified against the build and the code, not against a green `go test`.

## Verdict

The HTTP layer is not a hand-rolled mess and it is not over-abstracted in the
places the hypothesis expects. Routing already uses the canonical modern
stdlib answer, Go 1.22 method-plus-wildcard `http.ServeMux` patterns, and the
middleware chain (CORS, request id, observability, auth, route-auth) is six
small functions with a clear order. Adopting `chi`, `huma`, `oapi-codegen` or
`go-playground/validator` here would add a dependency to buy indirection the
stdlib already provides, and I recommend none of them. The real problems are
narrower and more specific: one over-clever abstraction (`errorCode`) silently
drops error codes for whole route families, the routing knowledge is declared
four times instead of once, and a handful of handler shapes and the "AI is off"
refusal are copy-pasted instead of extracted. Each of those has a code-judo
remedy that deletes code rather than moves it. The package is cohesive and
correctly sized; the one file past 1,000 lines is a hand-authored JSON string,
not logic.

## Findings

### 1. `errorCode` reverse-engineers the response and silently loses error codes for AI and plan routes

`writeJSON` (`internal/httpapi/server.go:305`) calls `noteError(w, errorCode(v))`
on every response, and `errorCode` (`internal/httpapi/metrics.go:95-124`) is a
five-case type switch that guesses the stable error code from the shape of the
value it was handed: `engine.QueryResult`, `*engine.QueryResult`, `eval.Response`,
`*eval.Response`, and `map[string]any` whose `"error"` is either
`map[string]string` or `map[string]any`. The comment at `server.go:302` claims
"a new endpoint cannot quietly ship without an error_code". It did. The switch has
no case for `*schema.Error`, `schema.Outcome` or `*perf.PlanBundle`, so three
route families answer through `writeJSON` and log an empty code. `writeAIError`
(`ai.go:773-798`) writes `map[string]any{"error": aiErr}` where `aiErr` is
`*schema.Error`; neither the `map[string]string` nor the `map[string]any`
assertion in `errorCode` can type-assert that, so it returns `""`.
`runGeneration` writes a `schema.Outcome` (`ai.go:235`) and `handleExplain`
writes a `*perf.PlanBundle` (`plan.go:126`); neither is a case. Consequence:
every `/tutor`, `/challenges/*`, `/courses/*` and `/explain` failure logs a
request line with no `error_code` attribute, while every other failure carries
one. The per-family counters (`recordStatement`, `recordPlan`,
`recordGeneration`) read the code directly and are unaffected, which is exactly
why the gap stayed invisible. The root cause is not the switch but the shape of
the error contract: the gateway writes an error envelope in at least five
different values (`errorEnvelope` in `environment.go:93`; `writeAIError`;
`courseSetupFailed` and `environmentUnavailable` in `environment.go:105,118`;
`runsOff` in `runs.go:47`; and four inline `map[string]any{"ok": false, ...}`
literals in `ai.go:295,355,442,474`). Because the encoder receives `any`, the
metrics layer must introspect to recover a fact the caller already knew.
Remedy: give the layer one typed `ErrorEnvelope` value and make every error path
write it through a single helper that passes the code to `noteError` explicitly;
delete `errorCode`'s type switch entirely. This also removes the drift the
switch exists to paper over: `writeAIError` emits no `"ok": false` (the
`notFound` comment at `server.go:315` says "ok:false like every other error
envelope"), and it emits codes such as `AI_UNAVAILABLE` that are absent from the
`ErrorEnvelope.code` enum in `apidocs.go`, so AI error responses do not match the
schema their own routes cite.

### 2. The route table is the declared source, but routing knowledge is stated four times

`routes()` (`internal/httpapi/routes.go:46-114`) is a single table of method,
path, handler and auth mode, and the middleware already treats it as the
source (`withRouteAuth` in `auth.go:67` builds its allow-map from it). Three
other representations of the same knowledge sit beside it. `operations`
(`metrics.go:185-221`) is a 35-entry `map[string]string` keyed by path string,
consulted only by `operationOf` (`metrics.go:225`) to produce a log label, with a
default of `"other"`. `isStatementRoute` (`server.go:294-300`) is a second
path-set, this time maintained as a `switch` over three literals. `requestErrorCode`
(`server.go:261-275`) is a third, dispatching on `strings.HasPrefix(r.URL.Path,
"/courses/")`, `"/challenges/"`, `r.URL.Path == "/tutor"` and `"/auth/"` to pick
the body-error vocabulary. The OpenAPI document (`apidocs.go`) is the fourth. The
first three are exactly the "knowledge stated in multiple places" the project's
own 2026-10-16 simplification pass flagged as a proposal; they have already
drifted (`GET /docs/` is in the table and the mux but documented once). The
prefix switch is also the "spaghetti in a shared path" smell the review skill
calls out: it decides an error code from a raw URL prefix rather than from the
declared route the mux already resolved. Remedy (the best code-judo move in the
package): add an `operation string` field and a small `bodyVocabulary` field to
the `route` struct, populate them in the table that already lists every route,
and delete `operations`, `operationOf` and `isStatementRoute`. `requestErrorCode`
becomes a read of the field plus the `empty` flag. One table, one source, and the
access-log vocabulary can no longer contain a route the router does not serve.

### 3. The "AI is off" refusal is copy-pasted seventeen times in three different shapes

The string `"AI is off: no generation service is configured."` occurs 17 times in
`ai.go` alone (`grep -c` on that literal). The guards are the `if svc == nil`
blocks at `ai.go:191,354,376,402,624,675,700,725,757`; the `if depot == nil`
blocks at `ai.go:441,473,510,534,574` and `runs.go:65,94`; and the `s.jobs ==
nil` block at `ai.go:294`. They answer in three distinct envelopes: the
`schema.Outcome` built by `unavailableOutcome` (`ai.go:314`), the inline
`map[string]any{"ok": false, <list>: []..., "error": map[string]string{...}}`
literal repeated at `ai.go:295,355,442,474`, and
`writeAIError(&schema.Error{Code: ai.CodeUnavailable, ...})` at the nine
`svc == nil` sites. A reader cannot tell from any one site whether the AI is off,
the depot is missing, or the response shape is deliberate, because the same fact
is expressed three ways. Remedy: one accessor, `func (s *Server) requireAI(w, r)
(*ai.Service, bool)`, that resolves the caller, checks the configuration, writes
the single canonical off-envelope (or the outcome, once finding 1 gives the layer
a typed envelope) and reports success; the depot readers get an analogous
`requireCourseDepot`. This deletes nine near-identical blocks and collapses the
message literal to one constant. The existing `handleGenerateChallenge` /
`handleGenerateCourse` delegation to `runGeneration` is the pattern to follow.

### 4. Duplicated handler shapes: two submission helpers and four list handlers

`submitGenerationJob` (`ai.go:260-281`) and `submitCourseBuild` (`ai.go:639-660`)
are the same twenty lines: build a `jobs.Submission`, `Submit`, branch on
`*schema.Error`, set the running gauge, write `202` with the job view. They differ
only in the `OwnerID`/`Kind`/`BuildCourseID` fields of the submission. The same
duplication runs through the list routes: `handleListChallenges` (`ai.go:348`),
`handleListCourses` (`ai.go:435`), `handleAdminCourses` (`ai.go:468`) and
`handleListRuns` (`runs.go:59`) share one shape, `requireUser`/`requireAdmin`,
guard the depot, fetch, map each record through a `*View` that blanks `OwnerID`,
write `{"ok": true, <key>: [...]}`. The difference between `handleListCourses`
and `handleAdminCourses` is two tokens (the require call and `CoursesFor` vs
`Courses`). Remedy: extract one `submitJob(w, r, submission, kind)` behind both
submissions, and one generic `list(w, r, listSpec)` taking the require function,
the off-envelope, the JSON key and a fetch closure. This is the classic
"copy-pasted logic instead of extracted helpers" the skill flags; the payoff is
that a change to the list envelope or the job-refusal status is one edit.

### 5. `webui.fmtInt` is a hand-rolled `strconv.Itoa`

`fmtInt` (`internal/webui/compress.go:119-141`) renders an `int` in decimal with a
`[20]byte` scratch buffer and a negation branch. Its own comment says it exists
"without strconv, which this package would otherwise import for one call", and it
has exactly one caller (`compress.go:89`, the `Content-Length` header). This is
the anti-slop pattern the repo's own conventions forbid: a bespoke helper where a
standard-library function does the job, chosen to avoid one import. Remedy:
`import "strconv"` and call `strconv.Itoa(len(body))`; delete twenty-two lines and
a comment that explains the code away. This is the highest-certainty, lowest-risk
deletion in the review and should ship on its own.

### 6. Size, decomposition and the sign-in throttle's home

The package does not breach the 1,000-line rule. `internal/httpapi` is 3,925
non-test lines over 40 handlers in 14 topic files, and `ai.go` at 843 lines is
the largest handler file but is internally organised by concern and under the
bar. Split the package only if a concern stops being HTTP. The one file past
1,000 lines is `internal/apidocs/apidocs.go` at 2,078, and 1,893 of those are a
single hand-authored JSON string literal. That is data, not logic, so the
file-size rule does not bite; but a 1,893-line string in a Go file is not
diffable, not lintable and not editable with JSON tooling, and drift from it is
caught for security and 401 only (`openapi_test.go`). If that string moves to
`apidocs/spec.json` and is pulled in with `go:embed`, it stays dependency-free
and becomes a JSON document an editor can validate. One genuine layering nit from
the project's own `docs/backend-decomposition.md` open list: `login_throttle.go`
is sign-in *policy*, but it lives in the HTTP package and its `clientIP` helper
(`login_throttle.go:109`) takes an `*http.Request` to read `RemoteAddr`.
`internal/auth` already owns sessions and the very `NormalizeEmail` this throttle
keys on, so the throttle belongs there, with `clientIP` reduced to the address
string. This is a move, not a rewrite, and it is optional.

## Third-party candidates, weighed

| Candidate | What it would replace | Verdict |
| --- | --- | --- |
| `net/http` `ServeMux` (Go 1.22 patterns) | already in use in `Handler()` | keep, this *is* the canonical answer |
| `chi` | the six-function chain and per-route middleware | no; would not delete a layer, and its `RealIP` middleware is the header-trusting behaviour the throttle deliberately rejects |
| `huma` / `oapi-codegen` | the hand-authored `apidocs.go` and typed request structs | no, now: the wire types (`engine`, `eval`, `schema`) are shared with the engine and the `contracts/*.schema.json` seam, so spec-first generation inverts the existing source of truth and rewrites the public wire for the payoff of one embedded file |
| `go-playground/validator` | `auth.ValidateEmail` / `ValidatePassword`, the `admin_ai` provider switch | no; the rules are domain rules (provider names, model refs, DSN shape) already owned by `auth` and `ai`, not expressible as struct tags |
| `golang.org/x/time/rate` | `login_throttle.go` | no; a token bucket still needs the keyed, bounded, evicting map and the whole-second `Retry-After`, so the dependency buys nothing the 225 lines do not already do |

The honest answer to "third-party packages already exist for this" is: for this
layer, none. `net/http` plus `encoding/json` is the right substrate, and the only
tool worth reaching for is `go:embed` for the OpenAPI string, which is stdlib.

## Proposed remediation sequence

1. `fmtInt` to `strconv.Itoa` (finding 5). Independent, trivial, no behaviour
   change.
2. One typed `ErrorEnvelope` with an explicit error code at write time; delete
   `errorCode`'s type switch (finding 1). Do this before the AI helpers so the
   canonical off-envelope has a shape to target. Rewrites ~26 `errorEnvelope`
   sites and `writeAIError`; the `ok` and enum drifts close as a side effect.
3. Fold `operation` and the body vocabulary into the `route` struct; delete
   `operations`, `operationOf` and `isStatementRoute` (finding 2). Small, and its
   own diff.
4. Add `requireAI` / `requireCourseDepot` and collapse the 17 refusals (finding
   3), now that step 2 gives them one envelope.
5. Extract the `submitJob` and `list` helpers (finding 4).
6. Move the spec to `apidocs/spec.json` with `go:embed`, and optionally move the
   login throttle to `internal/auth` (finding 6).

Each step is a reviewable, behaviour-preserving change and each should land with
its own test run. Steps 1, 3 and 5 touch no wire; steps 2 and 6 touch the wire
document and should be verified against `contract_test.go` and
`TestDocumentedSecurityMatchesTheHandlers`. I did not run the suite, so the
implementer must confirm `cd backend && go test ./...` (and the gated
`TestGatewayIntegration` with `QC_TEST_DATABASE_URL`) after each step.
