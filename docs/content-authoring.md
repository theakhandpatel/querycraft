# Authoring course content

Course reading material is **markdown**, and the database is the source of truth at
runtime. There is no CMS and no lesson text in application code.

## Where things live

| Piece | Path | Role |
| --- | --- | --- |
| Lesson markdown | `web/src/data/content/<module-id>.md` | The authored prose. One file per curriculum module. |
| Seeder | `web/src/api/content.ts` | Splits each file into sections and inserts them into the app database. |
| Table | `content_docs` | `(course_id, module_id, ordinal, section, markdown)`. Created in `web/src/api/pglite.ts`. |
| Reader | `web/src/api/pglite.ts` → `fetchModuleContent()` | `SELECT section, markdown FROM content_docs WHERE module_id = $1 ORDER BY ordinal`. |
| Curriculum | `web/src/data/curriculum.ts` | Module ids, titles, objectives, runnable examples and graded exercises. |

The markdown bundle is only the seeder input. The lesson page reads from
`content_docs`, so seeding happens once on first database creation and a reload
does not re-read the bundle.

## File naming = module id

`m1.md` seeds module `m1`, `j3.md` seeds `j3`, `q2.md` seeds `q2`. The file name is
the module id, and it must match an id in `curriculum.ts`. The course is derived
from the first letter (`m` → `sql-analytics`, `j` → `joins`, `q` → `subqueries`).

## Structure

```markdown
# Module title            <- dropped; the curriculum supplies the heading

Overview paragraph.       <- text before the first `##` becomes the "Overview" section

## First section           <- every top-level section is `##`
Prose...

## Check yourself
**1. Question?**

<details>
<summary>Answer</summary>

Answer prose.

</details>
```

- Sections split on `## `. Do not use `###` as a top-level heading.
- Prose sections become the reading column; the curriculum's example and exercise
  blocks are merged in after them and keep their own order.
- Run one per module id per course; `ordinal` preserves the order on read.

## Allowed content

- Markdown: paragraphs, lists, tables, blockquotes, inline code, fenced code.
- ```sql fences — rendered as code, not run.
- ```mermaid fences — rendered to SVG in the browser (`securityLevel: 'strict'`).
- Inline HTML limited to `<details>`, `<summary>` and
  `<span class="kbd-inline">…</span>`; everything is sanitised by DOMPurify.

## Adding a module

1. Add the module to `curriculum.ts` (id, title, objectives, blocks, and a graded
   exercise with `solution`, `contract` and `checks`).
2. Add `web/src/data/content/<id>.md`.
3. If it belongs to a new course, register the course in `COURSES`, map the id
   prefix in `content.ts` `courseForModule()`, and add the module list to the
   content preload in `web/src/state/store.tsx`.

The seeding loop is data-driven, so no other wiring is needed.

## Exercise versions

An exercise may carry `version` (default `1`). Bump it whenever the prompt,
contract or reference solution changes, and bump the dataset version when the
schema or seed changes. Every recorded attempt stores both, so an earlier pass
reads as **"solved on an earlier version"** in the Progress history instead of
being silently re-graded (technical design §7.3).

## Fixtures first

Grading rules are shared by the JS evaluator (`web/src/api/compare.ts`) and the
Go evaluator (`backend/internal/eval/compare.go`). Add a rule as cases in
`contracts/fixtures/` first — one JSON file per case with
`{ contract, actual_result, expected_outcome }` — then implement it so both
languages pass the same files (`make audit-fixtures`, `make test`). See
`contracts/fixtures/README.md`.
