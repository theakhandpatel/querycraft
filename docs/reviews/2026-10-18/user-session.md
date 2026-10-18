# Deep user session — 2026-10-18

Environment: the single binary (`backend/bin/querycraft`) started from a throwaway
config (addr 127.0.0.1:18088, embedded PostgreSQL 18.3, `ai_provider: fake`),
driven through the T3 preview browser (the desktop browser was not connected).
Every request below hit the real gateway, engine and stores.

## What worked, flow by flow

- **First boot**: SPA loads clean (no console errors), `/health` ok, fonts and
  assets 200; anonymous query on the shared read-only dataset answers.
- **Accounts**: signup (Sam Learner), reload keeps the session; admin login with
  the empty password works and swaps in the Manage nav; logout + localStorage
  clear returns to the auth screen.
- **Preloaded lessons**: chapter renders, runnable example ran (5 rows, 2.7 ms,
  `rq_g8b5eb`), wrong query refused honestly
  (`QUERY_SYNTAX_ERROR syntax error at or near "FRM"`), exercise graded
  (pass with contract checks, COLUMN_MISMATCH refusal otherwise), hint and
  reference reveal work, progress went 0 → 11%, notes and workbook persist
  across reload, "1 day streak" and counters update.
- **SQLite chapter**: `#/course/sqlite-essentials/s1` ran
  `SELECT type, name FROM sqlite_master …` on **SQLite 3.50.3** over HTTP.
- **Course generation (admin)**: goal → draft review (3 chapters, "3 of 3
  exercises executed and cross-checked") → "Add to my learning" → stored and
  enrolled (`course-4a0b98efc32ba538`), chapter 1 exercise passed with the
  reference's column names, course progress advanced to 33%.
- **Challenge lab**: full pipeline visible and green (validate → reference
  satisfies its contract, 20 rows → adversarial fixtures rejected → registered),
  "Practise this now" routes to the source chapter.
- **Workspace**: reset restored in 123 ms; the drawer's "Checking the execution
  environment…" is honest first-ask provisioning, not a stuck state.
- **Bad actor pass** (API): no token → 401; learner on `/admin/courses` → 403;
  learner on another owner's course → 404 (no existence probing); malformed AI
  body → `AI_INVALID_REQUEST`; six wrong logins → `401 401 401 401 401 429`
  (throttle holds); stacked statements (`DROP TABLE`) → refused by the prepared
  statement protocol with `QUERY_SYNTAX_ERROR`.

## Findings

### Fixed in this session

1. **Exercise prompts were never rendered.** `SqlCell` used `block.prompt` only
   as the AI-explain objective; the learner saw "Exercise 1.1 · contract · 3
   columns" and an editor with no task. Now rendered above the editor
   (`SqlCell.tsx`, `.cellprompt` in `querycraft.css`).
2. **Authored content drifted from the seed** (`m1.md` + `curriculum.ts`):
   the lesson sample claimed `3 | Chidi Okafor | NULL` and "Rows 3 and 4 show
   NULL" while the seed has `city = Lagos` for id 3 (the NULL is `email`), and
   Exercise 1.1's authored result listed `Lumen 27-inch 4K Monitor` instead of
   the seed's `Noise-Cancel Earmuffs`. The offline graders compare against the
   authored rows, so this mattered. Both now match the seed.
3. **Generated exercise prompts did not name their result-column aliases**
   while the grader compares names exactly; a correct query aliased `n` was
   refused (`COLUMN_MISMATCH`). The save-chapter tool description and the
   chapter prompt schema now require naming each result column exactly, and the
   built-in provider's four canned prompts now do.

### Open (need a decision or a feature)

4. **Chapter/section edits on a stored course are in-memory only.**
   `saveLessonEdits`/`saveCourseEdits` mutate the fetched `MODULES`/`COURSES`
   copy, toast "Chapter updated.", and revert on reload; there is no course-edit
   route (`GET`/`DELETE`/`build` only). Either add `PUT /courses/{id}` for
   authored fields or make the edit affordance honest about being draft-only.
5. **"Practise this now"** from a finished challenge lands on the source course
   chapter rather than a challenge-specific practice view; confirm intended.
6. Minor: the workspace drawer can show "Checking the execution environment…"
   for a few seconds on first ask while the per-account database provisions.

## Evidence

Screenshots and request ids were captured during the run; the browser tab
(`tab_1_1b4b9063-…`) remains open on the SQLite lesson for inspection. Gates
after the fixes: `make test` (23 audits) and `make verify` on the same tree.
