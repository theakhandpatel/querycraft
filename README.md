<img src="assets/icon.svg" width="56" height="56" alt="">

# Querycraft

Querycraft teaches SQL by having you write it against a real database. Each
exercise is graded by the rows your query returns, not by the text you typed,
so more than one correct answer can pass.

![A graded exercise: the query runs, the contract checks pass, and the answer is accepted](docs/assets/walkthrough-grade.gif)

## Run it

You need Go 1.26 or later and Node 20.19 or later.

```sh
make install   # frontend dependencies, once
make build
make serve
```

Run `make serve`, then open the address printed by the gateway and choose
**Start a lesson**.

The first start prepares a local PostgreSQL database with sample data. It takes
about 10 seconds. Later starts are immediate. To use your own PostgreSQL, create
`querycraft.json`:

```json
{"database_url":"postgres://user:pass@host:5432/db?sslmode=disable"}
```

For configuration, backups, and deployment, read
[Self-hosting](docs/self-hosting.md).

## What you can do

- **Follow a course.** Eight courses and 52 chapters cover PostgreSQL and
  SQLite. Lessons combine reading with SQL you run and check in the page.
- **Get graded on results.** An exercise passes when its columns, row order,
  and values match the expected result. The app explains which check failed.
- **Work in your own database.** Signed-in learners get a private workspace
  copied from a sealed template. Writes, DDL, and transactions work there, and
  reset restores the sample data.
- **Keep notes as you go.** Bookmarks, saved queries, notes, and progress live
  in your workbook. Search covers lessons, exercises, notes, bookmarks, and
  saved queries with <kbd>Cmd</kbd>+<kbd>K</kbd> or <kbd>Ctrl</kbd>+<kbd>K</kbd>.
- **Ask for help.** Every exercise has hints, a solution, and an optional tutor
  that sees your query and the error you hit.
- **Build a course.** Describe what you want to learn, and the app can generate
  a course whose exercises are checked against the engine before you see them.
  Use the built-in provider, or connect an OpenAI-compatible service.
- **Make it yours.** Choose one of 18 themes or build one from an accent color.

## Screenshots

| | |
| --- | --- |
| <img src="docs/assets/screens/02-home.png" width="400" alt="Home screen"> | <img src="docs/assets/screens/04-lesson.png" width="400" alt="A lesson with a SQL cell"> |
| Home: the next chapter, your streak, and your effort. | A lesson: reading, diagrams, and a SQL cell. |
| <img src="docs/assets/screens/06-passed.png" width="400" alt="A graded exercise"> | <img src="docs/assets/screens/09-tables-drawer.png" width="400" alt="The tables drawer"> |
| A graded exercise. Every contract check passed. | The tables drawer: the live schema, sample rows, and reset. |

The other screens are in [`docs/assets/screens/`](docs/assets/screens/). The
[full walkthrough](docs/assets/walkthrough.mp4) shows the app on the built
binary at 1920×1080.

## How it works

The app has two ways to run SQL. By default it runs PostgreSQL in your browser
with PGlite, inside a Web Worker, so you need no server. The Go gateway runs
PostgreSQL or SQLite on the server, serves the same app, and adds accounts,
stored courses, and the tutor. In a production build, the app uses the gateway
when it answers on its own origin.

`make build` produces one binary, `backend/bin/querycraft`, that serves the
landing page, the app, the API, and the API documentation on one port. It starts
its own PostgreSQL when no database is configured.

Read [Architecture](docs/architecture.md) for the components, the data model,
and the request path.

## Documentation

| Topic | Document |
| --- | --- |
| Build, test, and extend | [`docs/development.md`](docs/development.md) |
| Run on a server, configure, back up | [`docs/self-hosting.md`](docs/self-hosting.md) |
| Feature reference | [`docs/features.md`](docs/features.md) |
| Architecture and data layout | [`docs/architecture.md`](docs/architecture.md), [`docs/database-layout.md`](docs/database-layout.md) |
| API endpoints and configuration keys | [`backend/README.md`](backend/README.md) |
| Design decisions | [`docs/adr/`](docs/adr/) |
| All documentation | [`docs/README.md`](docs/README.md) |

## Contributing

Read [Development](docs/development.md) for the repository layout, the test
suite, and the writing and code conventions. Before you open a pull request,
run `make test` and `make verify`.
