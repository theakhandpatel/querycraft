# The JSON export

Querycraft writes one JSON document when you choose Export in the workbook or in
Appearance. It is the learner's own copy of their work and the migration path
between browser mode and server mode.

```jsonc
{
  "format_version": 1,
  "exported_at": "2026-10-14T06:57:59.037Z",
  "content_versions": {
    "dataset_version": 1,
    "progress_version": 5
  },
  "progress": {
    "version": 5,
    "seeded": false,
    "events":   [ /* one entry per attempt: ts, id, kind, title, course, lesson */ ],
    "passed":   { "ex.1.1": true },
    "accepted": { /* "accept my answer" overrides, kept separate from a pass */ },
    "firstTry": { "ex.1.1": true },
    "done":     { "m1": true },
    "notes":    [ /* { course, lesson, kind: note|bookmark, ts, text } */ ],
    "saved":    [ /* { course, lesson, name, sql } */ ],
    "attempts": 1,
    "attemptsByDay": [0, 0, 0, 0, 0, 0, 1],
    "timeMs":   { "sql-analytics": 180000 }
  },
  "drafts": { "ex.1.1": "SELECT name, category, price FROM products ..." }
}
```

## What the document guarantees

- Versioned. `format_version` is present and bumped when the shape changes in a
  way an older reader could not accept. `content_versions` records the dataset
  and progress versions the data was written against, so an old attempt stays
  interpretable after a seed or contract change.
- Complete. Every workbook entity the learner owns is in the document: attempt
  history, passes, acceptances, chapter completion, notes, bookmarks, saved
  queries, measured study time, and the editor draft for each cell.
- Credential-free. There is no token, session, password, or hash field. The
  bundled `Progress` snapshot has none, and the export adds none.
- Measured, not guessed. `timeMs` is active reading or practice time recorded
  while a chapter is open and the tab is visible. A course with no measured time
  is absent rather than filled in with an estimate.

`scripts/export-audit.mjs` (`make audit-export`) checks these properties. It runs
as part of `make test`.

## The schema

`contracts/export.schema.json` is the machine-readable form of this document.
Both halves of the feature use it. `web/src/lib/exportBundle.ts` produces a
document the schema accepts. `web/src/lib/importBundle.ts` validates every import
against it before writing anything.

`make audit-export` and `make audit-import` run the schema through a real
JSON-Schema validator (`ajv`), so the schema is executed rather than quoted.

The schema sets `additionalProperties: false` at every level. That is what makes
the credential-free guarantee checkable. A document carrying a `token`,
`password`, or `hash` field is invalid, not silently imported.

## Import

Import sits beside Export in Appearance and in the workbook's Continue card.
Import is a merge, not a restore.

| Situation | What happens |
| --- | --- |
| Unknown `format_version` | Refused, with the version named. Nothing is written. |
| `progress_version` newer than this build | Refused. A snapshot layout from the future cannot be interpreted. |
| Document fails the schema | Refused, naming the path and the first complaint. Nothing is written. |
| Text is not JSON | Refused, with the parser's position. Nothing is written. |
| An attempt already recorded | Skipped. Identity is the client request id, falling back to `course:exercise:timestamp` for documents written before request ids existed. Two attempts at one exercise are two entries, and both survive. |
| A note or bookmark already present | Skipped. Nothing the learner wrote is deleted or overwritten. |
| A saved query already present | Skipped, on `(course, lesson, name, sql)`. |
| A cell already has a draft | The draft in the editor wins. Import fills only cells with no draft. |
| Study time | Merged per course by the larger measured total, so re-importing the same file cannot double it. |
| Passes, acceptances, chapter completion | Union. Import can add a pass, never remove one. |
| Attempt counters and daily attempts | The larger value per counter and per day. |

Importing an export into an empty workspace reproduces that export exactly.
Importing it twice is the same as importing it once. `scripts/import-audit.mjs`
(`make audit-import`) proves both, along with every row above. It exports,
imports into an empty store, exports again, and compares the two documents apart
from `exported_at`.

Import is adapter-independent. It takes the current `Progress` and the file's
text and returns the merged `Progress`. The same code path runs signed in against
the gateway and offline against PGlite. The store persists the result through the
appropriate seam.
