# eval-perf — slop audit (wave 1)

Scope: every production file under `backend/internal/eval/**` and
`backend/internal/perf/**`. Test files are evidence only, not targets. Baseline
`go build ./internal/eval/ ./internal/perf/` is green. I did not run `make` or
any test suite.

## Verdict

The grading core is earned: `CompareDetailed`/`CompareColumnsDetailed`, the
value/row/column rules under them, and `Evaluate` are the live path
(`httpapi/handlers.go:204`, `ai/pipeline.go:598,619`, `contracts/fixtures`
through `eval/fixtures_test.go:129`). The real slop is wire surface that no
client reaches and a second implementation kept only as a test mirror. The
authored-check path (`Check`, the whole of `regex.go`, the checks branch of
`Evaluate`, `NormalizeCode`) has no production producer: the browser grades by
comparison (`pglite.ts:354`) and the HTTP client sends no `checks`
(`http.ts:419-432`). `eval.Exercise` decodes seven fields nothing reads.
`perf` writes `PlanIndex.Definition` and three warning constants that nothing
reads or emits. `perf.Explain` declares an error it never returns, and its only
caller keeps an unreachable branch. The plan fields themselves are not dead:
`PlanView.tsx` reads every `PlanNode` field and the schema requires the summary
arrays. The two packages do not share a shape that should live once — their
comparison outcomes genuinely differ; the duplication that exists is the
deliberate Go/TypeScript contract pair pinned by fixtures.

## Delete

- `Compare` — `eval/compare.go:90`. Pass-through wrapper returning
  `CompareDetailed(...)`. Callers: only `eval_test.go` (lines 113, 120, 130,
  135, 145, 150, 162, 165, 172, 179, 212, 217, 226, 232, 239); `rg '\bCompare\('`
  outside eval tests finds no match. Production and the fixture suite both call
  `CompareDetailed` (`eval.go:199`, `fixtures_test.go:129`). Remedy: delete;
  point the unit tests at `CompareDetailed`.
- `CompareColumns` — `eval/compare.go:72`. Same shape: only `eval_test.go:250,253`.
  `Evaluate` uses `CompareColumnsDetailed` directly (`eval.go:201`). Named by
  `docs/architecture-review.md:115` ("unexport"); delete, the tests can use the
  `*Detailed` form.
- `ErrorResponse` — `eval/eval.go:161`. Zero references anywhere
  (`rg 'ErrorResponse'` → only the comment and the type). Named dead by
  `docs/architecture-review.md:116`. Remedy: delete.
- `Comparison.RowsCompared` — `eval/detail.go:16`, written at `detail.go:63`,
  read by nothing (`rg 'RowsCompared'` → the two lines only). The same fact is
  dead on the client (`web/src/api/compare.ts:202` is written at 210/223/231/234
  and never read). Remedy: delete the field and its assignments in both.
- `WarnRowCapReached`, `WarnSettingsNonDefault`, `WarnAnalyzeSkipped` —
  `perf/plan.go:31-33`. No Go reference (`rg` over the repo finds them only in
  the declaration and the `plan.schema.json` enum). Go has no bundle decoder, so
  an unused constant is not needed to accept a code; the browser's
  `Schema.Literals` (`planContract.ts:205-212`) already carries the vocabulary.
  Named dead by `docs/architecture-review.md:119`. Remedy: delete the three
  constants (`WarnNodeCapReached`, `WarnSeqScanLargeTable`, `WarnEstimateOff`
  stay; each has an emit site).
- The authored-check grading path: `Check` (`eval/eval.go:68-73`),
  `Check.patternString` (`eval/regex.go:13`), `matchPattern`
  (`eval/regex.go:34`), `compilePattern` (`eval/regex.go:42`),
  `extractRegexSource` (`eval/regex.go:54`), `regexFlagPrefix`
  (`eval/regex.go:9`), the `Checks` branch (`eval/eval.go:205-218`), and
  `NormalizeCode` with `validFeedbackCodes` (`eval/compare.go:23-64`). Evidence
  of no live consumer: the HTTP client body carries only
  `id/version/lesson_id/contract/reference_solution` (`web/src/api/execution/http.ts:419-432`);
  the in-browser adapter never runs authored checks
  (`pglite.ts:354` uses `checksFromComparison`) and `types.ts:37-43` states
  "nothing runs this field at run time"; the AI pipeline sets no `Checks`
  (`ai/pipeline.go:739-743`). `rg 'NormalizeCode|patternString|matchPattern'`
  finds only `eval.go:208,209,214` and `eval_test.go`. Everything that reaches it
  is a test (`eval_test.go:265-267,290,381`). Remedy: delete the path and always
  emit `comparisonChecks`; keep the `Code*` constants and `CodeValueMismatch`.
- `perf.Explain`'s `error` return — `perf/explain.go:97`. Every return path
  returns `nil` (119, 144, 152, 158, 187), so the branch in its only caller is
  unreachable (`httpapi/plan.go:117-120`). The comment at `explain.go:93-96`
  admits it ("always nil today ... a future planner-only engine may fail").
  Remedy: return `*PlanBundle` only; delete the caller branch.
- `PlanIndex.Definition` — `perf/plan.go:98`, populated at
  `explain.go:445-452` from `pg_indexes.indexdef` (`explain.go:60`). No
  consumer reads `.definition` anywhere (`rg '\.definition'` → no matches;
  `PlanView.tsx:275-279` renders only `name` and `relation`; `perf-audit.mjs`
  ignores it). Remedy: delete the field and drop `indexdef` from
  `sqlPlanIndexes`; the frontend writer at `pglite.ts:777` can drop it too.
- `Exercise` decoration fields — `eval/eval.go:47-56,79-90`: `ID`, `Version`,
  `LessonID`, `Type`, `Prompt`, `Setup` (and `Setup.Dialect`), `Hints`,
  `Difficulty`. `Evaluate` reads only `Contract`, `ContractAlt`, `Result`,
  `ExpectedResult`, `ExpectedRows`, `ReferenceSolution`, `Solution`, `Checks`
  (`eval.go:180-216`); a grep for `e.Setup`/`Exercise.ID`/`.Prompt` finds no read.
  `Prompt` is written by `ai/pipeline.go:740` and never read.
  `progress.md:266-267` already recorded "Go decodes `Setup`/`Type`/`Prompt` and
  never reads them". Remedy: keep only the grading fields; the schema's
  `type`/`prompt`/`setup`/`hints`/`difficulty` become unread request keys, which
  `encoding/json` ignores.
- `ExplainOptions.NodeCap` — `perf/explain.go:45`. Production never sets it:
  `httpapi/plan.go:109-116` builds the options with `Analyze`, `Limits`,
  `Dataset`, `ScaleFactor` only. The sole setter is `explain_test.go:456`.
  Remedy: delete the field and use `DefaultPlanNodeCap` in `Explain`, or make it
  a real config knob; today it is test-only.
- `planFirstRow`/`planRows` variadic `args ...any` — `perf/explain.go:540,551`.
  No caller passes args (`explain.go:418,427,443`; `planFirstRow` forwards
  nothing at `explain.go:541`). Remedy: drop the variadic and the forwarding.

## Collapse

- One contract, two JSON keys — `Contract`/`ContractAlt`
  (`eval/eval.go:85-86`) merged in `effectiveContract` (`eval/eval.go:97-115`).
  Both spellings have live producers: the client sends `contract`
  (`http.ts:423`, `draft.ts:129`), authored/AI records carry `result_contract`
  (`ai.schema.json:96`). The merge also never copies `NumericTolerance` from the
  `result_contract` side, so the two keys can disagree about one fact. Remedy:
  pick one key, update the single producer, delete `effectiveContract`.
- One expected result, three keys — `Result`/`ExpectedResult`/`ExpectedRows`
  (`eval/eval.go:91-93`) read by `expected()` (`eval/eval.go:117-128`). Only
  `Result` has a producer (`ai/pipeline.go:742`, tests); `rg 'expected_rows|expected_result'`
  finds no producer in Go or `web/src`, only `eval.go` and `apidocs.go`. Remedy:
  keep `Result`, delete the other two and their branch in `expected()`.
- One reference solution, two keys — `ReferenceSolution`/`Solution`
  (`eval/eval.go:87-88`) resolved in `reference()` (`eval/eval.go:130-135`). The
  client sends `reference_solution` (`http.ts:431`); `solution` is a
  browser-model field (`types.ts:69`) that never reaches the wire. Remedy: keep
  `ReferenceSolution`, delete `Solution`.
- The five feedback codes are written twice in one file — `CodeValueMismatch`
  …`CodeRowOrder` (`eval/compare.go:16-20`) and the same five strings again as
  `validFeedbackCodes` keys (`eval/compare.go:29-33`). Remedy: build the map from
  the constants, or drop the constants and use the map keys; and if the
  checks-path delete lands, both shrink to the four codes `comparisonChecks`
  emits.
- One error envelope, two Go types — `perf.PlanError` (`perf/plan.go:124-128`)
  restates `engine.QueryError`'s `{code,message,detail}`. `perf` already imports
  `engine`. The schemas' code enums differ (`plan.schema.json:173-183` vs
  `query-result.schema.json`), so this is a real decision, not a free collapse.
  Remedy: only if the owner wants one envelope type; otherwise leave and note
  why.

## Move / split

- `perf/explain.go` (614 lines) mixes the plan seam with 150 lines of pure
  helpers. Move `planString`…`planValueFloat` and `planFirstRow`/`planRows`
  (`explain.go:458-614`) to `perf/planjson.go`. Behaviour-preserving.
- `eval/eval.go` (271 lines) mixes the request/response wire types
  (`eval.go:20-163`) with the `Evaluate` orchestration (`eval.go:165-270`). A
  `types.go` split is optional; the file is not yet a reader problem.

## Earned (checked, keep)

- `CompareDetailed`, `CompareColumnsDetailed`, `Comparison` — production
  (`eval.go:199,201`) and fixture (`fixtures_test.go:129`) consumers.
- `matchColumns`, `alignColumns`, `projectRows`, `rowsEqual`, `valueEqual`,
  `isNumericValue`, `toFloat`, `toBool`, `toStr`, `uniqueRows`, `containsRow`,
  `multisetEqual`, `dedupe` — each called from the `*Detailed` path
  (`detail.go:31,50,51,60,61,96,105,108,112,113,118,119,132,139`).
- `Evaluate` — live at `httpapi/handlers.go:204`; `EvaluatorVersion` — live at
  `ai/pipeline.go:798`.
- `comparisonChecks` — the production path runs it whenever no checks are sent
  (`eval.go:216-217`), which is every live request.
- `perf.Grade` family (`Verdict`, `Failure`, `nodeMatches`, label helpers) —
  the fixture suite is its live consumer (`grade_test.go:288`), and the fixtures
  are language-neutral with `plan-conformance-audit.mjs`; the labels pin the
  Go/TS pair. See UNCERTAIN for the production-dead half.
- `PlanNode` fields — every one is read by `PlanView.tsx` (`depth` 167, `alias`
  178, `join_type` 182, `parent_relationship` 183, `estimated_cost` 163,
  `actual_rows` 199, `actual_time_ms` 202, `loops` 205, `buffers_read` 206,
  `filter` 212, `index_condition` 211, `sort_key` 213, `warnings` 48).
- `PlanSummary.node_types`/`relations`/`max_estimated_rows` — required by
  `plan.schema.json:37-48` and read by `scripts/perf-audit.mjs:67,68,70,164,165`.
- `FailedBundle`, `EmptySummary`, `EmptyEnvironment` — the live refusal path
  (`httpapi/plan.go:190`); `summarizePlan`, `deriveWarnings`, `readEnvironment` —
  the live capture path.
- `StatusSucceeded`/`StatusFailed`, `TimingAnalyze`/`Estimate`/`None`,
  `EnginePostgres`, `DefaultPlanNodeCap`, `SeqScanLargeRows`,
  `EstimateOffRatio`, `WarnSeqScanLargeTable`, `WarnEstimateOff`,
  `WarnNodeCapReached` — all have emit/read sites (`explain.go:132-186,323,376-396`,
  `httpapi/plan.go:220-222`).
- `PlanBundle.Statement` is read by `perf-audit.mjs:168`; `RequestID`,
  `DurationMS`, `Error.Detail` are read by `writePlan`/`PlanView.tsx:95-97,129`.

## UNCERTAIN

- `perf.Grade` keep or delete. The gateway does not grade (`httpapi/plan.go:28-32`);
  the browser does (`planContract.ts:620`). Keeping it means two implementations
  of one rule plus a `Failure.Label`/`Detail` vocabulary no learner sees
  (`grade_test.go` asserts codes only). Deleting it loses the Go half of the
  cross-language fixture guard. Owner decides whether that guard is worth the
  second implementation. (`docs/architecture-review.md:157-161` calls it "dead in
  production".)
- Deleting the authored-check path removes documented API surface:
  `evaluation.schema.json:18-19` and `apidocs.go:1271` describe the check codes.
  No in-repo producer exists, but a non-repo client could send `checks`. Owner
  decision on the public wire.
- `exercise.schema.json:28-31` promises that `setup.dialect` mismatch is refused
  with `DIALECT_MISMATCH`, but the evaluator never reads `setup.dialect`; the
  check actually runs on the sibling `dialect` field (`httpapi/handlers.go:190`).
  Either wire the field or delete it and fix the schema claim. Crosses into
  `schema` and `httpapi`.
- `ContractAlt.NumericTolerance` is not merged in `effectiveContract`
  (`eval/eval.go:97-115`). If a client sends `contract` without a tolerance
  while `result_contract` carries one, the tolerance silently drops to 0.
  Behavioural; needs a test to confirm intent.
- `PlanError` vs `engine.QueryError` — see Collapse; the differing schema enums
  make this an owner call.
