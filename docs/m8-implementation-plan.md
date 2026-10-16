# M8: A course that builds itself, one chapter at a time

Status: in progress. M7 (real course generation) is complete and verified: a live
model generated a four-chapter course, every exercise's reference and cross-check
ran in a throwaway database, and the activated plan carried the engine's own rows.

## Why M8 exists

M7 asked the model for the whole course in **one** reply and blocked the request
until it arrived. A real run measured 147 s, and the shape of that has three
problems a learner feels immediately:

1. **Nothing is real until the end.** The stage list the screen draws is a replay:
   every stage arrives in the same response, so a 147-second wait shows no
   progress, then eight lines appear at once.
2. **One failure discards everything.** The whole plan is re-asked after any
   rejected exercise, and a chapter that the model got wrong twice ends the run
   with no course at all: including the chapters that had already verified.
3. **Nothing is saved until the end.** Nothing is in the database while the run
   is working, so a closed tab, a proxy timeout or a dead gateway leaves the
   learner with nothing to look at.

## The design: one session, a tool per chapter

The model is asked to write **one chapter at a time through a tool call**, in a
single conversation, and the gateway verifies and stores each chapter before the
model writes the next one.

* `POST /courses/generate` accepts the request and returns a job (202).
* The gateway opens one conversation and asks for an **outline** first: title,
  subtitle, level, objectives, and the chapter list (id, title, summary,
  objectives, exercise count). Small answer, fast, and it is what the screen can
  draw while the chapters are still being written.
* The model then calls `save_chapter` once per chapter with the chapter's
  markdown sections and its exercises.
* The gateway runs that chapter's references, its cross-checks and its
  adversarial fixtures in the course's scratch environment, and persists the
  chapter **as soon as it passes**.
* The tool result carries the engine's own verdict back into the conversation:
  a row count, the failing exercise id, the evaluator's message. That is the
  point of doing this in one session: the model repairs against a real error
  instead of guessing, and it still remembers what it wrote in chapter 1 when it
  writes chapter 3.
* `finish_course` closes the course. The stored row moves from `building` to
  `active` and the plan becomes what the app already knows how to render.

## What is stored, and when

`app.courses` keeps one row per course with the plan as JSONB (the read contract
`GET /courses/{id}` does not change). M8 uses it progressively:

| Moment | Row state |
| --- | --- |
| Outline accepted | `building`, plan carries title/level/objectives and the planned chapters with no sections yet |
| Each `save_chapter` pass | `building`, that chapter's sections and exercises appended, with its measured rows |
| `finish_course` / all chapters verified | `active` |
| No chapter ever verified | the run is refused and nothing is activated |

A learner who reloads mid-run sees the chapters that already passed. A run that
dies leaves a `building` course with the chapters it managed, instead of nothing.

## Bounds

* One chapter, one provider call, with a bounded number of in-session repairs
  after a failed `save_chapter`.
* A chapter that cannot be repaired is dropped with its failure reported, and the
  course finishes with what did verify: a smaller honest course, never a course
  with an unverified exercise in it.
* The run budget still bounds the whole conversation; the per-call budget still
  bounds one provider call.
* Nothing in a graded position reads a timing field.

## The audit trail

Every generation run now persists its transcript, every provider message,
tool call and tool result, in order, to `app.runs` (`internal/ai/transcript.go`).
The seam is the provider call, so both the challenge pipeline and the M8 course
agent are covered: `complete`/`completeRaw` record a single-shot exchange, and
the course agent records its conversation as it assembles it, because it owns
the messages and each tool result is the engine's own verdict. The record is
owner-scoped (the session stamps it, the same wrapper that stamps a stored
course), redacted (a credential never reaches the table) and bounded
(`ai.MaxTranscriptEntries` / `ai.MaxTranscriptBytes`, with a stored transcript
saying when it is a prefix of the run). It is read back over `GET /runs` and
`GET /runs/{id}`; another account's run is 404, never 403.

## Verification

* `make audit-course-gen` drives the streaming path against a freshly booted
  gateway with the deterministic provider.
* `make audit-ai-live` drives it against the real model, and re-grades the stored
  course's first exercise independently through `POST /query`.
* A poll mid-run must observe a chapter verified and stored while a later chapter
  is still being written; a run that is killed must leave the verified chapters
  readable.
* The frontend keeps reading the course from the gateway: a reload, a deep link
  and a second device all show the same stored course.

## What landed

Status: complete and verified. The loop replaced the single whole-course call.

* `backend/internal/ai/course_agent.go` holds the agent: one outline call on the
  strict `json_schema` path, then one conversation whose tools are
  `save_chapter` and `finish_course`. Bounds: `maxChapterWrites = 3` (one write
  and two in-session repairs per chapter), `maxAgentRounds = 40`, and every tool
  result is clipped to `maxToolResultChars` on a rune boundary.
* The provider seam grew the multi-turn pieces: `Messages` (role, content,
  `tool_call_id`, `tool_calls`) and `Tools` on the request; `ToolCalls` and
  `FinishReason` on the reply. `HTTPProvider` marshals `tools`/`tool_choice` and
  parses `message.tool_calls`; it never sends `response_format` together with
  `tools`, and the byte cap, redaction and timeout behaviour are unchanged.
  `http_provider_test.go` pins a tool-call reply, a plain reply, a malformed
  tool call, and the absence of `response_format` on a tool-calling request,
  against a stub `httptest` server.
* Chapters are gated by the same rules M7 used: `validateCourseChapter` (the
  M7 whole-plan validator, extracted rather than forked) and then
  `verifyChapterInEnv`, which runs the reference, the cross-check and the
  adversarial fixtures in the course's one scratch environment. A refused
  chapter's tool result carries the failing exercise id, the stage and the
  evaluator's own message.
* Persistence is progressive. The outline is stored as a `building` course
  (`StatusBuilding`); every accepted chapter is re-stored; `finish_course`
  activates the course, which holds exactly the chapters that verified. A
  chapter that cannot be repaired is dropped and reported, and a run in which
  nothing verified deletes the outline row and is refused: nothing activates a
  course containing an unverified exercise.
* The job view carries the live progress a poller needs: `chapters_planned`,
  `chapters_verified`, `current_chapter` (the chapter the gateway most recently
  started verifying) and `chapters`, the per-chapter report in outline order.
  The blocking routes are unchanged, and a terminal view only names a record
  when one exists: a refusal clears it.

Two defects the tests found, both fixed: a chapter that had been written and
refused reported as `skipped` rather than `refused`, and a refused job kept
naming the `building` row its own refusal had just deleted.

Evidence:

* `make audit-course-gen`: five runs. The deterministic provider activates a
  course chapter by chapter; a stub provider that pauses between turns shows
  the record route answering with a `building` course whose content is exactly
  the chapters that verified while later chapters are still being written; a
  second stub (one chapter's cross-check cannot agree) shows the refusal
  reaching the model, the bounded repair loop, the chapter being dropped and
  the rest of the course activating; a third stub (every chapter disagrees)
  shows the refusal and the absence of any row. Both negations are also run as
  Go unit tests, with `-count=1`.
* `backend/internal/ai/course_agent_test.go` covers the outline gate, the
  repair from the engine's message, the mid-run read, the drop, the refusal,
  the run budget and malformed arguments.
* `backend/internal/httpapi/jobs_test.go` covers the streaming read through the
  route (`GET /courses/{id}` mid-run) and the refusal leaving nothing behind,
  with a deliberately paused provider.
