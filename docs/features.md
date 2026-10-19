# Features

This page tells you what Querycraft does. Each entry points to the code that
proves it. The status is `v0.9.0`.

The app ships 8 courses, 52 chapters, 53 examples, and 53 exercises. It runs two
SQL dialects: PostgreSQL and SQLite.

Section 10 lists what is not finished.

---

## 1. Learn

| Feature | What it does |
| --- | --- |
| Home | Shows the next chapter, your streak, your effort, recent work, and suggested courses. |
| Course | Shows the chapter index, the state of each chapter, a progress ring, and a list of your other courses. |
| Lesson | Shows the chapter text from the database. You get a section index, a bookmark button, and links to the previous and next chapter. |
| Diagrams | A fenced `mermaid` block becomes an SVG picture in the page. The app uses the strict security level and waits for the fonts before it draws. |
| Examples and exercises | Each chapter has one example and one or two exercises. One cell type runs both. |
| Library | Shows all courses with the domain, level, length, and dialect. You can filter the list and remix a course. |
| Workbook | Keeps your bookmarks, your saved queries, and the history of each exercise. It also has a search field. |
| Search | Press Ctrl+K or Cmd+K. The search covers lesson text, exercise questions, your notes, your bookmarks, and your saved queries. |
| Progress | Counts measured time only. Never counts time the app guessed. Shows weekly activity and one ring for each course. |
| Challenge lab | You describe a gap. The app drafts a request and shows the checks it runs. AI is optional, and the page says so. |
| Appearance | Sets the theme. Shows the engine facts the app reports and the workspace you use. |
| Export and import | Writes your progress to one JSON file with no credentials. Import checks the file against the schema and merges it. It does not overwrite your work. |

Code: `web/src/components/screens/`, `web/src/lib/exportBundle.ts`,
`web/src/lib/markdown.ts`, `web/src/components/SearchBox.tsx`.

## 2. Run and grade

| Feature | What it does |
| --- | --- |
| Real engines | Runs each statement on a real engine. The choices are PostgreSQL through WebAssembly in the browser, PostgreSQL through the gateway, or SQLite. |
| Result grading | Compares your result against the contract in four checks: the columns and their order, the row count, the row values, and the row order. Any correct query passes. The app never compares SQL text. |
| Feedback | Names the check that failed and an error code from one closed set. Examples: `QUERY_TIMEOUT`, `QUERY_SYNTAX_ERROR`, `DIALECT_MISMATCH`. |
| Cell controls | Run, Check answer, Show hint, Show solution, Accept my answer, Keep query, Reset, and Bookmark. Ctrl+Enter or Cmd+Enter runs the cell. |
| Tutor | "Ask tutor" and "Explain my mistake" call `POST /tutor`. The gateway runs your SQL, assembles the context, and sees the expected result after your first attempt but never the reference query (ADR 0008). |
| Limits | One function applies the statement deadline, the row cap, the byte cap, and the concurrency cap. The result reports `truncated` or `QUERY_TIMEOUT` when a cap applies. |
| Tables drawer | Shows the live schema: tables, columns, types, key flags, null flags, and sample rows. It also resets your workspace. |

Code: `backend/internal/engine/execute.go`, `backend/internal/eval/compare.go`,
`web/src/api/compare.ts`, `web/src/components/SqlCell.tsx`,
`web/src/components/TablesDrawer.tsx`.

## 3. Two dialects

| Feature | What it does |
| --- | --- |
| PostgreSQL first | PostgreSQL is the engine the app teaches. It gives you roles, templates, one database for each account, `EXPLAIN ANALYZE`, `FILTER`, and window functions. |
| SQLite second | SQLite is a real engine. In the browser it runs through WebAssembly. On the gateway it is a file. It teaches the translation skill. |
| One dialect for each chapter | Each chapter declares its dialect. A query in the wrong dialect stops with `DIALECT_MISMATCH`. It does not run in the wrong engine. |
| Badges | Every course, chapter, and result shows a PostgreSQL or SQLite badge. |

Code: `web/src/lib/dialect.ts`, `docs/sqlite-dialect.md`.

## 4. Environments and isolation

| Feature | What it does |
| --- | --- |
| Signed out | Runs on the shared dataset in a read-only transaction. A write is refused. |
| Signed in | Runs in your own database. The app copies this database from a sealed template. `INSERT`, `UPDATE`, `DELETE`, DDL, and transactions all work. |
| Reset | `POST /environment/reset` drops your changes and copies the sealed template again. |
| Restricted role | Learner SQL never uses an administrative connection. The gateway refuses `CREATE ROLE`, `pg_read_file`, `COPY … TO PROGRAM`, and `DROP DATABASE` with `QUERY_RUNTIME_ERROR`. |
| Metadata store | The app keeps accounts, progress, courses, and runs in PostgreSQL by default. Set `metadata` to `sqlite` in `querycraft.json` to use a SQLite file instead. The practice engine stays PostgreSQL. |

Code: `backend/internal/dataset/template.go`,
`backend/internal/httpapi/adminpath_test.go`, `docs/database-layout.md`.

## 5. Accounts

| Feature | What it does |
| --- | --- |
| Local accounts | A new install seeds `admin@querycraft.local`. Public signups get the learner role. |
| Blank admin password | The app accepts a blank password only on the default loopback address. A network bind needs `admin_password` in `querycraft.json`. |
| Owner scoping | The session sets the owner. The app never reads the owner from the request body. Another account gets `404`, not `403`. |
| Sign-in throttle | The gateway slows repeated failed sign-ins and keeps its memory bounded. |
| Authoring is admin only | A learner who opens an authoring route goes back to Home. |
| Run transcripts | The app redacts, bounds, and owner-scopes each transcript before it stores it. |

Code: `backend/internal/store/`,
`backend/internal/httpapi/login_throttle.go`, `web/src/App.tsx`.

## 6. Build a course with a model

| Feature | What it does |
| --- | --- |
| Start from a goal | You describe the result you want. The model writes an outline, then the chapters. |
| Outline approval | Turn this on and the run stops after the outline. You approve or discard the outline before any chapter starts. |
| One chapter for each call | A run holds one conversation. The model sends one chapter for each call. |
| Verify before store | The gateway runs every reference query and every fixture in a new chapter. A chapter that fails is repaired with the error from the engine, or dropped. |
| No proof, no store | If no chapter passes the checks, the run stores nothing. |
| Add to a course | You can add a chapter to an existing course and edit the exercises. |
| Resume a stopped run | A run that dies mid-build leaves an `interrupted` course with the chapters that verified. The library offers Resume, and the next build continues from those chapters. |

Code: `backend/internal/ai/`, `web/src/components/screens/CreateScreen.tsx`,
`docs/outline-approval-design.md`, `docs/adr/0002`, `docs/adr/0005`.

## 7. Build and deploy

| Feature | What it does |
| --- | --- |
| One binary | `make build` compiles the app, embeds it, and writes `backend/bin/querycraft`. This one file serves the app, the API, and the docs on one port. |
| No Docker | On the first start, the gateway downloads PostgreSQL, creates a cluster, and loads the dataset. It needs no Docker and no configuration. |
| Three adapters, one seam | The adapters are `pglite`, `sqlite`, and `http`. `VITE_API_MODE` selects `pglite` or `http`. |
| Auto-detect | A production build with no mode set asks `/health` on its own origin. If the gateway answers, the app uses the `http` adapter. |
| Operator surface | `/health`, `/capabilities`, `/metrics`, `/diagnostics`, and OpenAPI 3.1 with Swagger UI at `/docs`. |
| Any PostgreSQL | Set `database_url` in `querycraft.json` to use a container or a managed server. |
| Theming engine | 18 presets (light, dark, warm, cool, high-contrast, terminal, plus One Dark, Dracula, Tokyo Night, Nord, Monokai, Rosé Pine, Solarized Light and GitHub Dimmed) over one token set. Every preset clears 4.5:1 on its key text pairs — `make audit-themes` gates them. |
| User-defined themes | Name, accent, base family: the full token set derives with the same contrast math. Themes sync to the account (`GET/PUT /preferences`) and follow the learner across devices. |
| Runtime AI configuration | The manage surface saves the provider while the gateway runs: fake, any OpenAI-compatible server, or pi's unified multi-provider API (Anthropic, OpenAI, Gemini and 35 more via the embedded catalog). Hot-swapped, persisted across restarts. |

Code: `Makefile`, `backend/internal/localdb/`, `web/src/api/index.ts`.

## 8. Checks

| Check | What it proves |
| --- | --- |
| `make verify` | Builds the app, type-checks it, lints it, runs each audit, runs `go test ./...`, and starts the binary on a new PostgreSQL cluster. |
| `make verify-mock` | Proves the honest failure mode. With no database, `POST /query` answers `ENVIRONMENT_UNAVAILABLE`. It never invents rows. |
| `make lint` | Runs oxlint with the anti-slop rules vendored at `web/tools/oxlint/anti-slop/`. Errors gate `make test`. Warnings do not. |
| `make doctor` | Runs React Doctor over `web/src` for accessibility, render behaviour and bundle findings. It is a report, not a gate, and it exits 1 when it finds something. |
| 55 fixtures | The Go evaluator and the JavaScript evaluator read the same `contracts/fixtures/*.json`. Neither language keeps its own answers. |
| 24 audit suites | Cover courses, content, lessons, search, shell, progress, export, import, storage, accessibility, AI, adapter parity, load, adversarial cases, metrics, the dataset map, and SQLite error codes. |
| Adversarial suite | Runs the ten cases from design section 13.3 and a seeded resource-exhaustion fuzz phase on a new gateway. It also publishes what it does not protect. |
| Load test | Proves the concurrency cap by filling every slot. It compares the client numbers against `/metrics`. |
| Adapter parity | Runs the same SQL on PGlite and on the gateway, then compares each row. |

## 9. Accessibility

The lesson flow works with the keyboard only. A skip link goes to the reading
column. Every control shows focus. The results grid is a labelled table. The
tables drawer is a dialog that traps focus and returns it when it closes.

The keyboard evidence covers the home screen, a lesson, Progress and the
Workbook. It does not cover every screen. Section 10 lists the gaps a React
Doctor scan found.

`docs/accessibility.md` records what is verified, how, and what is not verified.

## 10. What is not finished

- React Doctor reports 42 issues and exits 1 (2 errors, 40 warnings), down from
  114 at the first scan. The two errors are the same `effect-needs-cleanup` false
  positive: the cleanup exists but the rule cannot follow the ref that owns it.
  Every remaining warning is a recorded verdict in `progress.md`: the custom
  drawers (tested focus trap and `inert`; a `<dialog>` migration is its own
  item), the two modal-backdrop click handlers, the high-complexity and giant
  components (splits tracked), and the deliberately ordered sequential loops.
  The target stays informational so a new finding is read rather than gated.
- Two React Doctor accessibility findings stay on a modal backdrop's click
  handler. The backdrop click is a pointer convenience; the Close button, Escape
  and the dialog's focus trap are the keyboard path. A `role` would name the
  backdrop a control it is not.
  (`web/src/components/Modals.tsx`, `web/src/components/ui/PromptDialog.tsx`)
- Slowloris and TLS handshake stalls stay outside the adversarial suite. The
  seeded fuzz phase covers malformed bodies, oversized bodies and the row, byte,
  time and concurrency caps; `docs/adversarial-suite.md` lists that coverage and
  what a learner can still read.

---

## Where to look

| Question | File |
| --- | --- |
| How do the parts fit together? | `docs/architecture.md` |
| Why is it built this way? | `docs/adr/` |
| What does the API give me? | `backend/README.md`, `/docs` |
| How does a query run, and where are the caps? | `backend/internal/engine/execute.go` |
| How does the app grade an answer? | `contracts/fixtures/`, `backend/internal/eval/compare.go` |
| Where does learner data live? | `docs/database-layout.md` |
| How do I write a lesson? | `docs/content-authoring.md` |
| Where are the schema and the seed data? | `backend/internal/dataset/schema.sql`, `seed.sql` |
| What has shipped, and what is open? | `progress.md` |
