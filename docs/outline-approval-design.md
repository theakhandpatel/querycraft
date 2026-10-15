# Design — outline approval (pause after the outline)

Status: **designed, not built.** Chosen scope (owner, 2026-10-15): the *cheap
subset* — the run pauses with the outline stored and waits for the learner to
approve it or discard it. **No** edit-and-regenerate loop in v1; a fresh outline
is a fresh run.

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
again. Showing the index first turns that into a cheap "no, not that" — and the
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
- `POST /courses/{id}/build` — approve: continue the stored outline into the
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
  whose `plan.chapters` carry the outline's titles.
- Approve runs the chapter phase and activates the course; a chapter already
  verified before the pause is not re-asked for.
- Discard deletes the row and ends the run `discarded`.
- Another account's course id is 404 on build, exactly as on delete.
- Default (no approval requested) is byte-compatible with today: one run,
  outline → active.
- Existing M8 tests stay green unchanged.
