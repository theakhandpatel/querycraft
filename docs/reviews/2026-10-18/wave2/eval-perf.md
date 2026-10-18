# eval-perf — wave 2 verification

Baseline: `go build ./internal/eval/ ./internal/perf/` is green. I ran no test
suite and no `make` target, and I edited nothing. `rg` is not installed here, so
every search below is `grep -rn` with the exact pattern and scope written out.

## Verdict

Most of wave 1 survives. Ten of the sixteen Delete/Collapse/Move items are
confirmed as written, and every one of them is genuinely test-only or
unread. Five items point the right way but name a remedy with the wrong blast
radius: the authored-check delete, `PlanIndex.Definition`, the `Exercise`
decoration fields, `ExplainOptions.NodeCap`, and the `Contract`/`ContractAlt`
merge. Three items do not survive: `perf.Grade` must stay (it is the Go half of
the documented plan-fixture seam), the `PlanError` collapse is an owner call the
report already declines to make, and the optional `eval` split has no reader
problem. The headline omission is that the authored checks are dead on the web
side too, and `content-audit.mjs` still validates their codes.

## Confirmed actions

- [ ] Delete the three warning constants Go never emits: `WarnRowCapReached`,
  `WarnSettingsNonDefault`, `WarnAnalyzeSkipped` — `backend/internal/perf/plan.go:31-33`.
  Evidence: `grep -rn 'WarnRowCapReached\|WarnSettingsNonDefault\|WarnAnalyzeSkipped' --exclude-dir=node_modules .`
  returns only the declaration, `web/src/api/planContract.ts:106-108,209-211`,
  and `contracts/plan.schema.json:152-154`. No Go read exists, and Go has no
  bundle decoder, so the schema enum needs no Go constant. Safe now.
- [ ] Delete `ErrorResponse` — `backend/internal/eval/eval.go:160-163`.
  Evidence: `grep -rn 'ErrorResponse' backend web scripts` returns only the
  comment and the type (the other hits are `node_modules` and binaries). Dead.
- [ ] Delete the `Compare` wrapper and point its unit tests at
  `CompareDetailed` — `backend/internal/eval/compare.go:77-93`,
  `backend/internal/eval/eval_test.go:113,120,130,135,145,150,162,165,172,179,212,217,226,232,239`.
  Evidence: `grep -rn '\bCompare\(' --include='*.go' backend web scripts` finds
  only `eval_test.go` (and unrelated `strings.Compare`/`cmp.Compare`);
  production runs `CompareDetailed` at `eval.go:199` and `fixtures_test.go:129`.
  Safe now.
- [ ] Delete the `CompareColumns` wrapper — `backend/internal/eval/compare.go:66-75`,
  tests at `eval_test.go:250,253`. Evidence: `grep -rn '\bCompareColumns\b' backend web scripts`
  finds only the definition, the wrapper, its comment, and those two test lines;
  `Evaluate` calls `CompareColumnsDetailed` at `eval.go:201`. Safe now.
- [ ] Delete `Comparison.RowsCompared` in both languages — Go
  `backend/internal/eval/detail.go:16,63`; TS `web/src/api/compare.ts:202,210,223,231,234`.
  Evidence: `grep -rn 'RowsCompared' backend web` returns only those lines;
  `grep -rn 'rowsCompared' web/src` returns only the interface and the four
  literals. `checksFromComparison` (`compare.ts:285-305`) reads `columnsOk`,
  `rowsCountOk`, `valuesOk`, `rowOrderOk`, not this. Safe now.
- [ ] Drop `perf.Explain`'s error return and the caller's dead branch —
  `backend/internal/perf/explain.go:97,114-120,187`,
  `backend/internal/httpapi/plan.go:109-120`,
  `backend/internal/perf/explain_test.go:147,496`.
  Evidence: every return path yields `nil`, either through `failed(...)`
  (`explain.go:119`) or the success return (`explain.go:187`), so
  `plan.go:117-120` is unreachable. The comment at `explain.go:93-96` admits it.
  Change `Explain` to `(*PlanBundle)`, return the bundle directly, delete the
  `if err != nil` block, and unthread `err` in the two tests. Safe now.
- [ ] Collapse one reference solution to `ReferenceSolution` — delete
  `Exercise.Solution` (`eval.go:88`) and the fallback in `reference()`
  (`eval.go:130-135`); delete the `solution` alias from
  `contracts/exercise.schema.json:46-49` and from
  `backend/internal/apidocs/apidocs.go:1228` (`"solution"`).
  Evidence: `grep -rn -E '\bsolution:' scripts web/src` finds only browser `Block`
  fields (`ChallengeLabScreen.tsx:335`, `curriculum.ts`), which `http.ts:431`
  maps to `reference_solution` on the wire; `grep -rn 'reference_solution' scripts`
  shows every script sends that key. No client sends `solution`. Safe now.
- [ ] Collapse one expected result to `Result` — delete `ExpectedResult` and
  `ExpectedRows` fields (`eval.go:92-93`), the two extra branches in `expected()`
  (`eval.go:121-126`), and the request keys `expected_result`/`expected_rows` at
  `backend/internal/apidocs/apidocs.go:1232-1233`.
  Evidence: `grep -rn -E 'expected_rows|expected_result' backend web scripts contracts`
  returns only `eval.go` and `apidocs.go`; `exercise.schema.json` does not even
  list them. The in-process producer is `ai/pipeline.go:742` (`Result`), which
  stays. Safe now.
- [ ] Collapse the duplicate feedback-code vocabulary: delete
  `validFeedbackCodes` and `NormalizeCode` (`eval/compare.go:23-64`) once the
  checks path lands, and drop `NormalizeCode` from `eval.go:214`,
  `eval_test.go:430-436`.
  Evidence: `grep -rn 'NormalizeCode' backend web scripts` returns only
  `eval.go:214` and the test. The `Code*` constants and `comparisonChecks` stay.
  Safe once the checks path is deleted.
- [ ] Drop the variadic `args ...any` from `planFirstRow`/`planRows` —
  `backend/internal/perf/explain.go:540,551`, and the forward at `:541`.
  Evidence: `grep -rn -E 'planRows\(|planFirstRow\(' backend` shows callers at
  `explain.go:418,427,443` pass no args, and no test passes any. Safe now.
- [ ] Move the JSON and environment helpers out of the seam file — move
  `planString` through `planValueFloat` and `planFirstRow`/`planRows`
  (`backend/internal/perf/explain.go:458-614`) to a new
  `backend/internal/perf/planjson.go`. Behaviour-preserving.
  Evidence: `wc -l` puts `explain.go` at 614 lines; `TestGradeReadsNoTiming`
  (`grade_test.go:347`) scans all non-test package files, so a move changes
  nothing it sees. Safe now.

## Amended

- [ ] The authored-check grading path. Keep the delete, fix the blast radius.
  Unreachable in Go is confirmed: `grep -rn -E '\bCheck\b|\.Checks|Checks:' backend/internal --include='*.go'`
  shows `eval.Exercise.Checks` set only in `eval_test.go`; `ai/pipeline.go:739-743`
  builds `eval.Exercise` with `Prompt`, `Contract`, `Result` and no `Checks`;
  `scripts/course-audit.mjs:135`, `scripts/ai-audit.mjs:163` and
  `scripts/ai-live-audit.mjs:377` all post an `exercise` body with no `checks`.
  What wave 1 missed: the concept is already dead on the web side.
  `web/src/data/curriculum.ts` carries authored `checks` with `test` functions
  (about 100 entries), `store.tsx:1691,1727,1753` do the same, and `types.ts:37-43`
  says nothing runs them. Nothing calls `check.test`: the only `.checks` reads
  are `Evaluation.checks` (`SqlCell.tsx:158,161`, `ChallengeLabScreen.tsx:220`).
  `scripts/content-audit.mjs:265-289` validates every authored `checks[].code`
  against `evaluation.schema.json`, so it is a consumer of the dead data. Go's
  `schema.Check` (`schema/model.go:103`) and `Artifact.Checks` (`:138`, set at
  `ai/course.go:82`) are never validated (`validate.go` has no `Checks`) and
  never graded. Remedy: delete the Go path (`Check` `eval.go:64-73` and the
  `encoding/json` import, `Exercise.Checks` `eval.go:94`, the loop
  `eval.go:205-218`, all of `regex.go`, the map and `NormalizeCode` in
  `compare.go`), and make the `Evaluate` tail always call `comparisonChecks`.
  That deletes only the last vestige; the owner should also decide the web
  `checks` data, `ExerciseCheck`, `schema.Check`/`Artifact.Checks`,
  `content-audit.mjs` section 5, and the `evaluation.schema.json:19` enum. Do the
  Go delete now, keep the rest as one follow-up.
- [ ] `PlanIndex.Definition`. The value is unread, but wave 1's "delete both
  fields" understates the blast radius. Evidence: no code reads the property
  (`grep -rn 'definition' web/src` finds only the type at
  `planContract.ts:87` and the decoder at `:261`; `PlanView.tsx:275-279` renders
  `name` and `relation` only). But `web/src/api/pglite.ts:749-750,777` writes it,
  and all eleven `contracts/fixtures/perf-*.json` carry
  `bundle.environment.indexes[].definition`. `plan.schema.json:130` allows it and
  does not require it. Remedy: delete `PlanIndex.Definition` (`plan.go:98`), drop
  `indexdef` from `sqlPlanIndexes` (`explain.go:60`) and from the assignment
  (`explain.go:445-452`), delete the two TS lines above, and strip `definition`
  from the eleven fixtures plus `plan.schema.json:122-132` so the fixture family
  does not pin a field no emitter produces. Leaving the fixtures alone is safe
  (the Go test checks arrays only), but it keeps a dead field in the seam.
- [ ] `Exercise` decoration fields. The eight unread fields are real:
  `ID`, `Version`, `LessonID`, `Type`, `Prompt`, `Setup` (and `Setup.Dialect`),
  `Hints`, `Difficulty` (`eval.go:79-90`), plus the `Setup` type at `:46-56`.
  Evidence: `grep -rn -E '\.Setup\b|\.Prompt\b|Exercise\.ID' backend --include='*.go'
  | grep -v _test.go` returns only `schema.*`/`course.*` fields; the AI writer is
  `ai/pipeline.go:740`. Amendment: deleting `Setup` must also fix the claim at
  `contracts/exercise.schema.json:28-31` that a `setup.dialect` mismatch is
  refused, because the running check is the sibling `dialect` field at
  `httpapi/handlers.go:190`; and `apidocs.go:1207-1234` lists all eight keys, so
  the OpenAPI copy needs the same trim. `exercise.schema.json:7` still requires
  `id/version/lesson_id`; extra request keys are ignored by `encoding/json`, so
  keep the schema's request shape or trim it in the same change. Safe now.
- [ ] `ExplainOptions.NodeCap`. Production never sets it
  (`httpapi/plan.go:109-116` sets `Analyze`, `Limits`, `Dataset`, `ScaleFactor`),
  and the only setter is `explain_test.go:456`. Amendment: the field is the only
  way `TestExplainNodeCap` (`explain_test.go:446-463`) reaches the cap without a
  201-node plan, so deleting it means retargeting that test at
  `summarizePlan(root, 3)`, which is package-level and takes the cap directly.
  Delete `NodeCap` (`explain.go:45`), use `DefaultPlanNodeCap` at `explain.go:160-163`,
  and rewrite the test. Safe now.
- [ ] One contract, two JSON keys. Direction is right, "update the single
  producer" is wrong: there are two producers. The browser sends `contract`
  (`execution/http.ts:423`, fed by `ChallengeLabScreen.tsx:336` and
  `lib/draft.ts:130-134`), and the scripts send `result_contract`
  (`ai-audit.mjs:159`, `ai-live-audit.mjs:373`, `course-gen-audit.mjs:280`).
  `exercise.schema.json:34-41` documents both on purpose, so collapsing also
  edits that description and `apidocs.go:1225-1226`. Pick `result_contract`
  (the authored spelling) and change `http.ts:423`; then delete `ContractAlt`
  (`eval.go:86`), `effectiveContract` (`eval.go:97-115`), and the merge tests.
  This also removes the real `NumericTolerance` drop: `effectiveContract` copies
  columns, column order, row order and duplicates but not tolerance, so a
  `contract` without tolerance silently loses the `result_contract` tolerance.

## Dropped

- Delete `perf.Grade`: killed by the documented seam. `grade_test.go:288` is the
  Go consumer, `make test` runs it, and `make audit-perf-fixtures` plus
  `web/scripts/plan-conformance-audit.mjs:196-206` pin the same verdicts. The
  task forbids deleting a documented seam half. Keep, as wave 1's Earned list
  already concluded. `docs/architecture-review.md:157-161` is stale on this point.
- Collapse `perf.PlanError` into `engine.QueryError`: not an action. The report
  itself says "only if the owner wants"; the two schemas' code enums differ
  (`plan.schema.json:173-183` vs `query-result.schema.json`), so it is a real
  contract decision, not a free delete. Leave it.
- Split `eval.go` into `types.go`: dropped as the report says, "the file is not
  yet a reader problem". No evidence of harm found.

## New

- The web half of the authored-check concept, missed by wave 1 because the
  scope stopped at the Go seam. Authored `checks` live in
  `web/src/data/curriculum.ts` (about 100 objects with `test` functions),
  `web/src/state/store.tsx:1691,1727,1753`, and `web/src/types.ts:37-43,75`.
  Nothing executes `check.test`:
  `grep -rn -E 'block\.checks|item\.checks|\.test\(code' web/src scripts` finds
  no runner. Go's `schema.Check` and `Artifact.Checks` are carried but never
  validated (`validate.go` has no `Checks`) and never graded. So one feature is
  modeled in three places and run in none. Owner action: delete the web data and
  type, `schema.Check`/`Artifact.Checks`, and `content-audit.mjs:265-289`, or
  wire a real runner. This is the same decision as the amended Go delete.
- Stale path comment in `scripts/perf-fixture-normalize.mjs:6`: it names
  `backend/internal/engine/explain.go` as the Go walk, but the walk is
  `backend/internal/perf/explain.go:271` (`summarizePlan`).
  Evidence: `grep -rn 'func summarizePlan' backend` returns only
  `backend/internal/perf/explain.go:271`; no `explain.go` exists under
  `backend/internal/engine`. One-line doc fix.

## Still uncertain

- Whether the authored-check path must stay on the public wire. No in-repo
  producer exists, but a non-repo client could post `checks`; only the owner can
  retire that surface. A test that posts `checks` to `/answers/evaluate` would
  confirm the current clamp behaviour before deletion.
- Whether `contract` and `result_contract` are ever both present in one live
  request. The tolerance-drop bug only bites then, and no producer sends both.
  A request test would settle it and justify the collapse.
- Whether stripping `definition` from the eleven perf fixtures keeps
  `make audit-perf-fixtures` and `make audit-perf` green. The fixture family is
  the cross-language seam, so this needs the fixture workflow, not a grep.
