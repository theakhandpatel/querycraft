<img src="assets/icon.svg" width="56" height="56" alt="">

# Querycraft

Learn SQL by writing it. Every exercise runs on a real PostgreSQL engine, and
every answer is graded by comparing your query's result against an explicit
contract. It never compares SQL text, so there is more than one right answer.

The whole product is one binary: `make build` writes `backend/bin/querycraft`,
which serves the app, the API, and its own documentation, and starts its own
PostgreSQL. No Docker and no configuration.

[![Graded: Passed](docs/assets/walkthrough-poster.png)](docs/assets/walkthrough.mp4)

| Recording | |
| --- | --- |
| [Walkthrough, MP4, 1:38, 1080p](docs/assets/walkthrough.mp4) | The full flow, recorded on the built binary at 1920×1080: sign-in, home, a course, a lesson, a graded exercise, the tables drawer, the library, the workbook, progress, the challenge lab, authoring, and the dark theme. |
| [Highlight GIF](docs/assets/walkthrough-grade.gif) | 15 seconds. Running an exercise and grading it. |

| | |
| --- | --- |
| <img src="docs/assets/screens/02-home.png" width="380" alt="Home"> | <img src="docs/assets/screens/04-lesson.png" width="380" alt="Lesson"> |
| Home. The next chapter, your streak, and your effort. | A lesson. Reading column, diagrams, and the SQL cell. |
| <img src="docs/assets/screens/06-passed.png" width="380" alt="Graded"> | <img src="docs/assets/screens/09-tables-drawer.png" width="380" alt="Tables drawer"> |
| Graded. All four contract clauses pass. | The tables drawer. The live schema, with a workspace reset. |

The other eleven screens are in `docs/assets/screens/`.

## What you get

**Eight complete courses.** 52 chapters across PostgreSQL and SQLite, with 53
worked examples and 53 exercises: analytics, joins, subqueries, query plans,
foundations, and three SQLite courses.

**A real engine, not a simulation.** The app runs full PostgreSQL in your
browser, in a Web Worker. It needs no server, works offline, and can stop a
runaway statement instantly.

**Grading by result, not by text.** An answer passes when its result matches
the contract: the columns, the row order, and the values. The same rules run
in the browser and on the server.

**Your own workspace.** An anonymous query runs read-only against the shared
dataset. A signed-in learner gets a private database copied from a sealed
template on first use. There, writes, DDL, and transactions all work, and the
tables drawer can restore the original data at any time.

**A workbook that stays yours.** Bookmarks, saved queries, notes, and progress
history live on your device. One search field covers lesson text, exercises,
notes, bookmarks, and saved queries with `Cmd`+`K` or `Ctrl`+`K`. Export
writes a versioned JSON file with no credentials; import merges without
overwriting.

**A challenge lab.** Signed-in learners can generate practice challenges from
the dataset, with the built-in deterministic provider or any
OpenAI-compatible model.

**Keyboard accessible.** The app works end to end with the keyboard only, with
a skip link, visible focus, and dialogs that trap and return focus.

## Try it

You need Go 1.26 or later and Node 20.19 or later. If `go` is not on `PATH`,
run `export PATH=$PATH:/usr/local/go/bin`.

```sh
make build        # frontend, embed, gateway -> backend/bin/querycraft
make serve        # app and API on http://localhost:8080
```

The first start downloads PostgreSQL once, creates its cluster, and loads the
commerce dataset. This takes about 10 seconds. Later starts are immediate. The
cluster lives under `.data/pg/` relative to where you started the gateway
(the repository root for `make serve`); `querycraft db status` prints the
resolved paths. `make verify` runs this cold start on every run and checks the
responses.

A new install seeds an administrator at `admin@querycraft.local` with an empty
password, so the first sign-in needs no setup. That empty password works only
on the loopback default (`127.0.0.1:8080`); a network bind requires
`admin_password` in the config file. Public signups get the `learner` role.

To use your own PostgreSQL instead of the embedded one:

```sh
printf '{"database_url":"postgres://user:pass@host:5432/db?sslmode=disable"}\n' > querycraft.json
./backend/bin/querycraft
```

Every setting has a key in that file, and the binary is a small CLI:

```sh
querycraft db status    # what the gateway would resolve right now
querycraft db reset     # delete the local cluster; the next start re-applies the dataset
```

The API documents itself: open `http://localhost:8080/docs` for the Swagger
UI, or read `backend/README.md` for the endpoints and every configuration key.

## Documentation

| Question | Document |
| --- | --- |
| How do the parts fit together, with diagrams? | [`docs/architecture.md`](docs/architecture.md) |
| How do I build, test, and extend it? | [`docs/development.md`](docs/development.md) |
| What does the product do, and what is unfinished? | [`docs/features.md`](docs/features.md) |
| How do the endpoints and settings work? | [`backend/README.md`](backend/README.md) |
| Where is every document indexed? | [`docs/README.md`](docs/README.md) |
| Why was a decision made? | [`docs/adr/`](docs/adr/) |

## Status

The app covers the full course experience from the design: the home dashboard,
courses, lessons with SQL cells, the tables drawer, the workbook, progress,
the challenge lab, and the authoring screens. Eight courses ship complete, and
every exercise is graded against a result contract.

What is unfinished is listed where the work is tracked:
[`docs/features.md`](docs/features.md) lists the open items, and
[`progress.md`](progress.md) records what has shipped.