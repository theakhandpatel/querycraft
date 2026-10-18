# ai-course-agent — wave 2 verification

Scope: the wave-1 report `docs/reviews/2026-10-18/wave1/ai-course-agent.md`, against
`backend/internal/ai/course_agent.go` (1,520 lines), `course_agent_tools.go` (516),
`fake.go` (738), plus the files the report cites (`course.go`, `pipeline.go`,
`course_pipeline.go`, `store/courses.go`, `review.go`, `envdb/scratch.go`,
`dataset/roles.go`, the tests). Read-only. `rg` is not installed here, so every
search below is the same pattern run with the grep tool or `grep -rn` over the
whole repo.

## Verdict

Wave 1 mostly survives: 15 proposed actions, 5 hold exactly as written, 9 need a
correction to the exact remedy, and 1 (the `maxItems: 8` literal) is dropped
because the report itself rules it accepted. The dead symbols are real
(`handleToolCall`, `toolResultFromContent` have no caller anywhere, tests
included), and the reset tool is a fabricated capability. The corrections cluster
in three places: the report mis-attributes the pipeline mirror to `storeBuilding`,
it misses the live legacy test that pins the `requestFromRecord` fallback, and it
leaves the whole-plan gate `validateCoursePlan` in place after retiring the path
that was its only caller. The split boundaries are close but leave three
`courseBuild` functions and two outline/build helpers on the wrong side. Two
missed items: `validateCoursePlan` is production code with only test callers, and
the per-chapter exercise minimum the outline gate enforces is not enforced when a
chapter is saved.

## Confirmed actions

Ordered by value.

- [ ] Delete `handleToolCall` (`course_agent.go:1009-1025`). Files:
      `backend/internal/ai/course_agent.go`. Evidence: repo-wide `handleToolCall`
      returns exactly two hits, both in that file (comment `:1009`, definition
      `:1013`); the live callers are the pi closures
      `course_agent_tools.go:327` and `:341`. Safe now: it is the only caller of
      `saveChapterTool` besides the closure, and the only reader of
      `call.Function.Name` in the agent path.
- [ ] Delete `toolResultFromContent` (`course_agent_tools.go:465-474`). Files:
      `backend/internal/ai/course_agent_tools.go`. Evidence: repo-wide search
      returns only the comment `:465` and definition `:466`; every live reader
      calls `textFromContent` (`course_agent_tools.go:124,126,128,205`,
      `fake.go:573`). Safe now: no test or production caller.
- [ ] Delete `FakeProvider.courseBundle` (`fake.go:503-511`) and
      `TestCourseGateAcceptsTheBuiltInPlan` (`course_test.go:257-274`). Files:
      `backend/internal/ai/fake.go`, `backend/internal/ai/course_test.go`.
      Evidence: repo-wide `courseBundle` returns the definition `fake.go:505`,
      the only caller `course_test.go:260`, and docs; its own comment says "the
      agent never asks for it". The greenfield law retires the M7 whole-plan
      path. Safe now: the callers are the test and nothing else.
- [ ] Collapse the stage-to-error-code switch into one helper. Files:
      `backend/internal/ai/course_agent.go` (`courseRefused:1288-1300`),
      `backend/internal/ai/pipeline.go` (`exhausted:510-522`). Evidence: the two
      switches are identical for `StageCrossCheck` and
      `StageReferenceContract`; `courseRefused` additionally maps
      `StageValidateArtifact -> CodeInvalidArtifact` (`course_agent.go:1297-1298`)
      and catches a nil `last`. `backend/README.md:411-414` records that
      divergence as deliberate and caught by a test. Safe now: give the helper
      `codeForStage(stage string) (code string, ok bool)` covering the two shared
      cases only, let `exhausted` fall back to `CodeRepairExhausted`, and let
      `courseRefused` add the `StageValidateArtifact` arm and its default stage.
- [ ] Stop `finalizeCourse` publishing timings it never measured
      (`course_agent.go:1240-1246`). Files: `backend/internal/ai/course_agent.go`.
      Evidence: `stageStart := time.Now()` is taken at `:1240` and reused for
      `StageReferenceContract`, `StageCrossCheck` and `StageAdversarialFixtures`
      at `:1241-1246`, so all three report roughly 0 ms for a course whose
      exercises each carry a real `MS` (`course_pipeline.go:86`). Safe now:
      replace the three `p.timed(...)` calls with `p.mark(...)`, or carry the
      measured start. It only removes a false number.

## Amended

Same shape, with what changed and why.

- `reset_environment` tool + `courseResetSQL` (`course_agent_tools.go:381-396`,
  `:455-463`). Direction right, remedy incomplete. The tool is dead (no test,
  no turn; only the advertisement at `:382`), and it cannot work: the SQL is
  `DROP SCHEMA IF EXISTS dataset CASCADE` with no re-apply (`:458-463`), the
  scratch executor is the restricted learner (`envdb/scratch.go:28-48`), and
  the learner holds `USAGE/SELECT` plus `CREATE` on `dataset`, never ownership
  (`dataset/roles.go:68-70`, `template.go:197-211`), so the drop is refused;
  if it ran, `checkDataset` (`pipeline.go:661`) would fail every later chapter.
  Added edits: the file header comment at `course_agent_tools.go:36-37` names
  the tool and must go with it. Drop the `progress.md:2543` edit: that line is
  inside the dated section "The pi course agent landed... 2026-10-17", a
  historical record. Strike it only if the repo treats `progress.md` as current
  state, not a log.
- `FakeProvider.Delay` (`fake.go:29-33`, branch `:56-64`). Direction right,
  remedy needs a correction. The only setter is
  `httpapi/jobs_test.go:133`; no production caller. But the suggested replacement
  `gatedProvider` lives in `ai/jobs/jobs_test.go:67-113`, a test file in another
  package, so it cannot be imported. The httpapi test needs its own holding
  wrapper; `httpapi/jobs_test.go:513-564` (`crossCheckBreaker`) is the existing
  in-package precedent. Delete the field, the branch, and the `time` import if
  it becomes unused (`fake.go` still uses `time` at `:65,108`, so it stays).
- `providerToolCall` and `saveChapterTool`'s `provider.ToolCall` parameter
  (`course_agent_tools.go:509-516`, `:326-327`; `course_agent.go:1030`).
  Direction right, one claim is wrong: "delete `providerToolCall` plus the
  `provider.ToolCall` import use" is false. `provider.ToolCall` is still built
  and read by the transcript path (`course_agent_tools.go:490-493`,
  `transcript/transcript.go:268-269`), so the `provider` import stays. Change
  the signature to `saveChapterTool(ctx, build, ex, argsJSON string)`, pass
  `string(args)` at `:326`, delete `providerToolCall` and the `call` variable,
  and read `argsJSON` directly. `providerToolCall` itself has exactly one caller
  (`:326`).
- The `OutlineChapter` to `CourseChapter` map, three copies
  (`course_agent.go:301-317`, `:1316-1332`, `:1486-1506`). Direction right, two
  corrections. First, `withChapterSkeleton` (`:1316`) does not empty the slices
  the way `newCourseBuild` and `buildingChapters` do; it preserves the model's
  `Sections`/`Exercises` and only defaults nil to empty, and it takes `N` from
  `prev.N`, not from the outline. The constructor
  `func (o OutlineChapter) chapter(n int, prev schema.CourseChapter)
  schema.CourseChapter` is fine only if it starts from `prev` and overlays the
  outline fields, so call it as `outline.chapter(prev.N, chapter)`. Second,
  `gateFacts` (`:1338-1351`) and `chapterIDs` (`:1355-1361`) are also
  `courseBuild` methods; they belong with the build, not the helpers block
  (see the split).
- The pipeline mirrors `courseBuild` by hand
  (`course_agent.go:1128-1132`, `:1453-1480`). Direction right,
  mis-attributed. Only `publish` (`:1128-1132`) assigns `p.chapters` and
  `p.verified`; `storeBuilding` (`:1453-1480`) does not touch those fields, it
  only writes `build.report` into the stored outcome and calls `p.notify()`
  after setting `p.courseID`. The dead setter is real: `grep -n 'setVerified'`
  returns its definition (`pipeline.go:195`) and no call. So the collapse
  removes `p.verified`, `p.chapters` and `setVerified`, not the whole job view
  (`planned`, `currentChapter`, `courseID` are not on `courseBuild`). It still
  changes the `Progress` contract, so the owner sign-off in UNCERTAIN stands.
- `requestFromRecord`'s nil-`Request` fallback (`course_agent.go:374-386`).
  Direction right, the remedy misses a live consumer: the fallback is pinned by
  `TestALegacyRecordWithoutARequestIsStillBuildable`
  (`course_agent_test.go:1125-1174`, which stores a record with no `Request` at
  `:1135-1140` and asserts the recovered fields). All four creation writers do
  set `Request` (`course_agent.go:839`, `:932`, `:1472`;
  `course_pipeline.go:143`), and `RecoverInterruptedBuilds` only re-saves a row
  it read (`course_agent.go:967`), so the branch is unreachable in production.
  Delete the test and the branch (make it `req := *rec.Request`), and, as the
  report says, the store follow-up is to make the column `NOT NULL`
  (`store/courses.go:54-63` and `:140-157`, with the nil-request cases in
  `store/courses_test.go`, `sqlite_test.go`, `migrate_test.go`).
- `hint_level` written and parsed from two literals. Direction right, there is a
  third site: `review.go:410` writes `"\nhint_level: %d\n"`, `fake.go:345-351`
  parses `"hint_level: 0/2/3"`, and `review_test.go:66` asserts
  `"hint_level: 3"`. One shared symbol in `ai` (a format constant plus a
  `parseHintLevel` helper) covers all three. Low severity, as reported; the
  fallback is level 1.
- The `course_agent.go` split (`docs/target-architecture.md:46-49`). Direction
  right, boundaries need three corrections. `outlineExerciseCount`
  (`:1001-1007`) is an outline helper and belongs in `course_agent_outline.go`,
  not the run block. `gateFacts` (`:1338-1351`) and `chapterIDs` (`:1355-1361`)
  are `courseBuild` methods and belong in `course_build.go`. `courseHasWrittenChapter`
  (`:977-984`) is a plan helper and also belongs with the build. The tool-names
  const (`:57-60`) and bounds const (`:64-76`) are not assigned to either file
  and are used from both `course_agent.go` and `course_agent_tools.go`, so keep
  them in `course_agent.go` (or a shared file) and say so. The rest of the ranges
  check out against the function list (`validateCourseOutline:124-191`,
  `courseOutlineExerciseError:206-225`, `newCourseBuild:289-321`,
  `newCourseBuildFromRecord:333-367`, `courseID:445-451`, `courseOutlinePrompt:601`,
  `courseOutlineSchema:611-649`, `storedChapterTurns:691-718`,
  `storeBuilding:1453-1480`, `toolResult:1513-1520`). "About 700 lines" is closer
  to 760 kept.
- `challengeSchema` (`fake.go:390-425`). Direction right, the destination should
  be consistent with the split: after the split, `courseOutlineSchema` moves to
  `course_prompt.go`, while `courseChapterSchema`/`finishCourseSchema` stay in
  `course.go` (`:316`, `:375`). Pick one schema home for all four
  (`challengeSchema`, `courseOutlineSchema`, `courseChapterSchema`,
  `finishCourseSchema`) and the inline `run_sql` schema
  (`course_agent_tools.go:351`) with them; otherwise the wire schemas live in
  three files. Evidence: `challengeSchema` is read only by `pipeline.go:392` and
  `:428`.

## Dropped

- The `maxItems: 8` literal (`course_agent.go:632`) versus
  `maxCourseChapters = 8` (`course.go:46`). The report concludes it is the
  accepted wire-hint pattern, not a defect, so there is no action. Counter-note:
  it is a real duplicate, but the sibling schemas hardcode their bounds too, so
  interpolating one literal buys nothing on its own.

## New

- `validateCoursePlan` is production code with no production caller
  (`course.go:97-143`). `grep -rn 'validateCoursePlan'` returns the gate itself,
  two comments, and the two test callers (`course_test.go:243` and `:268`).
  Deleting `courseBundle` retires one of those callers (`:268`) but leaves the
  function and the table test at `:243`. Under the greenfield law, delete the
  gate; re-point the table test at `validateCourseChapter` (the shared, live
  gate called from `course_agent.go:1066`) or delete it. This is the missing half
  of the Delete item for `courseBundle`. `backend/README.md:431-435` names it as
  test-only but the wave-1 law overrides that ruling.
- A saved chapter may carry zero exercises, and nothing refuses it. The outline
  gate requires one per chapter (`course_agent.go:170`), but
  `validateCourseChapter` bounds only the maximum (`course.go:187`) and
  `courseChapterSchema.exercises` has `maxItems: 4` and no `minItems`
  (`course.go:340-341`). `saveChapterTool` runs that gate and then
  `verifyChapterInEnv`, so a chapter with `"exercises": []` would pass, verify
  with zero exercises, be marked `ChapterVerified`, and be stored. This settles
  the wave-1 UNCERTAIN item at the code level: the gap is reachable. Whether a
  model does it needs a run. Add `minItems: 1` to the schema and a
  `len(ch.Exercises) == 0` arm to `validateCourseChapter` so the outline's rule
  and the save-time rule are one truth.

## Still uncertain

- Whether the product wants a reset tool at all. The code proves the shipped one
  cannot restore anything; delete is the honest move. A `Runner`-backed re-create
  is the alternative and is a product call.
- The shadow-state collapse (Amended above). The technical facts are settled
  (`setVerified` is dead, only `publish` mirrors), but deriving `Progress` from
  the build changes the job view contract, so it needs the owner's sign-off.
- The empty-chapter gap needs a run to know how often a model leaves a chapter
  empty; the code gap itself is confirmed (New).
- The `maxItems: 8` interpolation, if the repo ever wants one schema truth.
  Owner call, since every sibling schema hardcodes its bounds.
- Moving `outlineJSONFromGoal` (`fake.go:647`) beside the shared markers is
  cosmetic. The fake must keep reading the prompt to stay a pure function, so
  leave it.
