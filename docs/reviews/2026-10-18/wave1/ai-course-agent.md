# ai-course-agent — slop audit (wave 1)

Scope: `backend/internal/ai/course_agent.go` (1,520 lines),
`backend/internal/ai/course_agent_tools.go` (516), `backend/internal/ai/fake.go`
(738). Read-only. `go build ./internal/ai/` is green.

The 2026-10-18 collapse is present in the working tree and is not re-reported:
the course agent reads `provider.CourseCapability` (`course_agent_tools.go:50`),
`storedChapterTurns` renders pi messages directly (`course_agent.go:691`), and
`goalOutlineMarker`/`goalOutlineEnd` are shared by prompt and fake
(`course_agent.go:667`, `fake.go:649`). The earlier wide review
`docs/reviews/2026-10-17/02_ai_pipeline.md` was read to skip landed items.

## Verdict

The course agent's skeleton is earned: the pi loop, the tool closure, the
`courseBuild` state model and the resume path are all load-bearing and every
method has a live caller. The real slop is concentrated in three places. First,
two functions have no caller at all and one advertised tool cannot work:
`handleToolCall` and `toolResultFromContent` are dead, and `reset_environment`
runs a `DROP SCHEMA ... CASCADE` that the restricted learner connection is not
allowed to execute, so the "reset" is a fabricated capability. Second, the file
is still six jobs in one, and the target architecture already names the split
(`docs/target-architecture.md:46-49`). Third, several one-fact copies remain:
the `OutlineChapter` to `CourseChapter` mapping is written three times, a
`provider.ToolCall` wrapper carries two fields nothing reads, and the pipeline
mirrors `courseBuild` by hand. The fake provider's generator is a genuine second
implementation and its prompt round-trip is a deliberate, now-linked contract,
so it stays. Nothing here is a behaviour bug except the reset tool.

## Delete

- `handleToolCall` — `course_agent.go:1009-1025`. Why it exists: the pre-pi
  dispatcher that switched on a `provider.ToolCall` name. Evidence it has no
  live consumer: `grep -rn --include='*.go' -w handleToolCall .` returns only
  its own comment and definition; the pi agent's tool closures call
  `p.saveChapterTool` and `p.finishCourseTool` directly
  (`course_agent_tools.go:327`, `:341`). Remedy: delete the function. It is also
  the only reason `saveChapterTool` needs a name (see Collapse 1).

- `toolResultFromContent` — `course_agent_tools.go:465-474`. Why it exists: a
  text extractor written beside `textFromContent`. Evidence: the same grep
  returns only its comment and definition; every live reader calls
  `textFromContent` (`course_agent_tools.go:124,126,128,205`, `fake.go:573`).
  Remedy: delete it.

- `reset_environment` tool and `courseResetSQL` — `course_agent_tools.go:381-396`,
  `:455-463`. Why it exists: a tool that tells the model it can restore a broken
  scratch. Evidence it has no live consumer and cannot work: no test and no fake
  turn calls it (`grep -rn 'reset_environment\|run_sql' --include='*.go'` finds
  only the advertisement), and the SQL is `DROP SCHEMA IF EXISTS dataset CASCADE`
  with no re-apply, while `openScratch` applies setup exactly once at the start
  (`course_pipeline.go:45`) and the scratch executor is the restricted learner
  connection (`envdb/scratch.go:20-27`). The learner only holds
  `USAGE/SELECT` on `dataset` (`dataset/roles.go:68-70`), so the drop is refused
  or, if it ran, would leave no dataset. The comment inside the SQL claims a
  re-seed that no statement performs. Remedy: either implement a real reset
  through `Runner` (recreate the scratch or re-apply `plan.Setup`) or delete the
  tool, `courseResetSQL`, and the `reset_environment` entry in
  `progress.md:2543`. Do not ship a tool that reports a restore it did not do.

- `FakeProvider.courseBundle` — `fake.go:503-511`. Why it exists: the whole-plan
  M7 provider reply. Evidence: its only caller is `course_test.go:260`
  (`grep -rn -w courseBundle .`); its own comment admits "the agent never asks
  for it"; the gate it feeds, `validateCoursePlan`, likewise has no non-test
  caller (`course.go:100`). This is test-only machinery and the greenfield law
  retires M7's whole-plan path. Remedy: delete `courseBundle` and the whole-plan
  test that reads it. The 2026-10-17 review kept it as a regression suite; that
  ruling is exactly what this wave overrides.

- `FakeProvider.Delay` — `fake.go:29-33` (used `:56-64`). Why it exists: to make
  a run observable while in flight. Evidence: the only setter in the tree is
  `httpapi/jobs_test.go:133` (`grep -rn 'Delay' --include='*.go' .`); no
  production caller sets it. Remedy: delete the field and the `time.After`
  branch, and let that test hold the run with a gating wrapper, which is the
  pattern already in `ai/jobs/jobs_test.go:67-113` (`gatedProvider`). "A test
  uses it" is not a reason to keep a knob on the shipped provider.

## Collapse

- `providerToolCall` and `saveChapterTool`'s `provider.ToolCall` parameter —
  `course_agent_tools.go:509-516`, call site `:326-327`; `course_agent.go:1030`.
  Two truths for one value: `providerToolCall` builds a `ToolCall` whose `ID` and
  `Function.Name` are never read, and `saveChapterTool` reads only
  `call.Function.Arguments` (`course_agent.go:1031`). The net path is
  `json.Marshal(params)` -> wrap -> `TrimSpace(call.Function.Arguments)`.
  Remedy: change the signature to `saveChapterTool(ctx, build, ex, argsJSON string)`,
  pass `string(args)` at the pi tool, and delete `providerToolCall` plus the
  `provider.ToolCall` import use. Deleting `handleToolCall` removes the switch
  that needed the name.

- The `OutlineChapter` -> `CourseChapter` field map, three copies —
  `course_agent.go:301-317` (`newCourseBuild`), `:1316-1332`
  (`withChapterSkeleton`), `:1486-1506` (`buildingChapters`). Each lists id, n,
  title, summary, kind, minutes, objectives and each builds the empty slices
  separately. `docs/target-architecture.md:37` already names the remedy: one
  constructor `func (o OutlineChapter) chapter(n int, prev schema.CourseChapter)
  schema.CourseChapter`, called by all three, so the invariant "the outline is
  what the learner was shown" is stated once.

- The pipeline mirrors `courseBuild` by hand — `course_agent.go:1128-1132`
  (`publish`) and `:1453-1480` (`storeBuilding`) copy `build.report` and
  `build.verifiedCount()` onto `pipeline.chapters` / `pipeline.verified`
  (`pipeline.go:81-91`), which `Progress` republishes (`pipeline.go:235-244`).
  The authoritative state is `courseBuild` (`course_agent.go:257-283`). The tell
  is that `pipeline.setVerified` (`pipeline.go:195`) has no caller at all while
  `publish` assigns `p.verified` directly. Remedy: derive the job view from the
  build at snapshot time (or let `Progress` take the build) and delete the mirrored
  fields and the dead setter. This touches the job view contract, so do it as one
  change (owner decision, see UNCERTAIN).

- `requestFromRecord`'s nil-`Request` fallback — `course_agent.go:374-386`, with
  the nullable column at `store/courses.go:54-63` and `:140-157`. The comment
  says "A record stored before the request was persisted has none". All four live
  writers set `Request: &build.request`
  (`course_agent.go:839`, `:932`, `:1472`, `course_pipeline.go:143`), so no live
  producer makes the field nil. This is backward compatibility in a greenfield
  repo. Remedy: delete the fallback branch (and, as a store follow-up, make the
  column `NOT NULL`). Marked for the store owner because the column change is
  outside this domain.

- Stage-to-error-code switch, written twice — `course_agent.go:1288-1300`
  (`courseRefused`) and `pipeline.go:510-522` (`exhausted`). They differ only in
  the fallback and in `StageValidateArtifact`, which the README calls a deliberate
  divergence. Remedy: one helper `codeForStage(stage string) (string, bool)` with
  each caller supplying its own fallback.

- `finalizeCourse` reports timings that were never measured —
  `course_agent.go:1240-1246`. `stageStart := time.Now()` is the start for three
  SQL stages, so reference, cross-check and fixtures all report roughly 0 ms for a
  course that spent minutes on them. The job view surfaces `MS`. Remedy: carry the
  per-stage start from `verifyChapterInEnv`, or stop publishing a number that is
  not a measurement.

- The `hint_level` contract is two string literals that agree by luck —
  written at `review.go:410` (`"\nhint_level: %d\n"`), parsed at
  `fake.go:345-351`. This is the same class the 2026-10-18 pass fixed for the
  outline marker. Remedy: one shared format constant in `ai`, referenced by both.
  Low severity because the fallback is level 1, not a failed run.

- The outline JSON Schema hardcodes the chapter bound — `course_agent.go:632`
  (`"maxItems": 8`) against `maxCourseChapters = 8` (`course.go:46`). The gate is
  `validateCourseOutline` (`:146`). The schema must be a literal, and the sibling
  schemas take the same trade, so this is the accepted wire-hint pattern rather
  than a defect. If the repo wants one truth, build the string with
  `fmt.Sprintf("...%d...", maxCourseChapters, ...)`.

## Move / split

`course_agent.go` is six jobs: outline types and gate, build state, prompt text
and schemas, run orchestration plus resume, in-session tool handlers, storage,
tool-result rendering. `course_agent_tools.go` is only the pi loop and its tool
definitions, which is why the boundary already works. Exact cut, following
`docs/target-architecture.md:46-49`:

- `course_agent_outline.go`: `CourseOutline`/`OutlineChapter` (`:84-108`),
  `validateCourseOutline` (`:124-191`), `courseOutlineExerciseError` (`:206-225`).
- `course_build.go`: status constants (`:234-250`), `courseBuild`/`chapterWrite`
  (`:257-283`), `newCourseBuild` (`:289-321`), `newCourseBuildFromRecord`
  (`:333-367`), `requestFromRecord` (`:374-386`), `outlineFromRecord`
  (`:394-425`), `plannedExercisesPerChapter` (`:432-440`), `courseID` (`:445-451`),
  and the method block `:454-579`.
- `course_prompt.go`: `courseOutlinePrompt` (`:601-605`), `courseOutlineSchema`
  (`:611-649`), `courseAgentSystemPrompt` (`:653-657`), the goal markers
  (`:666-669`), `courseAgentPrompt` (`:671-682`), `storedChapterTurns`
  (`:691-718`). Moving the fake's `outlineJSONFromGoal` reader beside the markers
  is optional (UNCERTAIN).
- `course_agent.go` keeps run and resume (`:729-1007`), the tool handlers
  (`:1030-1217`, minus the deleted `handleToolCall`), finalize and refusal
  (`:1221-1308`), chapter helpers (`:1316-1446`), storage (`:1453-1506`) and
  `toolResult` (`:1513-1520`). That is still about 700 lines; a second optional
  cut is `course_agent_storage.go` (`:1453-1506`), or moving the tool handlers next
  to their pi closures in `course_agent_tools.go`.

File hygiene only:

- `challengeSchema` — `fake.go:371-425`. Live in production, read by
  `pipeline.go:392` and `:428`, but declared in the fake provider's file beside
  `courseChapterSchema` and `finishCourseSchema` in `course.go:316,375`. It is the
  challenge wire contract, not provider content. Move it to the schema home
  (`course.go` or a new `prompt.go`) so `pipeline.go` does not depend on a
  constant that lives in `fake.go`.

## Earned (checked, keep)

- `toolSaveChapter` / `toolFinishCourse` (`:58-59`) — live in the pi tools and in
  the fake's emitted calls (`fake.go:598,608`).
- `maxChapterWrites` (`:69`) — read at `:1168`, `:1195`; `maxToolResultChars`
  (`:75`) — read at `:1519`.
- `CourseOutline` / `OutlineChapter` (`:84-108`) — live in `outlinePhase:784`,
  `courseOutline:517`, and the prompt round-trip.
- `validateCourseOutline` (`:124`) — live at `:793`; `courseOutlineExerciseError`
  (`:206`) — live at `:180`.
- `courseBuild`, `chapterWrite`, and every method (`index`, `next`, `missing`,
  `resolved`, `verifiedChapters`, `exerciseCount`, `measuredRows`, `record`,
  `sortReport`, `fullReport`, `verifiedCount`, `gateFacts`, `chapterIDs`) — each
  has a non-test caller (`grep` per method; see `:1065`, `:1203`, `:1223`).
- `storedChapterTurns` (`:691`) — live at `course_agent_tools.go:149`; renders
  the resumed turns the fake and a real model both read.
- `goalOutlineMarker` / `goalOutlineEnd` (`:667-668`) — live at `:674`, `:680`
  and `fake.go:649,654`; the landed one-symbol fix.
- `generateCourse` (`:729`) — live at `pipeline.go:368`; `BuildCourse` (`:852`)
  at `jobs.go:311` and `httpapi/ai.go:623`; `RecoverInterruptedBuilds` (`:946`) at
  `cmd/api/main.go:318`; `awaitApproval` (`:825`) reachable through
  `RequireApproval`, which the client sets (`web/src/state/store.tsx:1772`).
- `runCourseAgent` (`course_agent_tools.go:45`) and the `StreamFn` fallback
  (`:64-71`) — the nil branch is the real-provider path.
- `recoverToolCallsFromText` and helpers (`course_agent_tools.go:191-311`) —
  `docs/target-architecture.md:88-90` names this as the kept transport repair.
- `jsonSchemaToPi` / `schemaFromMap` (`:401-453`) — live for four tools; the
  byte-identical piai copy is gone.
- `assistantTurnResponse` / `textFromContent` / `providerToolCall`'s callers
  (`:479`, `:499`) — live in the transcript recorder and the fake.
- Fake generator: `coursePlanShape`, `courseOutline`, `fakeChapter`,
  `fakeChapterArgs`, `topicByName`, `courseTitle`, `goalFromPrompt`,
  `courseSectionIntro`, `courseSectionDiagram`, `pickTopic`, `scenarioSuffix`,
  `difficultyFromText`, `hashPrompt`, `tutorText`, `critiqueText`, `challengeSchema`
  — each has a live caller in `Complete` or the course stream (`census in
  Collapse/Move`).

## UNCERTAIN

- `reset_environment`: whether to implement a real reset or delete the tool. The
  code proves it cannot work as written; the product call is whether the agent
  needs a reset at all. Delete is the cheap honest option; a `Runner`-backed
  re-create is the alternative.
- The shadow-state collapse (Collapse 3). It touches the job view and the store
  write, so it needs the owner's sign-off that `Progress` may be derived from the
  build rather than mirrored.
- A saved chapter may carry zero exercises. The outline gate requires one
  (`course_agent.go:170`) and the prose says so (`:209`, `:223`), but
  `validateCourseChapter` only bounds the maximum (`course.go:187`) and the
  chapter schema has `maxItems` only (`course.go:341`). A reachable path would
  ship an empty chapter, but no test or model run proves it happens. Remedy if
  confirmed: add the minimum to `validateCourseChapter`.
- The `maxItems: 8` schema literal (Collapse 8) is either the accepted wire-hint
  convention or an interpolation. Owner call, since the sibling schemas already
  hardcode their bounds.
- Moving `outlineJSONFromGoal` (`fake.go:648`) next to the shared markers would
  make the prompt round-trip one file, but the fake must keep reading the prompt
  (not a side channel) to stay a pure function. Cosmetic.
