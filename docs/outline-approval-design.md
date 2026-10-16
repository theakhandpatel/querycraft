# Design: outline approval (pause after the outline)

Status: **built** (2026-10-16). Chosen scope (owner, 2026-10-15): the *cheap
subset*: the run pauses with the outline stored and waits for the learner to
approve it or discard it. **No** edit-and-regenerate loop in v1; a fresh outline
is a fresh run.

## What was built (and where it differs from the design above)

- `StatusAwaitingApproval` the outcome status; it is returned by
  `POST /courses/generate` when the request sets `require_approval: true`, and it
  is the status the run's transcript records. The pause is opt-in: a request
  without the flag is one run, outline into chapters, exactly as before.
- `POST /courses/{id}/build` continues the stored outline. It also accepts the
  same `Prefer: respond-async` ask as `/courses/generate`, so the approval is a
  job the screen can watch; the blocking envelope is the default. A course that
  is not `building` is refused with `409`, not silently re-run.
- `CourseRecord.Request` persists the generation request (the one data change).
  When the field is absent on a record stored before it existed, the resume
  recovers the goal from the plan's title and the exercise budget from the
  plan's own exercise count, and leaves the rest at their defaults rather than
  inventing words the learner never said.
- The stored row's `status` stays `building`; `awaiting_approval` is the *run's*
  status. `active` remains the only status that puts a course in front of
  learners.
- `discarded` is a job-view status, not an outcome one: `DELETE /courses/{id}`
  (the discard) ends the paused job so a poller is told why the run it watched
  will never continue. The run's transcript still says `awaiting_approval`,
  because that is where the run itself stopped.
- The one deliberate addition to the contract: `contracts/ai.schema.json`
  (`outcome.status`, `generationRequest.require_approval`) and
  `contracts/run.schema.json` (`runStatus`). The enums genuinely had to grow: 
  a response or transcript carrying the new status must validate.

## Why

Today one job runs outline → chapters to completion in a single request. The M8
agent already asks for the outline first and stores it as a `building` course
before writing a single chapter (`course_agent.go`, `CourseOutline` →
`storeBuilding`), so the *object* the learner would approve already exists and is
already readable through `GET /courses/{id}`. What is missing is the pause: as
soon as the outline validates, the same run barrels on into the expensive
chapter-generation phase.

That costs the learner in one specific way: **a bad outline wastes the whole
run.** The outline gate (`validateCourseOutline`) exists precisely because an
outline can be wrong; today the only recovery is to throw the run away and start
again. Showing the index first turns that into a cheap "no, not that", and the
lesson from M8 is that a full run is minutes, while an outline is seconds.

## The shape

One job, two phases, with a wait between them.

```
submit course run
      │
      ▼
  outline phase ── validate ── store as `building` ──► AWAIT APPROVAL
      │                                                      │
      │                                    ┌─────────────────┴─────────────────┐
      │                                    ▼                                   ▼
      │                              approve (build)                     discard
      │                                    │                                   │
      ▼                                    ▼                                   ▼
  (outline fails validation)        chapter phase                     delete the row
  run ends `rejected`               → `active`                        run ends `discarded`
```

### What the learner sees

The existing `BuildingScreen` already draws the stored `building` course
(`buildingChapters()` fills Title/Summary/Objectives from the outline before any
chapter is written) and already logs "the outline plans N chapters". The pause
turns that read-only view into a decision point: the index is on screen, with
**Build it** and **Discard** instead of an automatic transition into chapter
generation.

## Decisions

- **A separate outcome status, not a new terminal one.** Add
  `StatusAwaitingApproval` ("awaiting_approval") to the outcome status set. It is
  a *non-terminal* stop: the job ends, but the stored course is a live thing a
  later request continues. `active` stays the only status that puts a course in
  front of learners (unchanged invariant).
- **The stored course IS the state.** No new table. The `building` row already
  carries the outline in `plan.chapters` (titles, summaries, objectives, planned
  exercise counts) and its id is what the learner approves. This is the property
  that makes the pause restart-robust: the sandbox can reboot and the outline is
  still in Postgres, unlike the current in-memory live run.
- **Resume must persist the request.** `courseAgentPrompt(req, outline)` is built
  from the original `GenerationRequest` (goal, scenario, difficulty, topics,
  exercise budget). `CourseRecord` stores the `CoursePlan` but **not** the
  request, so the build phase cannot reconstruct its own prompt from the row
  today. The record must carry the request (or the promptable fields of it) for
  resume to work. This is the one backend data change the feature needs.
- **Approve = run the chapter phase from a stored outline.** The build phase is
  the second half of `generateCourse` today: given an `outline` it calls
  `courseAgentPrompt` and drives `save_chapter` to completion. Approval reloads
  the stored outline, rebuilds the agent, and runs that half. It must resume or
  restart cleanly: a chapter already `verified` should not be re-asked for
  (`build.written[i].state == ChapterVerified` is already the skip condition).
- **Discard = delete the row.** `DELETE /courses/{id}` already exists and is
  owner-scoped. Discarding is that, and the run records `discarded` so the job
  view is honest about why it stopped.

## Surface

- `POST /courses/generate` gains a mode/flag: default stays today's behaviour
  (compatible), the pause is requested explicitly. A client that does not ask for
  approval must see exactly what it sees today.
- `POST /courses/{id}/build`, approve: continue the stored outline into the
  chapter phase. Owner-scoped; 404 for another account's course (existing rule).
- The job view's `status` can be `awaiting_approval`; the course id is already
  named as soon as the outline is stored, so the client can navigate to it.

## Out of scope for this slice

- **Edit-and-regenerate.** The learner cannot yet change the outline and get a
  new one from the same run; that is a fresh run. This is the deliberate cut.
- **Per-chapter approval.** Approving approves the whole index.
- Metadata → SQLite, any UI redesign, any change to grading.

## Test plan

- The outline phase stops with `awaiting_approval` and a stored `building` course
  whose `plan.chapters` carry the outline's titles
  (`TestTheOutlinePhaseStopsAwaitingApproval`).
- Approve runs the chapter phase and activates the course
  (`TestApprovingAPausedCourseRunsTheChapterPhase`); a chapter already verified
  before the pause is not re-asked for
  (`TestAnAlreadyVerifiedChapterIsNotAskedForAgain`): the stored chapter is
  replayed into the conversation as an already-resolved turn.
- Discard deletes the row and ends the run `discarded`
  (`TestAPausedCourseCanBeDiscarded`).
- Another account's course id is 404 on build
  (`TestApprovingAnotherAccountsCourseIs404`), exactly as on delete.
- Default (no approval requested) is byte-compatible with today: one run,
  outline → active (`TestTheDefaultCourseRunIsUnchanged`).
- Existing M8 tests stay green unchanged.
- The end-to-end path is also driven over the real routes:
  `TestAPausedCourseIsApprovedOverTheRealRoutes` submits, pauses, reads the
  index, approves, and reads the activated course back with its measured rows.
