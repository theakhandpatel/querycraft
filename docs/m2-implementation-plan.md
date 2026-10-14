# M2 implementation plan — "Complete workbook (browser)"

Design source: `docs/technical-design.md` §14 (M2), §8 (persistence), §9 (content),
§10 (UI), §13.2 (test matrix). Steps are ordered so each one ships on its own,
leaves `make verify` green, and can be reviewed without waiting for the next.

## Status

Steps 1–9 have shipped. Each row names the command that proves it; every audit in
the last column runs under `make test`.

| # | Step | Outcome | Proof |
| --- | --- | --- | --- |
| 0 | Release 0.1.0 | **Not started** — same commit-and-tag act as step 10, awaiting the user's go-ahead | `git describe --tags` |
| 1 | Lesson completeness | Done — every published lesson has objectives, an example and an exercise or a reading-only reason | `make audit-lessons` → 23 lessons, 24 examples, 24 graded exercises, 0 problems |
| 2 | Store layout (§8.1) | Done — the eight stores, one snapshot splitting without loss, failed writes typed | `make audit-storage` → 8 stores, 0 problems |
| 3 | Bookmarks | Done — create and remove, survive reload, appear in the export | `make audit-storage`, `make audit-export` |
| 4 | Saved queries | Done — keep a query from a cell; listed in the workbook and carried by the export | `make audit-storage`, `make audit-export` |
| 5 | Search | Done — one field across lessons, exercises and the workbook | `make audit-search` → 0 problems |
| 6 | Progress views | Done — per-exercise history and the stale-version view | `make audit-progress` |
| 7 | Import | Done — validated import against the schema, loss-free round trip | `make audit-import`, `make audit-export` |
| 8 | Curriculum completion | Done — new course `foundations` (`f1`–`f7`) and module `m9`; 44 fixtures | `make audit-courses` → 24 exercises, 0 broken; `make audit-content` |
| 9 | Accessibility pass | Done — keyboard-only lesson walkthrough passed; it found two defects, fixed below | `docs/accessibility.md` |
| 10 | M2 release | **Not started** — awaiting the user's go-ahead to commit/tag | `make verify` |

Steps 0 and 10 are the two halves of one act — a commit plus a tag — and neither
has been taken yet.

## What M2 is

> Scope: full curriculum through window functions, notes, bookmarks, saved
> queries, search, progress views, import, accessibility pass.
>
> Exit gate:
> - Every published lesson has objectives, an example and an exercise or a reading-only reason.
> - Export and import round trip without loss.
> - Keyboard-only use of a full lesson works.

## Where we are today (measured, not assumed)

| M2 item | State |
| --- | --- |
| Objectives per lesson | Done — all 15 modules carry 3 objectives |
| Example per lesson | **Missing in `m6`, `m7`, `m8`** (1 example elsewhere) |
| Exercise per lesson | Done — 16 graded exercises, `make audit-courses` 16/16 |
| Curriculum order to window functions | Partial — no recursive-CTE lesson; `HAVING`/`OFFSET` mentioned once each |
| Notes | Done (lesson → `addNote`) |
| Bookmarks | **Model + renderer exist, no way to create one** |
| Saved queries | **Rendered in the workbook, nothing writes to them** |
| Search | **None** (`uiState.libraryFilter` only filters the course list) |
| Progress views | One screen: weekly bars, first-try rate, recent attempts |
| Import | **None** — export only |
| Accessibility | Partial: labels and focus styles exist; never tested keyboard-only |
| IndexedDB stores (§8.1 names `attempts`/`evaluations`/`notes`/`bookmarks`/`saved_queries`/`drafts`/`meta`) | Today: `progress` + `events`, drafts in `localStorage` |

## Sequencing

| # | Step | Ships | Depends on |
| --- | --- | --- | --- |
| 0 | Release 0.1.0 (commit + tag) | tag `v0.1.0` | — |
| 1 | Lesson-completeness audit + the 3 missing examples | `make audit-lessons` green | 0 |
| 2 | Offline store layout matches §8.1 | `make audit-storage` green | 0 |
| 3 | Bookmarks | create/remove, survive reload | 1 |
| 4 | Saved queries ("keep this query") | workbook section + export | 3 |
| 5 | Search | one field across lessons/exercises/workbook | 4 |
| 6 | Progress views | per-exercise history, stale-version view | 2 |
| 7 | Import + round-trip fixture | `make audit-import` green | 6 |
| 8 | Curriculum completion | new modules, fixture-first | 1, 7 |
| 9 | Accessibility pass | keyboard-only lesson walkthrough | 5, 6 |
| 10 | M2 release | `make verify` + tag `v0.2.0` | all |

Steps 1–2 are content/persistence hygiene: no new UI, low risk, and they unlock the rest.
Steps 3–6 are the workbook itself. Step 7 is the second exit gate. Step 8 is the
largest content block and is deliberately last so the UI can be complete first.

---

## Step 0 — Release 0.1.0

1. `git add -A` (162 files; `.gitignore` already excludes `node_modules/`, `dist/`,
   `.data/`, `backend/bin/`, `.worktrees/`).
2. Commit as the initial import of M1, tag `v0.1.0`.
3. Pre-flight already done: no pasted credentials in the tree, largest file is
   `web/package-lock.json` at 115 KB, no binaries.

**Done when:** `git describe --tags` says `v0.1.0` and the working tree is clean.

## Step 1 — Lesson completeness, enforced

The exit gate is a property of the content, so a script decides it, not a reviewer.

1. Add `scripts/lesson-audit.mjs` bundling `src/data/curriculum.ts` (same pattern as
   `scripts/course-audit.mjs`) and asserting, per module: ≥1 objective, ≥1 example
   block, and either a graded exercise or `readingOnly: true` with a stated reason.
2. Add `readingOnly?: string` to `CourseModule` and render the reason in the lesson
   outline, so a reading-only lesson is a deliberate, reviewable choice.
3. Write the missing runnable examples for `m6`, `m7`, `m8` (a cohort query, an
   `EXPLAIN` plan read, and a capstone review query), each with a one-line note.
4. Makefile: `audit-lessons` target, wired into `test`.

**Files:** `scripts/lesson-audit.mjs`, `web/src/types.ts`,
`web/src/data/curriculum.ts`, `web/src/components/screens/LessonScreen.tsx`, `Makefile`.
**Done when:** `make audit-lessons` reports 0 failures and the three new examples run
on the seed data.

## Step 2 — Offline store layout (§8.1)

Today the offline adapter writes one `progress` blob plus an `events` log, and drafts
in `localStorage`. §8.1 names the stores it wants; the doc is the arbiter, and this is
greenfield, so align the code instead of the doc.

1. `web/src/api/idb.ts`: version 2 with `attempts`, `evaluations`, `progress`,
   `notes`, `bookmarks`, `saved_queries`, `drafts`, `meta`.
2. Every write goes through a transaction; a failed write keeps the draft and raises
   `PERSISTENCE_UNAVAILABLE` (already wired to a toast in the store).
3. `meta` holds content versions and the export format version, so step 7 can reject
   a future document without guessing.
4. `web/src/lib/progress.ts` keeps its shape (one snapshot in memory for the UI); the
   adapter is what splits it across stores.

**Files:** `web/src/api/idb.ts`, `web/src/api/execution.ts`, `scripts/storage-audit.mjs`.
**Done when:** reload keeps attempts, notes, saved queries and drafts; `make audit-storage`
proves a failed write is reported rather than swallowed.

## Step 3 — Bookmarks

The model (`kind: 'bookmark'`) and the workbook renderer already exist; only the action
is missing.

1. A "Bookmark this section" control on the lesson outline and on a bookmarked exercise,
   with a visible on/off state.
2. Write through the store to `bookmarks`; record a `bookmark` event so the history
   shows it (event kinds already carry `hint`/`solution`).
3. Workbook: remove/edit a bookmark; deep-link back to the section it points at.

**Done when:** bookmarking survives a reload in both adapters and appears in the export.

## Step 4 — Saved queries

Workbook already renders `saved`; nothing writes to it.

1. "Keep this query" on a successful result and on a revealed reference solution,
   stored with the SQL, the exercise id, the contract snapshot and versions.
2. Dedupe on `(course, exercise, sql)`; cap per course with a clear message.
3. Workbook: run a saved query again in place (through the seam), copy, delete.

**Done when:** a kept query survives reload, appears in the export, and re-runs to the
same result.

## Step 5 — Search

1. `web/src/lib/search.ts`: a pure, tested index over lesson titles, objectives, prose
   headings, exercise prompts, notes, bookmarks and saved queries; ranked, no fuzzy
   guessing, empty query returns nothing.
2. One field in the shell (keyboard reachable, `Cmd/Ctrl+K`), grouped results, and a
   zero-result state that says what was searched.
3. Result click navigates to the lesson/section; search stays client-side in M2.

**Files:** `web/src/lib/search.ts`, `scripts/search-audit.mjs`, shell + a results panel.
**Done when:** `make audit-search` covers ranking, empty input and a no-match query.

## Step 6 — Progress views

1. Per-exercise history: every attempt for one exercise, newest first, with version
   stamps and the "solved on an earlier version" label already implemented in §7.3.
2. Per-topic mastery: passed/total per module, not just per course.
3. Attempts and evaluations are read from their own stores (step 2), so the view is a
   read of history rather than a re-derivation.

**Done when:** the view matches the export for the same account, and no number on the
screen is inferred from anything but stored records.

## Step 7 — Import, and the round trip

1. `contracts/export.schema.json` describing the §8.3 document; the existing
   `scripts/export-audit.mjs` validates every export against it.
2. `web/src/lib/importBundle.ts`: validate, then write. Refuse an unknown
   `format_version`, refuse a malformed document, and report the count imported.
3. Conflict policy, stated in the UI before writing: merge by `client_request_id`/
   item id, never duplicate an attempt, never delete an existing note.
4. Import/Export controls live together in Appearance and in the workbook.
5. `scripts/import-audit.mjs`: export → import into an empty store → export again →
   the two documents are equal apart from `exported_at`. Signed-out and
   gateway modes both covered.

**Done when:** `make audit-import` passes and the round trip is lossless for a
snapshot containing attempts, evaluations, notes, bookmarks, saved queries and drafts.

## Step 8 — Curriculum completion through window functions

The order in §9.3 is the checklist; today the gap is the foundations and recursive CTEs.

1. A `foundations` course covering the order's early items as separate lessons:
   DISTINCT, aliases and projection; WHERE, comparison, AND/OR/NOT and precedence;
   ORDER BY/LIMIT/OFFSET; NULL handling; IN/BETWEEN/LIKE; CASE; aggregation with
   HAVING. (`HAVING` and `OFFSET` are currently mentioned once each.)
2. Recursive CTEs as its own lesson, with an example and an exercise.
3. Fixtures first for every new exercise's wrong-query and alternative-formulation
   cases (§7.2), then the reference solution must pass `make audit-courses`.
4. Each new lesson: objectives, a runnable example, a graded exercise, and markdown in
   `web/src/data/content/<id>.md` with at least one mermaid diagram where a diagram
   carries the idea.

**Done when:** `make audit-lessons`, `make audit-courses` and `make audit-fixtures`
are green with the new modules included, and §9.3's list has no uncovered item.

## Step 9 — Accessibility pass

1. Keyboard-only walkthrough of one full lesson: reach every control, run a query with
   `Ctrl/Cmd+Enter`, submit, read the result and feedback, open the Tables drawer, and
   close it — using no pointer.
2. Fix what that finds: drawer focus trap and restore, result grid as a labelled table,
   live region for run/feedback status, visible focus on every interactive element,
   skip link to the reading column.
3. `docs/accessibility.md`: what is verified how, and what is not verified honestly
   (no automated a11y runner is installed, so the walkthrough is the evidence).

**Done when:** the walkthrough is recorded in the doc with the exact steps and the app
passes it.

### Step 9 evidence

The walkthrough ran keyboard-only on the `pglite` adapter and found two defects, both
now fixed:

1. **The Tables drawer's focus trap could not see the tables.** In
   `web/src/components/Schema.tsx` the drawers' tables were plain scrollable `<div>`s
   with no role or label, so `TablesDrawer` had nothing focusable inside them and
   `Tab` escaped to the document body. The tables now get the same treatment as
   `ResultGrid.tsx`: `role="region"` with an `aria-label` on the scroll container, a
   `visually-hidden` `<caption>` and `<th scope="col">` headers. Two bugs in the
   trap's own helpers were fixed with it — the `offsetParent` visibility test is
   `null` for anything inside a fixed-position panel, and the `[href]` selector also
   matched the SVG `<use href>` inside icons — so `TablesDrawer.tsx` now scopes to
   `a[href]` and filters with a `getClientRects()`-based `focusable()` helper.
2. **The query editor had no visible focus.** `.editor{box-shadow:none}` in
   `web/src/styles/querycraft.css` removed the browser ring and no wrapper rule
   replaced it. Added `.editorwrap:focus-within{box-shadow:inset 0 0 0 2px var(--accent)}`.

## Step 10 — M2 release

1. `make verify` + every `audit-*` target green, both adapters.
2. README/M2 docs updated; `docs/export-format.md` gains the import contract.
3. Commit and tag `v0.2.0`.

**M2 exit gate, mapped:** lesson completeness → step 1; export/import round trip →
step 7; keyboard-only lesson → step 9.
