# Simplification pass, 2026-10-16 (archived)

This report lived in `backend/README.md` until 2026-10-18. It describes the
simplification pass of 2026-10-16. Later passes superseded parts of it: the
store now writes its statements once, the provider configuration is a single
capability, and the API document lives in `apidocs/spec.json`. The record stays
here because the reasoning behind the proposals still reads well.

## Simplification pass, 2026-10-16

This pass removed code a reader had to hold in their head. The behaviour, the
JSON error codes, the HTTP surface and the honesty rule did not move. The
command `cd backend && go build ./... && go vet ./... && go test ./...` is green
for all 13 packages, and `go test` boots real PostgreSQL through `internal/pgtest`.

### Measured shape

| Package | Before | After | Delta |
| --- | --- | --- | --- |
| `internal/httpapi` | 13276 | 13228 | -48 |
| `internal/ai` | 11157 | 11102 | -55 |
| `internal/engine` | 6235 | 6230 | -5 |
| `internal/store` | 3510 | 3490 | -20 |
| `internal/eval` | 1380 | 1374 | -6 |
| `internal/perf` | 832 | 817 | -15 |
| `internal/metrics` | 532 | 540 | +8 |
| `internal/webui` | 228 | 225 | -3 |
| `internal/dataset`, `internal/envdb`, `internal/dialect`, `internal/localdb`, `internal/auth`, `internal/pgtest`, `cmd/api` | unchanged | unchanged | 0 |
| **Total** | **43947** | **43803** | **-144** |

The pass removed 8 functions, 0 interfaces and 0 files. `internal/metrics` grew
by 8 lines because a per-call allocation fix needed a doc comment; the behaviour
is the same and the allocation is gone.

### What changed

Standard library over hand-rolled helpers. `store/memory.go` copied
`json.RawMessage` by hand and now calls `bytes.Clone`; its three `sort.Slice`
comparators became `slices.SortFunc` with `cmp`. `eval` and `perf` each carried a
private `contains`; both are `slices.Contains` now, and `perf` gained
`slices.ContainsFunc` for `anyNode`. `engine/explain.go` dropped a warning loop
and a unique-append loop for `slices.Contains`. `metrics` built a
`strings.Replacer` per rendered line and now holds two package-level replacers,
and it sorts series with `slices.SortFunc`.

Duplicated knowledge, kept once. `store/sqlite.go` had `marshalJSON` and a
`jsonText` wrapper over it; `jsonText` holds the body now. `engine/engine.go`
repeated the string `"postgresql"` in `EngineName` and `DialectPostgres`, so
`EngineName` is an alias. `ai/jobs.go` and `ai/transcript.go` held the same
random-id builder under two names, now one `newID`. Three `Provenance` literals
in the pipeline, the course pipeline and review became one `provenance()`
constructor. `httpapi` wrote the owner-scoped course fetch four times, now one
`ownedCourse` helper that states the 404-never-403 rule once.

Layers that only forwarded. `httpapi.trimCourse` renamed `strings.TrimSpace`, so
the two call sites call `strings.TrimSpace`. `ai.complete` repeated `completeRaw`
line for line, so it delegates now. `ai.timeout` repeated `agentTimeout`, so it
delegates now.

Unreachable and misnamed code. `httpapi.writeSQLiteTablesError` had a `qerr ==
nil` branch no caller could reach. `store/memory.go` and `store/postgres.go`
carried a `Close` doc comment stacked on `Kind`; each comment sits on its
function now. The field `ExerciseResult.FixturesPassed` held a rejected count
under a `fixtures_rejected` JSON tag, so it is `FixturesRejected`.

Type-erased and stringly dispatch. `store/postgres.go` read SQLSTATE `23505`
through an anonymous `interface{ SQLState() string }`; it asserts
`*pgconn.PgError` like `engine` does. `ai/exerciseIndexFromField` parsed digits
by hand and now uses `strconv.Atoi`. `ai.clampInt` hand-rolled bounds and now
uses the `min` and `max` builtins. `httpapi.asyncRequested` compared one query
value twice.

Arguments and guards that lied. `envdb.buildEnvironment` took `accountID` and
`key` and read neither. `webui.Available` repeated the broken-embed guard that
`distFS` already holds.

### Proposals, not implemented

These would change behaviour or the wire, or they need test edits the brief
forbids, so they stay as proposals.

- `store` repeats the owner-scoped list and scan shape six times across
  `memory.go`, `postgres.go` and `sqlite_depot.go`, and states the metadata
  schema twice. A generic list helper and one schema template would cut real
  lines, but both rewrite a live SQL path for no behaviour change. Worth a
  separate, measured change.
- `eval.multisetEqual` and `eval.compareRowsDetailed` share a row-matching rule
  but return different verdict shapes. `ai.exhausted` and the course refusal
  switch differ on `StageValidateArtifact`, which a test caught when I tried to
  merge them: the challenge path reports `AI_REPAIR_EXHAUSTED`, the course path
  reports `AI_INVALID_ARTIFACT`. I reverted that merge.
- `engine.PgxExecutor.ClassifyError` only forwards to `pgCodeFor`, and
  `classifyFor` restates the sentinel ladder `classify` already holds. For a
  `PgxExecutor` the two paths are provably identical, but tests assert the hook
  exists and the ladder documents the dialect boundary. Removing it is a test
  edit plus a design call.
- `envdb.ResetPerf` stores its reset time under the challenge key
  `digest(accountID)`, so a perf reset overwrites the challenge environment's
  reported reset time. Keying it per kind fixes a latent bug, and that is a
  behaviour change.
- `dataset.perfTablesResponse` reports the commerce column list for
  `perf.orders`, while `dataset/perf.sql` adds five columns, and
  `dataset/perf_test.go` asserts the wrong claim. This is a bug, not a refactor.
- `dialect` exports `Names`, `ByName`, `Affinity`, `AffinityClasses`,
  `ErrorCodes` and a package-level `ClassifyError` that only tests call. Moving
  them into the test file is safe in this module but would ripple if an external
  consumer imports them.
- `ai` still holds dead knobs: `Config.AdversarialFixtures` has no setter,
  `GenerationRequest.Steps` is validated but never rendered, `CodeAIAvailable`
  has no reference, and `validateCoursePlan` plus `FakeProvider.courseBundle`
  run only in tests. Removing production symbols because only tests call them
  means deleting tests, which the brief forbids.
- `httpapi` states the routes in four places (the mux, `requestErrorCode`, the
  `operations` map and `openapi.go`). They already disagree on `GET /docs/`. A
  drift test is the cheap fix; generating the OpenAPI document is a larger one.
- `engine/explain.go` classifies a plan node by the substring `"index"`. An
  explicit node-type set would tighten that, and tightening is a behaviour
  change.

### Not run

I did not run `make test` or `make verify`, because both build and lint `web/`,
which another agent edits in this tree. I ran the Go suite instead, which holds
the Go half of the fixture suite (`internal/eval`), the explain fixtures, and
the HTTP contract tests. The JavaScript audit suites in `scripts/` did not run.

Two files in `backend/internal/ai` (`prompt_test.go`, `review_test.go`) carried
uncommitted test additions before this pass. I did not author or edit them.

The package layout changed after this pass; see
[`docs/backend-decomposition.md`](../docs/backend-decomposition.md).
