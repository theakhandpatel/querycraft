# Authoring course content

A chapter has two authored parts: reading prose in markdown and the exercises in
the curriculum. Both are data. No lesson text lives in application code.

## Where things live

| Piece | Path | Role |
| --- | --- | --- |
| Lesson markdown | `web/src/data/content/<module-id>.md` | The authored prose. One file per module. |
| Curriculum | `web/src/data/courses/<id>.ts` | Module ids, titles, objectives, examples, and graded exercises. `web/src/data/curriculum.ts` assembles the catalog. |
| Seeder | `web/src/api/content.ts`, `web/src/lib/contentSeed.ts` | Splits each file into sections and keeps the browser database in step with the bundle. |
| Browser table | `app.content_docs` | `(course_id, module_id, ordinal, section, markdown)`. |
| Browser metadata | `app.content_meta` | The fingerprint of the bundle that produced the rows. |
| Reader, browser mode | `getContent()` in `web/src/api/execution/browser.ts` | Returns the bundled sections. Reading a chapter does not open PGlite. |
| Reader, gateway mode | `getContent()` in `web/src/api/execution/http.ts` | Returns the bundled sections. The gateway does not store chapter prose. |

The lesson page renders the bundled sections in both modes. PGlite still seeds
`app.content_docs` when a PostgreSQL workspace boots, so a learner can query the
app metadata, but opening a chapter never waits for that database. The seeder
compares the bundle's fingerprint with `app.content_meta` and rewrites the table
when they differ. A changed or added module appears without a manual migration.

The prose is authored once, in the markdown files. The gateway does not need a
second copy.

## File name is module id

`m1.md` seeds module `m1`, `j3.md` seeds `j3`, `q2.md` seeds `q2`. The file name
is the module id, and it must match an id in the course's module under
`web/src/data/courses/`.

The course comes from the first letter of the module id through the prefix map in
`web/src/lib/contentSeed.ts`:

| Prefix | Course |
| --- | --- |
| `m` | `sql-analytics` |
| `j` | `joins` |
| `q` | `subqueries` |
| `f` | `foundations` |
| `p` | `plans` |
| `s` | `sqlite-essentials` |
| `r` | `sqlite-in-production` |
| `x` | `postgres-to-sqlite` |

A prefix with no entry falls back to the default course and seeds the chapter
onto the wrong course. Add the entry when you add a course.

## Structure

```markdown
# Module title            <- dropped; the curriculum supplies the heading

Overview paragraph.       <- text before the first ## becomes the Overview section

## First section           <- every top-level section is ##
Prose...

## Check yourself
**1. Question?**

<details>
<summary>Answer</summary>

Answer prose.

</details>
```

- Sections split on `## `. Do not use `###` as a top-level heading.
- The `#` title line is dropped.
- Text before the first `##` becomes an `Overview` section. Empty sections are
  dropped.
- Prose sections form the reading column. The curriculum's example and exercise
  blocks are merged in after them and keep their own order.
- `ordinal` preserves section order on read.

## Allowed content

- Markdown: paragraphs, lists, tables, blockquotes, inline code, and fenced code.
- `sql` fences render as code. They do not run.
- `mermaid` fences render to SVG in the browser with `securityLevel: 'strict'`.
- Inline HTML is allowed but sanitized with DOMPurify. The app uses `<details>`,
  `<summary>`, and `<span class="kbd-inline">...</span>`.
- Prose written in the curriculum uses the same inline HTML conventions.

## Add a module

1. Add the module to the course's file under `web/src/data/courses/`: id, title,
   objectives, blocks, and a graded exercise with `solution` and `contract`.
2. Add `web/src/data/content/<id>.md`.
3. If the module belongs to a new course, add the course to `COURSES` in
   `web/src/data/courses/index.ts` and add its prefix to `COURSE_BY_PREFIX` in
   `web/src/lib/contentSeed.ts`.

The seeding loop is data-driven. The lesson page loads the module ids from the
courses, so no separate content preload list needs editing.

## Exercise versions

An exercise may carry `version`, default `1`. Bump it when the prompt, contract,
or reference solution changes. Bump the dataset version when the schema or seed
changes. Every recorded attempt stores both versions, so an earlier pass reads as
"solved on an earlier version" in the progress history instead of being silently
re-graded.

## Fixtures first

Grading rules are shared by the JavaScript evaluator (`web/src/api/compare.ts`)
and the Go evaluator (`backend/internal/eval/compare.go`). Add a rule as cases in
`contracts/fixtures/` first: one JSON file per case with
`{ contract, actual_result, expected_outcome }`. Then implement it so both
languages pass the same files.

```sh
make audit-fixtures
make test
```

[`../contracts/fixtures/README.md`](../contracts/fixtures/README.md) describes the
case format.
