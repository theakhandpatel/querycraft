# Thermo-nuclear review 02: the generation layer (`backend/internal/ai`)

Scope: `internal/ai` plus `ai/jobs`, `ai/provider`, `ai/provider/piai`,
`ai/transcript`. Read-only; `go build ./...` is green. ~7,600 non-test lines,
~4,750 test lines.

## Verdict

The generation layer is honest and well-documented, but the pi adoption left a
seam that describes the same provider three times, and it did not finish the
deletion its own plan named. The pipeline (stages, evidence, repair budget,
transcript, scratch environment) earns its keep and should stay. What does not
earn its keep is the second provider description: `Config.Provider`,
`Config.RawConfig` and `Config.StreamFn` are three views of one configuration,
kept in sync by hand at every wiring site, and the course agent re-resolves the
pi model with `resolveCourseModel`, a hand-rolled copy of `piai.New`. That single
duplication is the root of the two-seam split, and it gave us a byte-identical
copy of `jsonSchemaToPi`. The pre-pi course wire surface
(`ProviderRequest.Messages/Tools/ToolChoice`, `ProviderResponse.FinishReason`,
the conversation branches of both transports) is now dead but still present.
`course_agent.go` at 1,513 lines carries six responsibilities in one file. The
fake provider is a genuine second implementation whose outline is recovered by
parsing the production prompt literal, so the shipped default path has a
stringly invariant linking two files. None of this is a behaviour bug; all of it
is structure that a handful of code-judo moves can delete rather than polish.
Below the fold: a dead exported constant here, a test-only gate there, and a
`sectionCount` nobody calls.

## Findings, ranked

### 1. One provider, described three times; the course agent re-implements pi's model resolution

`Config` carries `Provider provider.Provider` (`api.go:245`), `RawConfig
AIRawConfig` (`api.go:248`, defined `api.go:284-289`), and `StreamFn
agent.StreamFn` (`api.go:255`). Every construction site sets all three from the
same source: `cmd/api/main.go:283-295`, `httpapi/admin_ai.go:202-212`, and again
at `admin_ai.go:294-301` for the persisted setting. The course agent never reads
`Provider` for its model; it calls `resolveCourseModel(p.svc.cfg.RawConfig)`
(`course_agent_tools.go:49`), and that function (`course_agent_tools.go:476-497`)
re-derives the base URL, the "vendor/model" split and the catalog lookup that
`piai.New` already implements (`provider/piai/pi_ai.go:64-95`). So `piai` exists
for the challenge path's one-shot `Complete`, and the course path bypasses it
entirely. This is the defect the user asked about: two provider seams, and a
third data shape (`AIRawConfig`) whose only job is to survive the mismatch. The
plan recorded in `progress.md` (commit `6f61c71bd`) said to kill `piai` and the
provider interface for course generation; the implementation kept both and added
`resolveCourseModel` and `RawConfig` instead. The remedy is to give the provider
that serves `Complete` one more capability: an optional interface, defined in
`ai`, that hands the course agent its pi primitives (model record, key resolver,
and either a stream function or nil to mean "pi resolves it"). `FakeProvider`
returns its built-in model and its `CourseStreamFn`; `piai.PIAI` returns its
resolved model and key; the custom-base-URL `openai-compatible` shape returns a
custom model. Then delete `AIRawConfig`, `resolveCourseModel`, the `CourseStream`
type-assertion helper (`api.go:274-279`), and the `StreamFn` field on `Config`
(keep the test-scripting seam on the provider instead, as `helpers_test.go:344-361`
already hand-switches on concrete types to emulate). Three wiring sites, one
copied resolver and one whole concept disappear. This is the single best
code-judo move in the layer.

### 2. The pre-pi course wire surface is dead but still shipped

The provider comment says `Messages` "exists because the course agent writes one
chapter per call" (`provider/provider.go:31-34`), but after the pi adoption the
course agent builds its conversation from pi message types and never sets those
fields. Nothing in production constructs a `ProviderRequest` with `Messages`,
`Tools` or `ToolChoice`: the only writers in the tree are the two transports
translating them (`http_provider.go:173`, `piai/pi_ai.go:231`). `FinishReason` is
written by both providers (`http_provider.go:250`, `piai/pi_ai.go:154`) and read
by nothing. That makes the conversation branch of `HTTPProvider.messages`
(`http_provider.go:300-311`), the tool/`response_format` exclusivity branch
(`http_provider.go:186-194`), and the whole `Messages`/`Tools` half of
`piai.toContext` (`piai/pi_ai.go:200-231`) unreachable in production, retained by
tests that exercise the removed design. This is a boundary that no longer has a
caller: the data is already carried through pi. Deleting these fields and their
transport branches removes the last reason the two seams are said to need each
other and shrinks `provider.go`, both transports and their tests. Verify with
`grep -rn 'Messages:\|Tools:\|ToolChoice' --include='*.go' | grep -v _test` first;
it currently returns only the transports themselves.

### 3. `course_agent.go` is 1,513 lines carrying six responsibilities

The file is the largest in the package and nearly twice the 1,000-line line the
skill treats as a presumptive blocker. It holds: outline validation and the
outline-specific error prose (`121-222`), the `courseBuild` state model and its
derivations (`224-576`), prompt and system-prompt strings plus the outline JSON
Schema (`578-711`), run orchestration, approval and resume (`713-1000`), the
in-session tool handlers (`1002-1210`), chapter helpers (`1303-1439`), storage
(`1441-1499`) and tool-result rendering (`1501-1513`). A reader holding
"validate an outline" and "store a building course" in one file cannot scan it.
The split is mechanical and low-risk: `course_agent_outline.go` (outline types,
`validateCourseOutline`, `courseOutlineExerciseError`, the outline schema and
prompt), `course_build.go` (`courseBuild`, `chapterWrite`, all its methods and
`outlineFromRecord`/`requestFromRecord`), `course_prompt.go` (the three prompt
functions and the two schemas), and leave the run/tool/storage code in
`course_agent.go`. `course_agent_tools.go` is already the extracted agent loop
and shows the pattern works. `pipeline.go` is the second file over the line
(1,019) and is the natural home for a companion split (stage tracking vs request
rendering), but it is a smaller problem.

### 4. The shipped default provider recovers the outline by parsing the production prompt, through a duplicated literal

The default `ai_provider` is `fake` (`internal/config/config.go:107`), so the
built-in provider's chapter loop runs by default. That loop does not hold the
outline it generated; it re-reads it out of the goal prompt by locating
`goalOutlineMarker` and `goalOutlineEnd` (`fake.go:623-626`, parsed at
`fake.go:650-666`). Those constants are a copy of two literals written by
`courseAgentPrompt` (`course_agent.go:660-671`): the marker is the exact text at
line 663 and the end marker is the prefix of line 669, but neither site uses the
constant. Edit the production prompt sentence and the built-in provider silently
stops finding the outline, returning "I cannot find the agreed outline" and
failing the whole course run with no test pointing at the cause. This is a
load-bearing invariant expressed twice. The immediate fix is to make
`courseAgentPrompt` write `goalOutlineMarker` and `goalOutlineEnd` (or to move
both constants into the course-prompt file and reference them from the fake), so
the coupling is one symbol instead of two literals. The larger observation: the
fake is a second course generator (`coursePlanShape`, `courseOutline`,
`CourseStreamFn`, `fakeChapterArgs`, plus `bundle`), ~300 lines, and the
prompt-round-trip is the glue that lets it stay a pure function. That purity is
worth keeping, but it should be a deliberate, one-symbol contract, not two
string literals that agree by luck.

### 5. `pipeline` shadows `courseBuild`'s chapter state and syncs it by hand

`pipeline` carries course-only fields `planned`, `verified`, `currentChapter`,
`courseID` and `chapters` (`pipeline.go:81-91`), and `Progress` republishes them
(`pipeline.go:109-124`). But the authoritative course state lives in
`courseBuild` (`course_agent.go:254-280`); `publish(build)` copies
`build.report` and recomputes `verified` into the pipeline on every accepted or
dropped chapter (`course_agent.go:1121-1125`), and `storeBuilding` copies them
again. Two holders of one fact, with a manual sync call at each transition, is
exactly the duplicated chapter-state tracking the user suspected. Worse,
`courseBuild` itself indexes four parallel slices by outline position: `plan`
(`[]CourseChapter`), `outline.Chapters` (`[]OutlineChapter`), `written`
(`[]chapterWrite`) and `report` (`[]ChapterResult`), and `written[i].state` is
partly derivable from whether `plan.Chapters[i].Exercises` is non-empty
(reconstructed that way in `newCourseBuildFromRecord`, `course_agent.go:339-346`).
A single `[]chapterState` per position, holding status, write count, last error
and the measured result, with the plan and report derived from it, collapses the
parallel indexing. This is a medium-conviction move: the invariant is real (the
store writes the plan, the job reads the report), so it needs the store and
outcome shapes held fixed, but the manual `publish` sync and the four-slice
indexing are the parts a state model should delete.

### 6. `saveChapterTool` mutates the stored plan non-atomically and restores it four ways

`saveChapterTool` (`course_agent.go:1023-1114`, 91 lines) decodes arguments,
finds the chapter, saves the skeleton, writes the candidate into
`build.plan.Chapters[pos]`, validates, verifies, and on any of three failures
restores the skeleton by hand (`course_agent.go:1060`, `:1064`, `:1072`), each
with its own `chapterRefused` call. The mutation is deliberate (the exercise gate
and `verifyChapterInEnv` read the plan, and the verified `Expected` rows are
written back into it), but the four-way restore is the tell that the
"place-candidate, gate, roll-back" pattern wants a helper. A `tryChapter`
function that installs the candidate, runs the two gates, and always restores the
skeleton unless it returns a verified result would remove the three repeated
restore lines and the fourth implicit success path, and would make "a rejected
chapter must not leave half of itself behind" (the comment at `:1051-1054`)
enforced in one place. The same function naturally returns the reason it refused,
which `chapterRefused` (`course_agent.go:1139-1191`) currently reconstructs from
scattered state.

### 7. The `OutlineChapter` → `CourseChapter` field mapping is copied three times

`newCourseBuild` (`course_agent.go:298-314`), `withChapterSkeleton`
(`course_agent.go:1309-1325`) and `buildingChapters` (`course_agent.go:1479-1499`)
each list the same fields (id, n, title, summary, kind, minutes, objectives) and
each handles the empty slices separately. Three copies of one mapping will drift
the moment a chapter gains a field. One constructor, `func (o OutlineChapter)
chapter(prev schema.CourseChapter) schema.CourseChapter`, called by all three,
states the invariant once, which is the one `withChapterSkeleton` exists to
protect: the outline is what the learner has been shown, so a chapter cannot
rename itself mid-course.

### 8. Tool-call recovery is transport repair living in the policy package

`recoverToolCallsFromText` and its helpers (`course_agent_tools.go:198-318`,
~120 lines: `messageHasToolCalls`, `parseTextToolCalls`, `jsonCandidates`,
`fencedCodeBlocks`, `firstOf`) wrap every stream to rescue servers that answer
with a tool call written as JSON in the message text. The commit that added it
says it was needed against a real model, so its existence is not speculative.
The problem is placement and ownership: parsing a provider's prose into tool
calls is exactly the "wire primitives are a solved problem and not ours" claim
the ADR reopening makes, and this is a second implementation of tool-call
extraction sitting above pi's adapters. `fencedCodeBlocks` even hardcodes the
`json`/`JSON` fence tags and an 8-call cap. The remedy is either to push the
recovery into the provider adapter (`piai`, where the wire format lives) or to
feed it back upstream, leaving the policy package with tool *definitions* and
*gating* only. Treat this as a boundary finding rather than a deletion: the
behaviour is load-bearing, its home is wrong.

### 9. Dead and test-only surface the package carries

These are small individually and were already named in `backend/README.md`
(§"Proposals, not implemented"); together they add concepts a reader must
discount. `sectionCount` (`course.go:96-102`) has no caller. `CodeAIAvailable`
(`api.go:33`) is unused. `GenerationRequest.Steps` is validated (`validate.go:60-62`,
`:71-74`) but never rendered into any prompt. `Config.AdversarialFixtures`
(`api.go:261`) has no setter in the tree, so the branch that would use it
(`pipeline.go:734-738`) is never taken in production. `validateCoursePlan` and
`FakeProvider.courseBundle` run only from tests (`course.go:109`,
`fake.go:505`). `provider.DefaultMaxOutputBytes` is real (it bounds the
transport), but `FinishReason` and the conversation fields above are not. The
guard here is Chesterton's Fence: several of these are called only by tests, so
deleting the production symbol means deleting a test, which the brief forbids.
The honest catalogue is: delete `sectionCount`, `CodeAIAvailable`, `Steps`, and
`AdversarialFixtures` (plus its branch) once their tests move; leave
`validateCoursePlan`/`courseBundle` as the M7 regression suite, marked as such.

### 10. `finalizeCourse` reports timings that were never measured, and the stage-to-code switch is written twice

`finalizeCourse` stamps three SQL stages done with `stageStart := time.Now()` as
the start (`course_agent.go:1233-1239`), so reference, cross-check and fixtures
all report ~0 ms for a course that spent minutes on them. The job view surfaces
`MS`, so this is a reported number that is not a measurement. Separately, the
stage-to-error-code mapping exists twice: `exhausted` (`pipeline.go:510-524`) and
`courseRefused` (`course_agent.go:1281-1296`) differ only in their fallback and
their handling of `StageValidateArtifact`, which the README records as a
deliberate divergence. A `codeForStage(stage string) (string, bool)` helper with
each caller supplying its own fallback preserves the divergence and deletes the
duplicated switch. Neither is structural, but both are the sort of reported fact
a reviewer should not have to distrust.

## Remediation sequence

Order is by leverage per unit of risk; each step is independently shippable and
behaviour-preserving.

1. Link the outline literals (finding 4's immediate fix): have
   `courseAgentPrompt` use `goalOutlineMarker`/`goalOutlineEnd`, or move the pair
   into the course-prompt file and reference it from `fake.go`. One symbol, no
   behaviour change, removes a silent default-path failure.
2. Collapse the provider descriptions (finding 1). Add the one optional
   interface in `ai`; implement it on `FakeProvider` and `piai.PIAI`; delete
   `AIRawConfig`, `resolveCourseModel`, `CourseStream`, `Config.RawConfig` and
   the three `StreamFn` wiring lines. Move the test stream onto the provider.
   Verify with the existing `course_agent_test.go` suite and
   `make audit-course-gen`.
3. Delete the dead wire surface (finding 2): `ProviderRequest.Messages/Tools/
   ToolChoice`, `ProviderResponse.FinishReason`, the conversation branches in
   both transports, and the tests that exercised them. This is the payoff step
   that makes step 2's seam the only seam.
4. Split `course_agent.go` by responsibility (finding 3):
   `course_agent_outline.go`, `course_build.go`, `course_prompt.go`. Pure move.
5. Factor the candidate/roll-back pattern into `tryChapter` (finding 6) and the
   one `OutlineChapter.chapter` constructor (finding 7). Small, local, and both
   remove copy-paste.
6. Unify the course progress state (finding 5) if step 4 has not already made the
   shadow copies obviously redundant; this is the one move that touches the
   store/job contract, so do it last and in its own change.
7. Sweep the dead surface (finding 9) and the reported-timing/code-switch
   duplication (finding 10). Move or delete the tests that guard the removed
   symbols in the same change.

Do not bundle steps 1-3 with 4-7: the first three change a data shape and want a
short, reviewable diff; the rest are mechanical extractions.

## Measurements

- Non-test lines in `internal/ai` (recursive): 7,582. Tests: 4,746.
- Files over 1,000 lines: `course_agent.go` 1,513; `pipeline.go` 1,019.
- Other large non-test files: `fake.go` 741; `course_agent_tools.go` 583;
  `validate.go` 548; `review.go` 511; `jobs/jobs.go` 511; `api.go` 467.
- Functions at or over 50 lines: 16, including `runCourseAgent` (131),
  `HTTPProvider.Complete` (107), `saveChapterTool` (91), `generateRecorded` (90),
  `courseAgentTools` (83).
- `jsonSchemaToPi`/`schemaFromMap` bodies are byte-identical in
  `course_agent_tools.go:407-460` and `piai/pi_ai.go:234-289` (54 lines each);
  `diff` reports only a two-line comment difference.
- Production setters of `ProviderRequest.Messages`/`Tools`/`ToolChoice`: none
  (`grep -rn 'Messages:\|Tools:\|ToolChoice' --include='*.go' | grep -v _test`
  returns only the two transports). Readers of `ProviderResponse.FinishReason`:
  none.
- `github.com/sky-valley/pi` is listed `// indirect` in `backend/go.mod:25`
  though `internal/ai` imports it directly; `go mod tidy` hygiene, not a review
  finding.
