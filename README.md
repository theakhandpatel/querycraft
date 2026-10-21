<img src="assets/icon.svg" width="56" height="56" alt="">

# Querycraft

**Learn SQL by writing it.**

**[Open the browser sample](https://querycraft.akhand.cc/)** ·
[Documentation](docs/README.md)

You cannot learn SQL by watching someone else write it.

You can read the chapter, follow the diagram, and still freeze at the blank line where the query goes. You know the feeling. SQL looks like a set of rules until you have to write something yourself.

The skill is in the writing. The only way to build it is to write the query, run it, and see what comes back.

That moment is the product.

You get a chapter, a real database, and a question. You write `SELECT name, price FROM products WHERE category = 'Electronics' ORDER BY price;`. Five rows come back. Querycraft checks the columns, the rows, the order, and the values. If they match, you pass. It never compares your SQL to a model answer, because more than one query can be right.

No videos. No multiple choice. No "close enough."

![A graded exercise: the query runs, the contract checks pass, and the answer is accepted](docs/assets/walkthrough-grade.png)

## Start with a chapter

The library starts with SQLite Essentials because SQLite is one file and needs no server. The first query runs in the browser. SQL Foundations moves to PostgreSQL. Later courses cover joins, subqueries, CTEs, window functions, query plans, and the two SQLite courses that go deeper.

If the course you need does not exist, describe it. Querycraft drafts an outline, writes the chapters, and runs every exercise against the database before you see it. You can edit the result. It grades the same way as the built-in chapters.

## A database you can break

In the browser sample, the database runs in your tab. Sign in to the hosted build and it belongs to your account. Either way you can insert rows, change a table, open a transaction, and reset when you make a mess.

The workbook keeps your notes, saved queries, bookmarks, and every attempt. Search finds them. Choose one of 19 themes, or build one from an accent.

| | |
| --- | --- |
| <img src="docs/assets/screens/04-lesson.png" width="400" alt="A lesson with a SQL cell"> | <img src="docs/assets/screens/12-create-course.png" width="400" alt="Create a course"> |
| A chapter is a page you can run. | Describe a course. Every exercise is verified before you see it. |

## Try the browser sample

[Open the browser sample](https://querycraft.akhand.cc/). It
carries all eight courses and runs SQLite or PostgreSQL in the page. No account
is needed, and progress stays in your browser.

The hosted build adds accounts, AI course generation, the tutor, and stored
courses.

## Run it

You need Go 1.26 or later and Node 22 or later.

```sh
make install
make build
make serve
```

Open the address the gateway prints and choose **Start a lesson**.

The first start prepares the database and the sample data. Later starts are instant. To use your own PostgreSQL, read [Self-hosting](docs/self-hosting.md).

## Documentation

| Topic | Document |
| --- | --- |
| Build, test, and extend | [`docs/development.md`](docs/development.md) |
| Run on a server, configure, back up | [`docs/self-hosting.md`](docs/self-hosting.md) |
| Architecture and data layout | [`docs/architecture.md`](docs/architecture.md), [`docs/database-layout.md`](docs/database-layout.md) |
| API endpoints and configuration keys | [`backend/README.md`](backend/README.md) |
| Design decisions | [`docs/adr/`](docs/adr/) |
| All documentation | [`docs/README.md`](docs/README.md) |

## Contributing

Read [Development](docs/development.md) for the repository layout, the test suite, and the conventions. Before you open a pull request, run `make test` and `make verify`.

## License

Querycraft is available under the [MIT License](LICENSE).

---

**The database does not care how you wrote the query. It cares what comes back.**
