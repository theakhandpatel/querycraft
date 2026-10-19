# Expert advice: priorities, product calls, designs and autonomy

Date: 2026-10-18. Author: principal engineer, delegated direction.
Scope: `docs/open-items.md` items 1 to 25, the five product calls, and the next
two items. This is a decision document. It gives a verdict and the shape to
build, not a survey.

Method: I read `docs/open-items.md`, `docs/features.md` section 10,
`docs/target-architecture.md`, all twelve wave-2 verifications under
`docs/reviews/2026-10-18/wave2/` and `docs/reviews/2026-10-18/user-session.md`,
then the files each call depends on. The tracker is stale in two places, and
this document names them.

## 1. Priority order

Item 1 is already implemented and verify-green. The proof is the route
`PUT /courses/{id}` (`backend/internal/httpapi/routes.go:115`), the handler
`handlePutCourse` (`backend/internal/httpapi/ai.go:815`), the store calls
(`web/src/state/store.tsx:1625`, `:1658`) and `course_edit_test.go`. Mark it
`[x]`, not `[~]`.

I keep items 2 and 3 next, then reorder the rest. The rule behind the order:
land the two designed splits first, then the small correctness fixes that fit
one gate cycle, then the dedup sweeps, then the product and architecture calls
last. This keeps every 20-minute run able to finish, gate and stop.

| New | Old | Item | Why here |
| --- | --- | --- | --- |
| 1 | 1 | Persist edits to a stored course | Done and verify-green. |
| 2 | 2 | Split `course_agent.go` | Designed below. Biggest backend readability win, no behaviour change. |
| 3 | 3 | `BuildingScreen` reducer | Designed below. Biggest web readability win, no behaviour change. |
| 4 | 10 | Course setup on course open | A real bug: a stored course's exercise dies with "relation does not exist" on first use. |
| 5 | 7 | "Practise this now" routing | Small, user-visible, wrong today (opens the next chapter, not the reference's). |
| 6 | 6 | Memoize `Markdown` | Tiny, measured (6.8 ms and 13.2 ms), one file. |
| 7 | 11 | AI call-timeout one home | One number, one file pair. |
| 8 | 14 | `piai.CatalogEntry` JSON tags | One small wire fix. |
| 9 | 13 | Move admin provider helpers | Symbol relocation, two call sites. |
| 10 | 12 | Move `login_throttle.go` to `auth` | Symbol relocation; touches `config`. |
| 11 | 16 | Four perf fixtures `definition` key | Delete a dead field across the fixture family, run the fixture gate. |
| 12 | 8 | "Report a problem" | Already deleted (see 2b). Close it. |
| 13 | 9 | "Save challenge" | Already deleted (see 2c). Close it. |
| 14 | 15 | Five test-only `dialect` exports | Policy call answered below: delete. |
| 15 | 4 | Split `store.tsx` | Large. Split into sub-items with one gate each. |
| 16 | 5 | `ai.ts` decoders to `effect/Schema` | Large. Split by decoder groups. |
| 17 | 17 | apidocs uncertain set | Small docs and schema fixes, needs the `operationId` owner call. |
| 18 | 19 | `/runs` screen | New screen, medium. |
| 19 | 20 | Bearer token in `localStorage` | Architecture change. Defer (see 2e). |
| 20 | 18 | `.pill` tones, `resetDialectChoice` | Style and an owner call. |
| last | 21-25 | Accessibility, React Doctor, lint warnings, Node v20, fuzz | Not the current concern, unchanged. |

Two tracker corrections. Items 8 and 9 are already done by deletion, and
`docs/features.md` section 10 still lists them. Update both.

## 2. Product calls

### 2a. Item 7, "Practise this now"

Verdict: honest minimal, and it is a bug fix. The button calls `s.resumeCourse`
(`ChallengeLabScreen.tsx:596`), and `resumeCourse` opens
`openLessonIn(uiState.selectedCourseId, nextChapterIndexOf(course, progress))`
(`store.tsx:975`). That opens the next chapter of the selected course, not the
chapter that owns the reference.

Behaviour: resolve the reference block's own home and open that lesson. Add one
helper to `web/src/lib/model.ts` that maps a block id to `{courseId, moduleIndex}`
and call `s.openLessonIn(courseId, moduleIndex)`. The reference today is
`ex.5.1` from `referenceBlock()` (`ChallengeLabScreen.tsx:22-25`), so the button
lands on that exercise's chapter and cell. That is honest: the offline path's
challenge text is authored, not generated.

The correct bigger one is a real solver screen: an editor that grades the
learner against the stored challenge's contract. That screen does not exist
today, and the lab is a generation and validation view, not a practice view.
Recommend the minimal fix now, and file the solver as its own item. If a
gateway-generated artifact ever gets the button, it must read "Open in the lab"
and route to `#/lab/<id>`; it must not claim to practise.

### 2b. Item 8, "Report a problem"

Verdict: neither route, and the item is stale. `grep` over `web/src` finds no
`reportProblem`, no "Report a problem" and no "Save challenge". Wave 2 deleted
both fake affordances, and the deletions landed. Keep them deleted.

Why neither route is honest from a lesson. `ReportDefect` requires a stored
challenge: it loads `s.cfg.Depot.Challenge(ctx, rep.ChallengeID)`
(`backend/internal/ai/review.go:160`) and reports "No such challenge" when the
id is absent. A built-in exercise has no challenge id. The critique route has
the same shape, `/challenges/{id}/critique`, so it also needs an id the lesson
does not have. The lesson's real helper already ships: "Explain my mistake"
calls `POST /tutor` with `{sql, objective}` (`SqlCell.tsx:233-249`). That is the
same inputs a lesson "report" would have.

If the owner wants defect reporting for built-in exercises, that is a new
backend capability (an exercise-scoped defect route that verifies against the
authored block), not a wiring of the challenge route. File it, do not improvise
it.

### 2c. Item 9, "Save challenge"

Verdict: local draft, not a server artifact, and the item is stale. The two
buttons are gone. Keep them gone.

The lab's real generation already persists server-side: `POST /challenges/generate`
registers a challenge when the learner asks for one. The offline authored `run()`
does not generate, so its step 4 message "challenge registered · version 1"
(`ChallengeLabScreen.tsx:239`) overstates what happened: `draftChallenge`
returns a local `{ok: true}` in both adapters (`execution/browser.ts:164`,
`execution/http.ts:507`). Fix the wording to say the authored challenge was
validated, not registered, and drop the "version 1" claim.

If the owner wants the lab to keep a request, the honest home is a local draft
keyed by account, beside the existing SQL drafts (`saveDraft` in
`store.tsx:123`), not a new endpoint. Nothing is registered until the learner
asks for generation.

### 2d. Item 10, course setup statements

Verdict: run them where the workspace is provisioned for the course, and use a
request that names the course. Do not put a mutating side effect on the course
read.

The setup applies today only inside the reset handler
(`backend/internal/httpapi/environment.go:260` and `:314`, through
`applyCourseSetup`). `GET /courses/{id}` is a pure read, and in this SPA opening
an already-catalogued stored course issues no course request at all. So "on
course open" is not one well-defined request.

Concrete shape: extend the existing per-course warm-up. `store.tsx:876-878`
already calls `api.warmUp(courseDataset, courseDialect)` whenever the selected
course changes. Add `course` to that seam (`shared.ts:79`) and give the http
adapter a real call, `POST /environment/prepare?course=<id>` (new route). The
handler resolves the course owner-scoped through the existing `courseSetup`
helper (`environment.go:359`), ensures the environment, and applies the setup
once per environment generation. Record "course setup applied" beside the
environment in `envdb`, so the call is idempotent and a reset clears it. The
setup statements are not idempotent (a `CREATE TABLE` fails on the second run),
so the marker is required.

Environment unavailable: the route answers `ENVIRONMENT_UNAVAILABLE` with the
existing `environmentUnavailable` shape. The course still opens, the lesson
still renders, and the drawer keeps the honest "Checking the execution
environment…" note (`TablesDrawer.tsx:163-167`). The first Run reports the same
code through the existing statement refusal
(`environment.go:135`). Nothing is invented.

Keep the reset path as it is: a manual reset must re-apply the setup, and that
is the same `applyCourseSetup` call.

### 2e. Item 20, bearer token in `localStorage`

Verdict: not now. Keep `localStorage` for this program and revisit after items
15 and 16 land. The change is not small: once the browser sends the cookie
automatically to the same origin, CSRF defense becomes mandatory, every client
call loses its explicit credential, and `readAuthUser` feeds the AI
`approved_by` echo (`authStorage.ts:15-18`) today. Doing it in one 20-minute run
would leave auth split across two mechanisms.

Smallest honest shape when you do it: the gateway sets `qc_session` as
`HttpOnly; SameSite=Lax; Path=/` on login and signup and clears it on logout;
`httpLayer` drops the `Authorization` header and relies on the same-origin
cookie; delete `readAuthToken`/`writeAuthToken` (`authStorage.ts:20-36`); keep
the non-secret account id for `approved_by`; add a same-origin check on the
mutating routes. The offline adapters have no session and are unaffected.

## 3. Design specs

### 3a. Item 2, split `course_agent.go`

File today: `backend/internal/ai/course_agent.go`, 1,495 lines. The wave-2
boundaries hold with three corrections. The corrections: `storedChapterTurns`
is conversation assembly, not a prompt string, so it stays with the run; all
wire schemas move to one new file rather than splitting across outline and
prompt files; and the shadow-state `Progress` collapse belongs in this item.

Move exactly these symbols. Line numbers are from the current file.

`course_agent_outline.go` (outline types and gates):
- `CourseOutline` (100-107), `OutlineChapter` (111-124).
- `validateCourseOutline` (140-207), `courseOutlineExerciseError` (222-241).
- `outlineExerciseCount` (1000-1006). Wave-2 correction: it is an outline helper.

`course_build.go` (the build state and every derivation):
- Status consts `ChapterVerified`/`ChapterDropped`/`ChapterRefused`/`ChapterSkipped`
  and `ExerciseVerified`/`ExerciseFailed`/`ExerciseNotRun` (250-266).
- `courseBuild` (273-289), `chapterWrite` (291-299).
- `newCourseBuild` (305-327), `newCourseBuildFromRecord` (339-373).
- `requestFromRecord` (379-383), `outlineFromRecord` (391-422),
  `plannedExercisesPerChapter` (429-437).
- `courseID` (442-448), `index` (451-458), `next` (461-468), `missing` (471-479),
  `resolved` (482), `verifiedChapters` (485-493), `exerciseCount` (496-502),
  `measuredRows` (504-512), `record` (518-527), `sortReport` (530-539),
  `fullReport` (544-566), `verifiedCount` (568-576).
- `OutlineChapter.chapter` (1301-1317), `gateFacts` (1323-1336), `chapterIDs`
  (1340-1346). Wave-2 correction: these are `courseBuild` methods, not helpers.
- `courseHasWrittenChapter` (976-983). Wave-2 correction: a plan helper.

`course_prompt.go` (prompt text only):
- `courseOutlinePrompt` (597-601), `courseAgentSystemPrompt` (649-653),
  `goalOutlineMarker` and `goalOutlineEnd` (659-662), `courseAgentPrompt`
  (668-679).

`schemas.go` (new; one home for all wire schemas):
- `courseOutlineSchema` (607-645).
- `courseChapterSchemaFormat` and `courseChapterSchema` (from
  `course.go:266`, `:322`).
- `finishCourseSchema` (from `course.go:327`).
- `challengeSchema` (from `fake.go:362`). This one matters: the live challenge
  path (`pipeline.go:417`, `:453`) reads a schema defined in the fake provider
  file today.
- the inline `run_sql` tool schema (from `course_agent_tools.go:351`), named
  `runSQLSchema`.

Keep in `course_agent.go` (the run, the tools, the storage): the package doc,
the tool-name consts (73-76) and the bounds consts (80-92) because
`course_agent_tools.go` reads both, and `generateCourse`, `outlinePhase`,
`awaitApproval`, `BuildCourse`, `buildCourseRecorded`, `settleStoppedBuild`,
`RecoverInterruptedBuilds`, `courseBuildRefusal`, `saveChapterTool`, `publish`,
`chapterRows`, `chapterRefused`, `finishCourseTool`, `finalizeCourse`,
`courseRefused`, the exercise helpers (1352-1431), `storeBuilding`,
`buildingChapters`, `toolResult` and `storedChapterTurns` (690-717).

The shadow-state `Progress` collapse, included in this item:
- Add `build *courseBuild` to `pipeline` (`pipeline.go:89`). It is nil for a
  challenge run.
- Delete the pipeline fields `planned`, `verified`, `currentChapter`, `chapters`
  (`pipeline.go:113-125`). Each is a copy of a `courseBuild` fact.
- Derive them in `notify()` (`pipeline.go:241-274`): `Planned` is
  `len(build.outline.Chapters)`, `Verified` is `build.verifiedCount()`,
  `CurrentChapter` is `build.current`, `CourseID` is `build.plan.ID`, `Chapters`
  is `build.report`. The wire shape of `Progress` does not change.
- Add `current string` to `courseBuild`. `setChapter` sets it and notifies.
- `publish(build)` (`course_agent.go:1109`) becomes `p.notify()`. The
  store-before-publish order inside `saveChapterTool` stays.
- After a row delete (`settleStoppedBuild` with no verified chapter,
  `courseRefused`), set `p.build = nil` so `CourseID` stops naming a dead row.
- `outlinePhase:801-802` and `buildCourseRecorded:886-887` set `p.build = build`
  instead of the two counters.

Wave 2 called this a `Progress` contract change needing sign-off. Signature
given: the JSON is identical, only the internal source moves to the one truth.
Delete the dead `setVerified` (`pipeline.go:194`) in the same edit.

Two wave-2 actions I deliberately leave out of item 2. The `requestFromRecord`
nil-`Request` fallback (`course_agent.go:379-386`) needs the store column made
`NOT NULL` and the legacy test deleted in the same change; that is a store
migration, so file it separately. The `maxItems: 8` literal stays, as wave 2
concluded.

### 3b. Item 3, `BuildingScreen` reducer

File today: `web/src/components/screens/BuildingScreen.tsx`, 838 lines, with a
537-line effect at 156-678. The wave-2 tag list holds with these corrections:
the initial phase is `queued`; `settling` and `finishing` are the local
functions `settle`/`finish`, so they are events, not phases; the run also
adopts a stored course mid-poll (`:474`, `:490`); and `watchJob` has two failure
exits (`misses >= 3` at `:426`, deadline at `:497`) that are not the settle
failure.

New files: `web/src/components/screens/useGenerationRun.ts` (reducer and hook)
and `web/src/lib/generationPoll.ts` (the pure poll).

State, one object with a `phase` tag:
```
type Phase =
  | 'queued' | 'pacing' | 'submitting' | 'generating' | 'watching'
  | 'awaiting' | 'approving' | 'discarding'
  | 'verifying' | 'drafting' | 'finishing' | 'failed';
interface RunState {
  phase: Phase;
  log: LogLine[];
  steps: StepState[];
  built: Course | null;
  report: DraftReport | null;
  generated: AiOutcome | null;
  job: AiJob | null;
  recordStatus: string | null;
  awaiting: AiOutcome | null;
  failure: Failure | null;
}
```

Actions, each one transition:
- `start`: `queued` to `pacing` on a fresh run, straight to the submit branch
  when resuming.
- `paced`: one log line from the pacing.
- `stageReport{stages}`: update `steps` and append the stage lines.
- `jobView{view, lines}`: append the poll lines, set `job` and the chapter
  facts.
- `adopt{course, status}`: a stored course read mid-poll.
- `submitted{handle}`: `submitting` to `watching` when a handle arrives, to
  `generating` when it does not.
- `settled{outcome}`: `watching` or `generating` to `awaiting`, `finishing` or
  `failed`, chosen by one pure `settlePhase(outcome)`.
- `approving`/`discarding` and `discarded`.
- `authored{phase}`: `verifying` then `drafting` for the no-provider path.
- `failed{failure}`, `pollMissed{failure}`, `pollTimedOut{failure}`.
- `finish`, `reset`.

Effect boundaries:
- The reducer is pure. No timers, no network, no `performance.now()`.
- One entry effect keyed on `runKey` owns the abort controller, the pacing
  (`runPipeline` and `sleep`), `submitGeneration`, `generateCourse`,
  `generationJob` and `getCourse`, and dispatches the actions.
- `approve(courseId)` and `discard(courseId)` are functions the hook returns.
  They dispatch `approving`/`discarding`, call `approveCourse`/`deleteCourse`,
  and dispatch the result. This removes `approveRef` and `discardRef`.
- `poll(view, seen)` in `generationPoll.ts` is pure: it returns the new
  `{lines, seen}` for a job view. Test it without a server.
- `settlePhase(outcome)` is pure and decides `awaiting` vs `finishing` vs
  `failed`, so the branch at `:262-330` becomes data.
- The 700 ms navigate timer stays outside the reducer.

Two coupled cleanups in the same item. The log becomes `LogLine[]` with
`{t, tone, glyph, text}` instead of `{t, html}`; `GenerationLog`
(`web/src/components/ui/GenerationLog.tsx`) renders it as JSX, and the hand
`esc` and `dangerouslySetInnerHTML` path dies in both `BuildingScreen` and
`ChallengeLabScreen`. `ChallengeLabScreen.tsx:493-497` shares the same shape, so
update both call sites in one edit. `web-view.md`'s `measured` re-derivation is
already fixed (`BuildingScreen.tsx:685-686` derives from `report.totals`), so
leave it.

## 4. Autonomy rules

1. One item per run. Start only an item the tracker marks `[ ]`; never start
   item N+1 before the gate for N is green.
2. Split an item when it cannot clear one gate cycle, or when one diff touches
   two of {`backend/`, `web/`, `contracts/`}. Each part gets its own tracker line
   and its own `make test`.
3. Done means: the change is in the working tree, `make test` and `make verify`
   are green, the evidence line names the file and symbol, and no new lint error
   or warning appears. "Code written" is not done.
4. Stop and report when a gate fails twice on the same cause, when the item
   needs an owner call not answered here, or when the run cannot name the file
   that proves the change. Report the exact command and its first error line.
5. No overlap: a run claims an item by writing `[~]` plus its session id and
   start time in the tracker, and releases it on the gate. A second run skips
   any `[~]` younger than 40 minutes, and reclaims an older one with a note.

## 5. Risks

1. Working the stale tracker. Items 1, 8 and 9 are already done, and wave 2
   landed the web deletions (`esc`, `sleep`, `GenerationLog`, `useChecklist`
   already exist). Tripwire: before editing, `grep` the symbol; if it has no
   caller or is already deleted, mark the item done and move on.
2. Refactor churn that never gates. The large splits (items 2, 3, 15, 16) can
   leave the tree half-moved past a run boundary. Tripwire: if `make test` is
   not green when a run ends, revert to the last green state and split smaller.
3. Building the bigger product version. Every product call here has an honest
   minimal answer (items 7, 8, 9, 10, 20). Tripwire: if a change adds a route,
   a screen or a store beyond the named file, stop and file it as a new item.
