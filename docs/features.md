# Features

Querycraft teaches SQL by having you write it. Lessons combine short readings
with exercises you run against a real database, and each answer is graded by the
rows it returns.

The library starts with SQLite, then moves to PostgreSQL. It covers the clauses,
joins, subqueries, CTEs, window functions, query plans, and the differences
between the two engines. Every chapter runs; every answer is graded live.

## Two ways to run

The browser sample runs entirely in the page. It carries the built-in library,
runs SQLite and PostgreSQL in the browser, and keeps progress, notes, and themes
on this device. It needs no account and no server. It is the build published on
GitHub Pages.

The hosted build runs the Go gateway. It adds accounts, private server
workspaces, AI course generation, the tutor, stored courses, generation runs,
and administration. The library, grading, workbook, themes, and export work the
same way in both builds.

## Courses and lessons

- **A library to start from.** SQLite Essentials first, then SQL Foundations,
  analytics, joins, subqueries, query plans, and two deeper SQLite courses. Each
  course states its level, length, and dialect. The library includes eight
  courses and 52 chapters.
- **Lessons built for practice.** A chapter combines reading with one or two
  exercises and a worked example. Fenced Mermaid diagrams render as SVG.
- **Library.** Browse every course, filter by topic or dialect, and start a
  course from the library or the home screen.
- **Next step, always visible.** Home shows the next chapter, your streak, time
  practised, recent work, and suggested courses.

## Practice and grading

- **Grading by result.** An answer passes when its result matches the exercise
  contract: the columns and their order, the row count, the values, and the row
  order. The app never compares SQL text, so any correct query passes.
- **Useful feedback.** When a check fails, the app names the check and returns a
  stable error code such as `QUERY_TIMEOUT` or `QUERY_SYNTAX_ERROR`.
- **Help when you need it.** Every exercise offers a hint and a solution. In the
  hosted build, Ask tutor sends your query and the error to the configured model
  and talks you through it without revealing the reference query.
- **A real SQL editor.** Syntax highlighting, line numbers, bracket matching,
  and schema-aware completion for the chapter's dialect. The editor loads on
  demand and reads the app's theme tokens, so it follows every preset.
- **Run and check.** A cell runs with <kbd>Ctrl</kbd>+<kbd>Enter</kbd> or
  <kbd>Cmd</kbd>+<kbd>Enter</kbd>. You can run, check, reveal a hint or the
  solution, keep the query, reset the cell, or bookmark the exercise.

## Your workspace

In the browser sample, the local workspace is writable from the first query.
`INSERT`, `UPDATE`, `DELETE`, DDL, and transactions all work. Reset restores the
sample data at any time. Progress stays on this device.

In the hosted build:

- Signed out, queries run read-only against the shared sample data. A write is
  refused.
- Signed in, you get a private database copied from a sealed template.
  `INSERT`, `UPDATE`, `DELETE`, DDL, and transactions all work. Reset restores
  the sample data at any time.
- **Tables drawer.** Inspect the live schema, column types, keys, nullability,
  and sample rows without leaving the lesson.
- **Two engines.** SQLite is the first teaching engine in the browser.
  PostgreSQL is the deeper path: analytics, query plans, and the job-oriented
  material. The SQLite courses cover the differences that matter when you move
  between the two.

## Workbook and search

- Bookmarks, saved queries, notes, and the history of every exercise live in
  your workbook.
- Progress counts measured time only. It never guesses time you did not spend.
- Search covers lesson text, exercise questions, notes, bookmarks, and saved
  queries. Press <kbd>Ctrl</kbd>+<kbd>K</kbd> or <kbd>Cmd</kbd>+<kbd>K</kbd>.
- Export writes a versioned JSON file with no credentials. Import checks the
  file and merges it without overwriting work you already have.

## Accounts (hosted build)

- A new install seeds an administrator at `admin@querycraft.local` with a blank
  password. The blank password works only on the default loopback address. A
  network bind requires `admin_password` in `querycraft.json`.
- Public sign-ups receive the learner role. Authoring screens are available to
  the administrator.
- Your progress, workspaces, courses, and runs are scoped to your account. The
  app resolves the owner from the session, never from the request body.

## Build a course with AI (hosted build)

- **Start from a goal.** Describe what you want to get good at. The model writes
  an outline, then the chapters.
- **Approve the outline.** With approval enabled, the run stops after the
  outline so you can accept or discard it before any chapter is written.
- **Verified before stored.** The gateway runs every reference query and fixture
  in a new chapter. A chapter that fails is repaired with the engine's error, or
  dropped. If no chapter passes, the run stores nothing.
- **Resume a stopped run.** A run that stops mid-build leaves the chapters that
  verified. The library offers Resume, and the next run continues from them.
- **Edit and extend.** Add a chapter to an existing course and edit its
  exercises from the authoring screens.
- **Use any provider.** The built-in provider needs no key. Any
  OpenAI-compatible server works, and the embedded catalog resolves Anthropic,
  OpenAI, Gemini, and many more. Configure the provider from the manage screen
  while the gateway runs.

## Appearance

- 18 themes: light, dark, warm, cool, high-contrast, terminal, and familiar
  palettes such as One Dark, Dracula, Nord, Tokyo Night, Monokai, Rosé Pine,
  Solarized Light, and GitHub Dimmed.
- Build a theme from an accent color. The app derives the full token set and
  checks contrast.
- Themes sync to your account and follow you across devices.

## Run it your way

- **In the browser.** The browser build starts with SQLite, so lessons, results,
  and progress work without a server or a network. PostgreSQL also runs in the
  page for the PostgreSQL courses.
- **On the gateway.** The hosted app runs PostgreSQL or SQLite on the server and
  adds accounts, stored courses, and the tutor.
- **One binary.** `make build` embeds the landing page and the app in the
  gateway, which serves both with the API and its documentation on one port. It
  starts its own PostgreSQL when no database is configured.
- **Your database.** Set `database_url` in `querycraft.json` to use a container
  or a managed PostgreSQL server.
- **Operator endpoints.** `/health`, `/capabilities`, `/metrics`, `/diagnostics`,
  and OpenAPI 3.1 with Swagger UI at `/docs`.

## Learn more

| Topic | Document |
| --- | --- |
| Architecture and data layout | [architecture.md](architecture.md), [database-layout.md](database-layout.md) |
| SQLite specifics | [sqlite-dialect.md](sqlite-dialect.md) |
| Write a chapter | [content-authoring.md](content-authoring.md) |
| Export format | [export-format.md](export-format.md) |
| Run on a server | [self-hosting.md](self-hosting.md) |
| API reference | [../backend/README.md](../backend/README.md) |
