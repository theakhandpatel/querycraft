<img src="assets/icon.svg" width="56" height="56" alt="">

# Querycraft

Querycraft teaches SQL by having you write it against a real database. Start
from a chapter on `SELECT` and `FROM`, joins, subqueries, CTEs, or query plans,
and every answer is graded live by the rows it returns. When the library is not
the course you need, describe the course you want and build it yourself. It gets
the same live grading.

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

- **Learn from real chapters.** The library covers `SELECT` and `FROM`, joins,
  subqueries, CTEs, window functions, and query plans. PostgreSQL and SQLite
  each get their own path, including where the two diverge.
- **Get graded live.** Every exercise runs against a real database and passes
  only when its columns, row order, and values match the contract. Any correct
  query passes; the feedback names the check that failed.
- **Design your own course.** Describe what you want to get good at. The app
  drafts an outline and chapters, verifies every exercise against the engine,
  and lets you edit the result. Your course is graded live, like the built-in
  ones.
- **Work in your own database.** Signed-in learners get a private workspace
  copied from a sealed template. Writes, DDL, and transactions work there, and
  reset restores the sample data.
- **Keep notes as you go.** Bookmarks, saved queries, notes, and progress live
  in your workbook. Search covers lessons, exercises, notes, bookmarks, and
  saved queries with <kbd>Cmd</kbd>+<kbd>K</kbd> or <kbd>Ctrl</kbd>+<kbd>K</kbd>.
- **Choose the look.** Start from one of 18 themes, or build one from an accent
  color. It follows your account across devices.
- **Ask for help.** Every exercise has hints, a solution, and an optional tutor
  that sees your query and the error you hit.

## Screenshots

| | |
| --- | --- |
| <img src="docs/assets/screens/02-home.png" width="400" alt="Home screen"> | <img src="docs/assets/screens/04-lesson.png" width="400" alt="A lesson with a SQL cell"> |
| Home: the next chapter, your streak, and your effort. | A lesson: reading, diagrams, and a SQL cell. |
| <img src="docs/assets/screens/06-passed.png" width="400" alt="A graded exercise"> | <img src="docs/assets/screens/09-tables-drawer.png" width="400" alt="The tables drawer"> |
| A graded exercise. Every contract check passed. | The tables drawer: the live schema, sample rows, and reset. |
| <img src="docs/assets/screens/12-create-course.png" width="400" alt="Create a course"> | <img src="docs/assets/screens/15-appearance.png" width="400" alt="Choose a theme"> |
| Describe a course; every exercise is verified before you see it. | Choose a preset, or build a theme from an accent color. |

The other screens are in [`docs/assets/screens/`](docs/assets/screens/). The
[full walkthrough](docs/assets/walkthrough.mp4) shows the app on the built
binary at 1920×1080.

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
