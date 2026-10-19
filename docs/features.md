# Features

Querycraft teaches SQL by having you write it. Lessons combine short readings
with exercises you run against a real database, and each answer is graded by the
rows it returns.

The app ships eight courses and 52 chapters across PostgreSQL and SQLite.

## Courses and lessons

- **Eight courses.** Analytics, joins, subqueries, query plans, foundations, and
  three SQLite courses. Each course states its level, length, and dialect.
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
- **Help when you need it.** Every exercise offers a hint and a solution. Ask
  tutor sends your query and the error to the configured model and talks you
  through it without revealing the reference query.
- **Run and check.** A cell runs with <kbd>Ctrl</kbd>+<kbd>Enter</kbd> or
  <kbd>Cmd</kbd>+<kbd>Enter</kbd>. You can run, check, reveal a hint or the
  solution, keep the query, reset the cell, or bookmark the exercise.

## Your workspace

- **Signed out.** Queries run read-only against the shared sample data. A write
  is refused.
- **Signed in.** You get a private database copied from a sealed template.
  `INSERT`, `UPDATE`, `DELETE`, DDL, and transactions all work. Reset restores
  the sample data at any time.
- **Tables drawer.** Inspect the live schema, column types, keys, nullability,
  and sample rows without leaving the lesson.
- **Two engines.** PostgreSQL is the main teaching engine. SQLite is a second
  dialect with its own courses, so you can learn the differences that matter
  when you move between the two.

## Workbook and search

- Bookmarks, saved queries, notes, and the history of every exercise live in
  your workbook.
- Progress counts measured time only. It never guesses time you did not spend.
- Search covers lesson text, exercise questions, notes, bookmarks, and saved
  queries. Press <kbd>Ctrl</kbd>+<kbd>K</kbd> or <kbd>Cmd</kbd>+<kbd>K</kbd>.
- Export writes a versioned JSON file with no credentials. Import checks the
  file and merges it without overwriting work you already have.

## Accounts

- A new install seeds an administrator at `admin@querycraft.local` with a blank
  password. The blank password works only on the default loopback address. A
  network bind requires `admin_password` in `querycraft.json`.
- Public sign-ups receive the learner role. Authoring screens are available to
  the administrator.
- Your progress, workspaces, courses, and runs are scoped to your account. The
  app resolves the owner from the session, never from the request body.

## Build a course with AI

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

- **In the browser.** The default experience runs PostgreSQL in a Web Worker
  with PGlite. It needs no server and works offline.
- **On the gateway.** The Go service runs PostgreSQL or SQLite on the server and
  adds accounts, stored courses, and the tutor. The app switches to it when it
  answers on the same origin.
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
