# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Self-directed learners who want to *write* SQL rather than watch it being written:
career switchers, analysts picking up the language properly, and developers who use
SQL occasionally and want the gaps closed. They arrive with a specific goal, work in
short sittings in a browser tab, and are practising on their own time without an
instructor or a cohort. The admin surface (`role === 'admin'`) is the same product's
authoring track, used by whoever is writing the curriculum, not by a separate
audience with separate needs.

## Product Purpose

Querycraft teaches SQL by having the learner write it against a real database and
grading what the query returns. Chapters combine authored reading with runnable SQL
cells; the learner types a statement, runs it, and checks it against an exercise's
expected result. Success is that the learner actually exercises and retains the
language in a real engine, not that a lesson was marked watched.

## Positioning

**Graded on the result, not the SQL text.** An exercise declares a result contract: 
the shape of the correct answer. Any formulation that produces it passes. The
product is therefore honest in a way a text-matching tutorial cannot be: it cannot
mark a correct query wrong for using a different join style, and it cannot pass a
query that returns the wrong rows. Running is real execution, not simulation: the
default path compiles PostgreSQL to WebAssembly and runs it in the browser tab.

## Operating Context

- Learning happens in a browser tab, usually with no server and no setup: the default
  execution adapter is PGlite (real PostgreSQL, WASM) in a Web Worker, with learner
  data in IndexedDB.
- The same SPA also runs against a Go gateway that owns a real PostgreSQL, either for
  a hosted deployment or for the HTTP adapter during development.
- A learner's working session is: pick a course, read a chapter, write and run SQL,
  get graded, keep notes and bookmarks, revisit from the workbook and progress screens.
- Destructive actions are confined to the learner's own workspace, which is what the
  Tables drawer describes and resets.
- AI assistance exists (challenge generation, critique) but is optional; the learning
  core must work without it.

## Capabilities and Constraints

Confirmed functionality in the shipped web app:

- **Courses and chapters.** Built-in curriculum courses (`sql-analytics`, `joins`,
  `subqueries`, `plans`, and the SQLite tracks) plus gateway-stored courses. A chapter
  (`module` in code and data) is a sequence of blocks: prose, syntax, runnable
  examples, and graded exercises.
- **Runnable SQL cells.** A cell holds an editor, a Run control, Check answer, Hint,
  Show solution, Keep query and Reset, plus a result grid and a feedback panel.
- **Dialect.** The product serves more than one engine. A chapter resolves to a
  dialect (PostgreSQL or SQLite), shown as a badge, and refuses honestly when the
  engine behind the seam cannot serve it.
- **Query plans.** Performance exercises capture `EXPLAIN`/`EXPLAIN ANALYZE` output
  and grade it structurally, shown in a plan viewer.
- **Workbook, progress, library, challenge lab, appearance.** Notes and bookmarks,
  a progress screen with real accrued study time, course browsing/enrolment, and an
  optional AI drill generator.
- **Authoring.** An admin-only flow creates a course through a pipeline (generate,
  review, apply), never applied without approval.

Terminology that is part of the product and must not be renamed: chapter, exercise,
result contract, workbook, challenge lab, workspace, dialect, plan.

Explicitly undecided / not in this build: accounts are local-first (a local workspace
by default); the product makes no claim about certification, grades, or employment.

## Brand Commitments

- The name is **Querycraft**. The shell's own copy is lowercase `querycraft` in the
  top bar wordmark.
- Voice: plain, precise, and non-infantilising. Errors name the problem and the
  recovery, and an execution failure is explicitly distinguished from a wrong answer.
- The visual identity is fixed and established: an "Ink & Pine" light palette and a
  "Pine on Slate" dark palette, Geist for prose, Space Grotesk for display, Geist Mono
  for data and metadata. See `DESIGN.md`.

## Evidence on Hand

- Chapter reading content is authored markdown in `web/src/data/content/*.md`
  (51 modules), seeded into the `content_docs` table.
- The dataset is a real commerce fixture, schema and seed in
  `backend/internal/dataset/{schema,seed}.sql`, shared by the Go evaluator and the
  browser app through Vite `?raw`.
- Grading rules are evidenced by `contracts/fixtures/*.json`, read by both the Go
  evaluator test and the JS fixture audit, so neither language keeps its own
  expectations.
- There are no customers, testimonials, benchmark claims, press, or pricing in this
  repository. Future work must not invent any.

## Product Principles

1. **The result is the truth.** Grading compares what the query returned. Any correct
   formulation passes; a plausible-looking query that returns the wrong rows does not.
2. **Run it for real, or say you cannot.** Execution goes through a real engine. When
   no engine can serve a request, the product reports `ENVIRONMENT_UNAVAILABLE` or an
   explicit refusal rather than inventing rows.
3. **Never confuse a broken query with a wrong answer.** Execution errors are reported
   as errors and are not recorded as failed attempts.
4. **One seam for all data.** Every screen reads through the `ExecutionService`
   boundary; screens do not fetch, and no screen fabricates a result.
5. **The learning core stands alone.** Notes, progress, exercises and grading work
   with AI switched off and with no server running.

## Accessibility & Inclusion

- The interface must be fully keyboard operable: the SQL editor, the result grid, and
  the tables drawer are all reachable and escapable without a pointer, with visible
  focus, and the shell carries a skip link.
- Status is announced, not just coloured: runs, verdicts and async work report through
  live regions, and metadata contrast must meet WCAG AA against the surface it sits on.
- Motion respects both `prefers-reduced-motion` and an in-product reduced-motion
  setting.
