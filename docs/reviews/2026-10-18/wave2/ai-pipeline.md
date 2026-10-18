# ai-pipeline — wave 2 verification

Domain: `backend/internal/ai/pipeline.go`, `api.go`, `validate.go`, `review.go`,
`course.go`. Read-only. `rg` is not on PATH here, so every search below is the
grep equivalent of the one the report cites. The working tree is the uncommitted
2026-10-18 collapse.

## Verdict

Wave 1 survives almost intact. Thirteen items are confirmed as stated, and every
one of the five "no consumer" deletes holds under a fresh grep across the whole
repo (tests, `scripts/`, `web/`). The dead branches are dead, the identity hops
are identity hops, and the two written-once facts are each written twice. Seven
items need amendment, mostly because the wave-1 remedy names the symptom and
understates the blast radius: the `Challenges` delete touches the `Depot`
interface, a test double and five store test call sites, `ProvidedRunner` is
test-only machinery the greenfield law says to delete, and the stage-to-code
helper must not absorb `StageValidateArtifact` or the pipeline's own test
(`pipeline_test.go:311`) breaks. One sub-item drops: `DefectVerdict.Error` is
not dead, the web client reads it. The final list is 13 confirmed actions, 9
amended, 1 dropped, 1 new.

## Confirmed actions

- [ ] Delete `setVerified` — `backend/internal/ai/pipeline.go:194-198` — evidence: `grep -rn "setVerified" --include='*.go' .` returns only the comment at `:194` and the definition at `:195`. `p.verified` is written by `publish` at `course_agent.go:1130` from `build.verifiedCount()`, and read by the job view at `jobs/jobs.go:424`. — safe: no caller in the tree; the field stays and keeps its writer.

- [ ] Merge the four identity hops into their exported methods — `backend/internal/ai/api.go:337-354` and `backend/internal/ai/review.go:37,73,152,281` — evidence: `grep -rn "s\.tutor(\|s\.critique(\|s\.reportDefect(\|s\.applyPatch(" --include='*.go' .` returns exactly `api.go:338,343,348,353`; the tests call only the exported names (`review_test.go:51,101,153,226`). — safe: move each unexported body up, delete the twin. Four functions, two files.

- [ ] Delete `validateCoursePlan` — `backend/internal/ai/course.go:97-143` — evidence: `grep -rn "validateCoursePlan" .` returns the definition, the comment at `course_agent.go:48`, `course_test.go:243,268`, and `backend/README.md:433`. The live run calls `validateCourseOutline` (`course_agent.go:793`) and `validateCourseChapter` (`course_agent.go:1066`); `validateCourseOutline` (`course_agent.go:124-197`) re-checks every plan rule including title, subtitle, level, objectives, chapter count, setup and the exercise budget, and `validateCourseChapter` owns the exercise rules. — safe at runtime: the only rule with no live home is the `dialect` check, and no production path ever ran `validateCoursePlan` to apply it. Update `course_agent.go:47-48` and `backend/README.md:433` in the same change.

- [ ] Delete `ErrNotConfigured` and the nil branch in `ProvidedRunner.Scratch` — `backend/internal/ai/api.go:50-51,168-174` — evidence: `grep -rn "ErrNotConfigured" --include='*.go' .` returns only `api.go:50,51,171`. — safe: no caller. If `ProvidedRunner` is deleted per the amended item below, this is one change.

- [ ] Delete the `kind` check in `validateRequest` — `backend/internal/ai/validate.go:36-38`, test case at `validate_test.go:44` — evidence: `grep -rn "validateRequest(" --include='*.go' .` returns only `pipeline.go:379`, `course_agent.go:762` and the test. `GenerateChallenge`/`GenerateCourse` set the kind (`api.go:326,332`), the job registry routes through them (`jobs/jobs.go:314-316`), and the HTTP route sets `req.Kind = kind` (`httpapi/ai.go:186`). `s.generate` has only those two callers. — safe: no runtime path can reach the branch with an unknown kind.

- [ ] Delete the `stages == nil` branch in `pipeline.outcome` — `backend/internal/ai/pipeline.go:260-263`; keep the `attempts` guard at `:265` — evidence: `newPipeline` appends all nine stages at `pipeline.go:96-99`, and `grep -rn "newPipeline(\|&pipeline{" --include='*.go' .` shows every pipeline comes from `newPipeline` (`pipeline.go:374`, `review.go:391,400`, `course_agent.go:734,884`). `p.attempts` starts nil and only grows, so that guard is reachable. — safe.

- [ ] Delete the dead `start` at `pipeline.go:587` — `backend/internal/ai/pipeline.go:587` — evidence: `start := time.Now()` at `:587` is overwritten by `start = time.Now()` at `:589` with no read between. — safe.

- [ ] Rename exported `Fixture` to unexported `fixture` — `backend/internal/ai/api.go:255-259`, uses at `pipeline.go:763,764,773` — evidence: `grep -rn "\bFixture\b" --include='*.go' .` returns only those four sites. — safe: internal vocabulary, nothing outside the package can name it.

- [ ] Collapse the unavailable-code predicate into one method — `backend/internal/ai/api.go:406-410`, `backend/internal/ai/review.go:362-366` — evidence: both are the same four lines, `CodeUnavailable` then `s != nil && s.cfg.Provider != nil && s.cfg.Runner == nil` sets `CodeEnvironmentDown`. — safe: `func (s *Service) unavailableCode() string` used by `unavailable` and `requireAvailable`.

- [ ] Replace the two ordered-field maps with slices — `backend/internal/ai/validate.go:59-66` and `:163-171` — evidence: both iterate a `map[string]string`, so Go randomizes which field is named when both are invalid. Every other check in the file is an ordered slice. — safe: a `[]struct{field, value string}` evaluates each check once and makes the named field deterministic.

- [ ] Inline `agentTimeout` into `timeout` and drop the stale sentence — `backend/internal/ai/pipeline.go:487-502` — evidence: `grep -rn "agentTimeout" --include='*.go' .` returns only `:490` (definition) and `:501` (the single call in `timeout`). The course path never calls it; its timeouts come from `runCtx`. — safe: two-line body, one caller.

- [ ] Move the request-rendering block to `pipeline_prompt.go` — `backend/internal/ai/pipeline.go:850-1014` — evidence: the banner `/* ---------- request rendering ---------- */` is at `:850`, and `docs/target-architecture.md:50` names exactly this split. The block is `requestPrompt`, `datasetBrief`, `requestSummary`, `kindOrChallenge`, `systemPrompt`, `scratchName`, `evalContract`, `queryErrorText`, `trimTrailingSemicolon`, `quoteIdent`, `probeStrings`, `valueString`. — safe: same package, no behavior change.

- [ ] Delete `PatchResult.Error` — `backend/internal/ai/api.go:126` and the four assignments at `review.go:306,318,327` — evidence: no `patchResult` schema exists (`grep -rn "patchResult" contracts/ backend/internal/apidocs/` finds only `contracts/README.md:47`), every non-nil value is returned beside a non-nil `error` so `handlePatch` takes `writeAIError` and discards the struct (`httpapi/ai.go:754-757`), and the client reads only `body.applied` (`web/src/api/ai.ts:964`). — safe. Fix `contracts/README.md:47` in the same change: it points at a `patchResult` definition that does not exist.

## Amended

- [ ] Stage-to-code switch: the helper must cover only the two shared stages — `pipeline.go:511-517` and `course_agent.go:1286-1296` — what changed: wave 1 said one `codeForStage` with "each caller supplying its own fallback" preserves the `StageValidateArtifact` divergence. It only does if the helper maps `StageCrossCheck` and `StageReferenceContract` and nothing else. The pipeline reaches `exhausted` with `StageValidateArtifact` and expects `CodeRepairExhausted` (`pipeline_test.go:311`, `NewError(CodeRepairExhausted, schema.StageValidateArtifact, ...)`). If the helper also maps `ValidateArtifact`, that test breaks and the pipeline's code changes. Edit: `func codeForStage(stage string) (string, bool)` with the two cases; the pipeline caller keeps `CodeRepairExhausted` as fallback, the course caller keeps its own `case schema.StageValidateArtifact` before calling.

- [ ] Defaults re-merged: the call-site count is 12, not 11 — `api.go:279,321`, `pipeline.go:370`, `review.go:45,81,174,230,320,399`, `course_agent.go:730,869`, `course_agent_tools.go:90` — evidence: `grep -rn "Merge(DefaultLimits())" --include='*.go' .` lists the constructor at `api.go:268` plus those 12. — what changed: keep the constructor merge and `NewJobs` (`jobs/jobs.go:148`), read `s.cfg.Limits` (already merged) at each of the 12. The `httpapi/ai.go:215` and `admin_ai.go:210` merges operate on a cfg that may be `ai.Config{}`, so `Limits.Merge` stays exported.

- [ ] `Service.Challenges` cascade is wider than "two store test cases" — what changed: deleting it also requires `schema.Depot.Challenges` (`schema/model.go:239`), the store impl (`store/challenges.go:106-108`), the test double (`helpers_test.go:145-147`), and five store test call sites, not two (`store/challenges_test.go:97,186,207,235`, `store/sqlite_test.go:185`). Evidence: `grep -rn "Challenges(ctx context.Context) (\[\]schema.Record, error)" backend` returns only `store/challenges.go:106` and `ai/api.go:357`; `grep -rn "\.Challenges(" --include='*.go' .` shows the only production hit is `api.go:361`, inside the wrapper being deleted. `Depot.Courses` is not the same case: it has live readers at `course_agent.go:950` and `httpapi/ai.go:472`, so leave it.

- [ ] `ProvidedRunner`: delete it, per the greenfield law — `backend/internal/ai/api.go:163-174` — what changed: wave 1 left this as an owner decision. The code settles it. `grep -rn "Runner:" --include='*.go' . | grep -v _test` returns only `cmd/api/main.go:286` (the envdb runner), so the export exists solely for four test files (`helpers_test.go:351`, `course_agent_test.go:822`, `jobs/jobs_test.go:137`, `httpapi/jobs_test.go:160`). Under "test-only machinery without a real consumer is deleted", it goes; each affected package defines a three-line fake, and `course_agent_test.go` already shows the pattern with `failingRunner` at `:1188`. Bundle `ErrNotConfigured` into this change.

- [ ] `datasetVersion` fallbacks: delete both, do not unify — `pipeline.go:824-830` and `course_pipeline.go:122-125` — what changed: wave 1 offered one helper. The fallback is unreachable everywhere. `grep -rn "DatasetVersion" --include='*.go' backend` shows `cmd/api/main.go:289` sets it at boot, `admin_ai.go` rebuilds from `s.currentAI()` and preserves it, and every test construction sets `DatasetVersion: 1` (`helpers_test.go:352`, `course_agent_test.go:823,1189`, `jobs/jobs_test.go:138`, `httpapi/jobs_test.go:161`). Nothing constructs `Config{}` for a run. Delete both `< 1` branches and the decoupling comment at `pipeline.go:826-828`; also fix `review.go:381-384`, whose `stampProvenance` comment claims the version "may be zero here" when it never is.

- [ ] Chapter bounds: only 6, 40 and 4 are course constants — `course.go:47-51`, `course.go:330,336,341`, and `api.go:198` — what changed: `MaxHints: 3` is a `DefaultLimits` field, not a `course.go` constant, and the schema is a package-level const that cannot read runtime limits. Render the schema with `fmt.Sprintf` for `maxCourseSections`, `minSectionChars` and `maxCourseExercises`; leave the `maxItems: 3` hint alone or bind it to `DefaultLimits().MaxHints` at init. The comment at `:312-315` still explains why the bound is part of the hint.

- [ ] `DefectVerdict.Error` is not a dead twin of `PatchResult.Error` — `api.go:106`, `contracts/ai.schema.json:243`, `web/src/api/ai.ts` `asDefectVerdict` — what changed: the contract keeps the field and the web client reads `value.error`. So the two error channels are not one dead and one live. Keep the field; either populate it in `reportDefect` or drop it from the contract and the client together. Do not delete it as dead.

- [ ] `validateCoursePlan` test retarget is not a one-line swap — `course_test.go:243,268` — what changed: `validateCourseOutline` takes a `CourseOutline`, not a `CoursePlan`. Chapter-level cases (duplicate exercise id, two-statement reference, empty expected columns) can call `validateCourseChapter` directly; plan-level cases (setup dataset, unknown dialect) need an outline fixture. The `unknown dialect` case has no home after the delete because `CourseOutline` carries no dialect field.

- [ ] Package doc overclaims the nine-stage invariant — `pipeline.go:17-24` — what changed: wave 1 listed this as uncertain. The code settles it. `unavailable` returns `Stages: []schema.Stage{}` (`api.go:414-419`), `courseBuildRefusal` returns an empty slice (`course_agent.go:990-999`), and a successful `applyPatch` returns one (`review.go:332`). All are runs that never started, so the policy is defensible but the sentence is not literally true. Narrow it to "a run that starts lists all nine stages in order".

## Dropped

- Delete `DefectVerdict.Error` as an unused field: dropped. The web client reads it (`web/src/api/ai.ts` `asDefectVerdict` maps `error: asAiError(value.error)`) and `contracts/ai.schema.json:243` requires the property. Only the `PatchResult` half of that wave-1 collapse is dead.

## New

- [ ] `splitFieldDetail`'s no-colon fallback is dead — `backend/internal/ai/course.go:244-249` — evidence: `grep -rn "splitFieldDetail" --include='*.go' backend` returns only the caller `course.go:238` and the definition. The caller only ever receives an error from `validateArtifact`, and every error from `validateArtifact` is built by `artifactError` (`validate.go:193-196`) as `field + ": " + detail`, so `strings.Index(detail, ": ")` always finds a match and the `return detail, "failed"` branch never runs. Edit: drop the branch and return the two values directly, or leave it and stop calling it a field split. Low value, bounded to `course.go`.

## Still uncertain

None. The four wave-1 uncertain items settle by reading the code: `ProvidedRunner` has no production consumer, `DatasetVersion` is always set, the package doc is contradicted by three never-started paths, and the `patchResult` schema genuinely does not exist. No runtime proof is needed for any confirmed or amended action.
