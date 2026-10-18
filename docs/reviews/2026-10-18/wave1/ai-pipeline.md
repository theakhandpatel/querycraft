# ai-pipeline — slop audit (wave 1)

Scope: `backend/internal/ai/pipeline.go`, `api.go`, `validate.go`, `review.go`,
`course.go`. Read-only; `go build ./internal/ai/` is green on the working tree,
which carries the uncommitted 2026-10-18 collapse. Context read for call sites:
`ai/jobs/jobs.go`, `ai/course_agent.go`, `ai/course_pipeline.go`,
`ai/course_agent_tools.go`, `ai/fake.go`, `httpapi/ai.go`, `cmd/api/main.go`,
`contracts/ai.schema.json`. The earlier review
`docs/reviews/2026-10-17/02_ai_pipeline.md` was read only to skip landed items:
`RawConfig`, `StreamFn`, `resolveCourseModel`, `CodeAIAvailable`,
`AdversarialFixtures`, `GenerationRequest.Steps`, the provider collapse and the
httpapi error-code switch are gone and are not re-reported here.

## Verdict

The five files are mostly earned: one envelope constructor (`NewError`), one
stage plan (`stageOrder`), one policy gate (`validateArtifact`) reused by the
course path, and one provider seam whose capability replaced the old
`RawConfig`/`StreamFn` split. What is left is the same disease in a thinner
layer. The clearest slop is structural, not local: `Service.Limits.Merge(DefaultLimits())`
is applied once in the constructor and re-applied at eleven call sites, the
four tutor/critique/defect/patch entry points are identity hops over unexported
twins, and the unavailable-code predicate and the stage-to-code switch are each
written twice. On top of that sit five symbols with no live consumer at all:
`setVerified`, `Service.Challenges`, `validateCoursePlan`, `ErrNotConfigured`
and the exported `Fixture` type. `validate.go`'s tokenizer, `pipeline.go`'s
stage reporting and `review.go`'s execution-before-verdict rule are load-bearing
and should not be touched; deleting them would delete the policy. One file
(`pipeline.go`, 1014 lines) still carries three responsibilities with the split
boundary already marked in it.

## Delete

- `setVerified` — `pipeline.go:194-198`. Why it exists: it was how the course
  agent pushed "how many chapters have verified" onto the run before `publish`
  existed. Evidence it has no consumer: `grep -rn "setVerified" backend
  --include='*.go'` returns only the comment and the function definition; the
  job view reads `Progress.Verified` (`jobs/jobs.go:424`), which `publish`
  fills from `build.verifiedCount()` (`course_agent.go:1130`). Remedy: delete
  the method; `p.verified` stays, written by `publish`.

- `Service.Challenges` — `api.go:356-362`. Why it exists: an all-owner list
  wrapper over `Depot.Challenges`. Evidence it has no consumer: `grep -rn
  "\.Challenges(" backend --include='*.go'` shows the only production list
  route calls `svc.ChallengesFor` (`httpapi/ai.go:359`); the `Depot.Challenges`
  hits are `store` internals and its tests. Remedy: delete the method. The
  `Depot.Challenges` method then has no production reader either
  (`store/challenges.go:106`), so delete it in the same change and drop its two
  store test cases.

- `validateCoursePlan` — `course.go:97-143`. Why it exists: the M7 whole-plan
  gate. Evidence it has no consumer: `grep -rn "validateCoursePlan"
  backend --include='*.go'` returns the definition, a comment in
  `course_agent.go:48`, and `course_test.go:243,268`. The live course path runs
  `validateCourseOutline` (`course_agent.go:793`) and `validateCourseChapter`
  (`course_agent.go:1066`); `validateCourseChapter` already owns every
  exercise and section rule and `validateCourseOutline` owns every plan rule,
  so nothing at runtime is lost. Remedy: delete the function and point the
  `course_test.go` table at `validateCourseChapter`/`validateCourseOutline`
  directly. Do not keep a second gate because a test calls it.

- `ErrNotConfigured` — `api.go:50-51`, returned at `api.go:171`. Why it exists:
  a sentinel for a `Runner` that has no executor. Evidence it has no consumer:
  `grep -rn "ErrNotConfigured" . --include='*.go'` returns only those three
  lines; the nil branch that would return it is unreachable because every
  construction of `ProvidedRunner` passes `Exec` (`helpers_test.go:351`,
  `course_agent_test.go:822`, `jobs/jobs_test.go:137`,
  `httpapi/jobs_test.go:160`). Remedy: delete the var and the nil branch.

- `validateRequest`'s kind check — `validate.go:36-38`. Why it exists: to refuse
  an unknown `kind`. Evidence it is unreachable at runtime: every caller sets
  the kind before validation, `GenerateChallenge`/`GenerateCourse`
  (`api.go:326,332`), the job registry (`jobs/jobs.go:231`) and the HTTP route
  (`httpapi/ai.go:188`); the only driver of the error is
  `validate_test.go:44`. Remedy: delete the branch, or keep it and say in the
  comment that callers, not the request body, decide the kind. Do not leave a
  gate whose only exerciser is a test.

- `pipeline.outcome`'s nil-stages branch — `pipeline.go:260-263`. Why it
  exists: to satisfy the "arrays, not null" contract rule. Evidence it is dead:
  `newPipeline` always appends all nine stages (`pipeline.go:96-99`) and every
  `pipeline` in the tree comes from `newPipeline` (`generateRecorded:374`,
  `verifyArtifact:391`, `prepareScratch:400`, `generateCourse:734`,
  `buildCourseRecorded:884`), so `p.stages` is never nil. Remedy: delete the
  `stages == nil` branch; keep the `attempts` guard, which is reachable.

- Dead `start` in `verifyInEnv` — `pipeline.go:587`. Why it exists: a leftover
  from an earlier reordering. Evidence: `start := time.Now()` at `:587` is
  overwritten by `start = time.Now()` at `:589` with no read between. Remedy:
  delete line 587.

- Exported `Fixture` type — `api.go:255-259`. Why it exists: fixtures are part
  of the pipeline's vocabulary. Evidence it is internal-only: `grep -rn
  "\bFixture\b" backend --include='*.go'` returns only `api.go` and
  `pipeline.go:763,764,773`. Remedy: rename to lowercase `fixture`; nothing
  outside the package can name it and nothing does.

## Collapse

- Defaults merged in the constructor and again at eleven call sites —
  `api.go:268` vs `api.go:279`, `:321`, `pipeline.go:370`, `review.go:45,81,174,230,320,399`,
  `course_agent.go:730,869`, `course_agent_tools.go:90`. One fact, the
  effective limits, is computed twice. Evidence the constructor is the only
  writer: `grep -rn "Service{" backend --include='*.go' | grep -v _test`
  returns only `api.go:269`, and `NewService` already sets `cfg.Limits =
  cfg.Limits.Merge(DefaultLimits())` at `:268`. Remedy: read `s.cfg.Limits` at
  every call site and delete the re-merges; `Merge` stays for the one
  constructor and for `NewJobs` (`jobs/jobs.go:148`, which receives raw
  `ai.Limits` from a different path, `httpapi/ai.go:38`).

- Unavailable-code predicate written twice — `api.go:406-410` and
  `review.go:362-366`. Both compute "ENVIRONMENT_UNAVAILABLE when a provider is
  set and the runner is missing, else AI_UNAVAILABLE" with the same four lines.
  Remedy: one `func (s *Service) unavailableCode() string` used by
  `unavailable` and `requireAvailable`.

- Stage-to-code switch written twice — `pipeline.go:511-517` and
  `course_agent.go:1288-1298`. `exhausted` maps
  `StageCrossCheck`/`StageReferenceContract`; `courseRefused` maps the same two
  plus `StageValidateArtifact`. `grep -rn "codeForStage" backend` returns
  nothing. This is item "Stage to error code: two switches" in
  `docs/target-architecture.md`. Remedy: one
  `codeForStage(stage string) (string, bool)` with each caller supplying its own
  fallback, which preserves the deliberate `StageValidateArtifact` divergence.

- Four identity hops — `api.go:337` (`Tutor` -> `review.go:37 tutor`),
  `api.go:342` (`Critique` -> `review.go:73 critique`), `api.go:347`
  (`ReportDefect` -> `review.go:152 reportDefect`), `api.go:352` (`ApplyPatch`
  -> `review.go:281 applyPatch`). Evidence: each unexported twin's only caller
  is the exported wrapper, `grep -rn "s.tutor(\|s.critique(\|s.reportDefect(\|s.applyPatch("
  backend --include='*.go' | grep -v _test` returns exactly `api.go:338,343,348,353`;
  the tests call the exported names. Remedy: move each body into the exported
  method and delete the unexported twin.

- Two error channels for one failure — `DefectVerdict.Error` (`api.go:106`) and
  `PatchResult.Error` (`api.go:126`). The Go error is the only live channel:
  `reportDefect` never assigns `verdict.Error` (`grep -rn "verdict.Error"
  backend` returns only `course_agent.go:1420`, a different type), and every
  non-nil `PatchResult.Error` is returned beside a non-nil `error`, so
  `handlePatch` takes `writeAIError` and the struct is discarded
  (`review.go:306,318,327` vs `httpapi/ai.go:756`). The client reads only
  `body.applied` (`web/src/api/ai.ts:966`). Remedy: delete `PatchResult.Error`;
  keep `DefectVerdict.Error` only if the contract keeps
  `contracts/ai.schema.json:243`, and if so, fill it instead of returning a
  bare error.

- The scratch-open sequence written three times — `pipeline.go:550-571`
  (challenge), `course_pipeline.go:28-52` (course), `review.go:178-186`
  (defect probe). Each checks the runner or a scratch failure, opens
  `Runner.Scratch`, records `evidence.ScratchDatabase`, and calls `prepare`.
  Remedy: one `openScratch(ctx, name)` on `pipeline` returning the executor and
  release; the challenge and defect paths call it, the course path keeps its
  plan-level setup.

- `datasetVersion` zero-fallback written twice — `pipeline.go:824-830` and
  `course_pipeline.go:122-125`. Both say "use `cfg.DatasetVersion`, else the
  artifact's own claim". Remedy: one `func (p *pipeline) datasetVersion(claim int) int`.

- Chapter bounds written twice as literals — `course.go:47-51` defines
  `maxCourseSections = 6`, `maxCourseExercises = 4`, `minSectionChars = 40`
  (and `api.go:198` `MaxHints: 3`); `courseChapterSchema` hardcodes the same
  numbers at `course.go:330` (`"maxItems": 6`), `:336` (`"minLength": 40`),
  `:341` (`"maxItems": 4`), `:352` (`"maxItems": 3`). The comment at `:312-315`
  says the bounds are deliberately part of the hint, which makes the drift
  worse, not better: raising a constant silently leaves the hint wrong. Remedy:
  render the schema with `fmt.Sprintf` from the constants, or drop the bounded
  values from the hint.

- Field checks written as maps where order is the point —
  `validate.go:59-66` (title/scenario) and `validate.go:163-171`
  (`column_order`/`row_order`/`duplicates`). Go map iteration order is random,
  so which field an invalid artifact names varies run to run, and the repair
  prompt built from that detail varies with it. Every other check in the file
  is an ordered slice. Remedy: replace both maps with a `[]struct{field, value string}`
  slice, which also evaluates `utf8.RuneCountInString(value)` once.

- `agentTimeout` is a one-caller hop with a false doc — `pipeline.go:487-502`.
  The comment says it exists "so the course agent can hand it back up", but
  `grep -rn "agentTimeout" backend --include='*.go'` returns only `:490` and
  `:501`; the course path never calls it (its timeouts come from `runCtx`). The
  only caller is `timeout`, which wraps the result in `failedOutcome`. Remedy:
  inline the two-line body into `timeout` and delete the stale sentence.

## Move / split

- `pipeline.go` (1014 lines) carries three concerns: stage machinery (`mark`,
  `begin`, `timed`, `notify`, `finalize`, `outcome`), the challenge run
  (`generateRecorded`, `verify`, `verifyInEnv`, `prepare`, `activate`), and
  prompt/rendering plus SQL text helpers (`requestPrompt`, `datasetBrief`,
  `requestSummary`, `kindOrChallenge`, `systemPrompt`, `scratchName`,
  `evalContract`, `queryErrorText`, `trimTrailingSemicolon`, `quoteIdent`,
  `probeStrings`, `valueString`). The boundary is already marked by the
  `/* ---------- request rendering ---------- */` banner at `pipeline.go:850`,
  which is exactly the split `docs/target-architecture.md` names. Remedy: move
  lines 850-1014 to `pipeline_prompt.go`; no behaviour change.

## Earned (checked, keep)

- `NewError` — the single envelope constructor (`api.go:46`), used across all
  five files and matched by `*schema.Error` in `jobs/jobs.go:213`.
- `stageOrder` + `QueuedStages` — one stage list, consumed by `jobs/jobs.go:395`.
- `WithProgress`/`progressFrom`/`progressKey` — live reporter seam, `jobs/jobs.go:307`.
- `Progress` fields `Planned/Verified/CurrentChapter/CourseID/Chapters` — all read
  in `jobs/jobs.go:418-428`.
- `notify`, `mark`, `begin`, `timed`, `finalize`, `outcome` — drive the live view
  and the final array shapes the contract requires (`ai.schema.json:155-161`).
- `snapshotStages` — used by the interrupted-build store path,
  `course_agent.go:924,1464`.
- `lastExpected` — the course path copies the measured rows into the exercise,
  `course_pipeline.go:94-98`.
- `verifyInEnv` split — one implementation for both paths,
  `pipeline.go:578` and `course_pipeline.go:76`.
- `defaultFixtures`/`constantFixture` including the `len(expected.Rows) > 1`
  guard — `pipeline.go:771`, no false rejection on a one-row reference.
- `probeStrings`/`quoteIdent`/`valueString` — the catalog probes go through bound
  parameters, `pipeline.go:662-663,674-675`, so no injection.
- `forbiddenWords` and `setupForbiddenWords` are two policies, not a duplicate:
  setup must allow `CREATE`/`INSERT`, `validate.go:439` vs `:382`.
- `referencesCatalog` — enforced on both solutions and setup, `validate.go:414,482`.
- `validateArtifact` reused by the course path, `course.go:203`.
- `validExerciseType`/`validContractEnum` — match the contract enums,
  `ai.schema.json:106,36-38`.
- `sameContract` field compare — `review.go:452`, justified against float
  tolerance at `:449-451`, used at `:250`.
- `applyChanges` unknown-field error — `review.go:501`.
- `guardSolutions`/`containsSolution` — enforced on every hint and critique,
  `review.go:60,91,117`.
- `verifyArtifact` — every applied patch re-runs the SQL stages,
  `review.go:323,390`.
- `Runner` seam — one real implementation, `envdb/scratch.go:28`.
- `Availability.TimeoutMS` — read by the gateway (`jobs/jobs.go:232`) and the
  client deadline (`web/src/components/screens/BuildingScreen.tsx:459`).
- `Limits.MaxConcurrent` — read through `NewJobs` at `jobs/jobs.go:154`,
  wired at `httpapi/ai.go:38`; not speculative.
- `Config.DatasetVersion` — set at `cmd/api/main.go:289`, stamped into
  provenance at `pipeline.go:831` and `review.go:384`.
- `courseChapterSchema`/`finishCourseSchema` — the tool argument schemas,
  `course_agent_tools.go:320,336`.
- `coursePromptMarker` — one symbol shared with the built-in provider,
  `fake.go:84`.
- `exerciseArtifact`, `exerciseCount`, `validateCourseChapter`,
  `validateCourseSetup`, `coursePlanError` — live on the course path,
  `course_pipeline.go:70`, `course_agent.go:384,1066,140`.
- `Reset`, `ChallengesFor`, `Challenge`, `UnavailableOutcome`, `Limits` — live,
  `httpapi/ai.go:404,359,378`, `jobs/jobs.go:213,232`.
- `decodeJSON` — one decode path for bundles, outlines and chapters,
  `pipeline.go:532`, `course_agent.go:785,1036`.

## UNCERTAIN

- `ProvidedRunner` (`api.go:163-174`) is documented as a test seam and has four
  test consumers (`helpers_test.go:351`, `course_agent_test.go:822`,
  `jobs/jobs_test.go:137`, `httpapi/jobs_test.go:160`) but no production one.
  Under the greenfield rule it is test-only machinery; deleting it means each
  external test package defines a three-line fake, which `course_agent_test.go`
  already does with `failingRunner`. Owner decision: delete, or accept it as
  the one shared test double.
- `Config.DatasetVersion`'s zero-fallback (`pipeline.go:824-830`) is unreachable
  while `cmd/api/main.go:289` always sets it; the swap path preserves it
  (`httpapi/admin_ai.go:198-202`). It only exists so the package need not import
  `dataset`. Needs an owner decision: keep the decoupling, or delete the branch
  and the duplicated copy in `course_pipeline.go:122-125`.
- The package doc at `pipeline.go:17-24` says "the outcome always lists all nine
  stages in order". The AI-off path returns `Stages: []schema.Stage{}`
  (`api.go:418`), and `courseBuildRefusal` returns an empty slice
  (`course_agent.go:994`). Both are runs that never started, so the claim is
  defensible, but it is not literally true and no test pins it.
- The `PatchResult` response has no schema in `contracts/ai.schema.json`;
  `contracts/README.md:47` still points at a `patchResult` definition that does
  not exist. Needs an owner decision before `PatchResult.Error` is deleted.
