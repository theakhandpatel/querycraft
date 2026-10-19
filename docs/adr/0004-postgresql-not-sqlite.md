# ADR 0004: PostgreSQL on both adapters, not SQLite

- **Status:** accepted
- **Date:** 2026-10-14
- **Deciders:** project owner + agent session
- **Question:** for a POC, would SQLite have been enough? It needs no server, no
  download, no roles, no connection pool and no per-learner database: all of the
  M3 machinery, and the file is the database. Should the app be ported to it?

Raised after the M3 template-environment work, which is a large amount of
infrastructure that SQLite would make unnecessary.

## Decision

**Keep PostgreSQL, on both adapters.** PGlite (PostgreSQL compiled to WebAssembly)
in the browser, native PostgreSQL behind the Go gateway. Do not port to SQLite.

This is not a taste judgement. The repository's own content settles it.

## The evidence: the course *is* PostgreSQL

`web/src/data/curriculum.ts` plus `web/src/data/content/*.md` (23 chapters,
~164 KiB) use these constructs, counted by `rg`:

| Construct | Occurrences | SQLite |
| --- | --- | --- |
| `::` cast (`DATE_TRUNC('month', o.ordered_at)::date`) | 33 | **absent**: no cast operator; only `CAST(x AS t)`, and no date types |
| `DATE_TRUNC` | 30 | **absent** |
| `PARTITION BY` (window functions) | 25 | present since 3.25, but with different typing and ordering semantics |
| `EXPLAIN ANALYZE` | 18 | **absent**: SQLite has only `EXPLAIN QUERY PLAN`, whose output is a quirk of the planner, not a plan tree with actual row counts |
| `WITH RECURSIVE` | 10 | present |
| `ILIKE` | 2 | **absent**: `LIKE` is also ASCII-case-insensitive by default, so the wrong habit is learned |
| `DISTINCT ON` | 2 | **absent** |
| `COUNT(*) FILTER (WHERE …)` | present (m3) | present since 3.30 |
| `DATE '2026-01-01'`, `INTERVAL '1 month'`, `TO_CHAR` | present (m9, m6, m8) | **absent** as types/functions; DATE/INTERVAL literals and `TO_CHAR` do not exist |

Chapter by chapter, that is not a port. It is a rewrite:

- **m3** revenue reports: `DATE_TRUNC`, `FILTER`.
- **m6** cohort retention: `DATE_TRUNC` twice, `::date`.
- **m7** *"Read and improve a query plan"*: `EXPLAIN ANALYZE` and reading plan
  nodes with estimated vs actual rows. **This chapter cannot exist on SQLite at
  all**, and the whole M5 "SQL performance lab" milestone is built on it.
- **m8** capstone: `DATE_TRUNC`, window total.
- **m9** recursive CTEs: `DATE` literals, `INTERVAL` arithmetic, `TO_CHAR`.

Five of nine chapters in the first course alone, plus all of M5.

## Why SQLite would not even have reduced the work

The bugs the adapter-parity audit just found are the ones that matter, and SQLite
does not remove them:

- The gateway emitted `numeric` as a JSON number (`118.00` → `118`), silently losing
  scale and precision: the exact values a money column is made of.
- PGlite's JavaScript decoder shifted a naive `timestamp` by the host's UTC offset
  (`10:30:00` → `05:00:00`), because it materialises `timestamp` as a JS `Date`.
- The two adapters disagreed about SQLSTATE `42P01`/`42703` (`missing relation`):
  Go said `QUERY_SYNTAX_ERROR`, the design doc's own table (§4.2) says
  `QUERY_RUNTIME_ERROR` for "missing relation".

Both are decoding bugs *against the same engine*, and both are fixed by making both
adapters emit PostgreSQL's own text form for the types that have one (measured: a
PGlite per-OID parser override returns `2024-01-15`, `2024-01-15 10:30:00` and
`118.00` verbatim). Two *different* engines would have turned these two decodable
bugs into a permanent structural divergence: every result set, every error message
and every lesson's expected output would need a per-engine variant, and the M3 exit
gate "the same lesson content runs against both adapters unchanged" would stop
being checkable.

## Where SQLite would genuinely have been simpler

Stated honestly, because the owner's instinct is not wrong in the abstract:

- **A generic SELECT-only SQL tutor should use SQLite.** No 60 MiB download, no
  server to boot, no roles, no pool, no per-learner database. If the product were
  "learn `SELECT`/`JOIN`/`GROUP BY` against a toy table", SQLite in the browser
  (`wa-sqlite`, `sql.js`) is strictly less machinery and the right call.
- **The writable learner workspace is the one feature that is easier in SQLite**:
  copy a file, hand it to the learner, delete it. Per-account PostgreSQL
  environments (ADR 0003) cost ~35 ms per create and a connection budget; a file
  copy costs ~1 ms and no connection at all. Worth recording as the real price of
  the decision.
- If the concern is download size or an air-gapped install, the answer is bundling
  the PostgreSQL binaries or pointing `QC_DATABASE_URL` at a managed server, both
  already supported, not changing the engine.

## Consequences

- The engine is a single source of dialect truth: what the browser runs and what
  the gateway runs are the same PostgreSQL, so a lesson cannot teach behaviour the
  other adapter contradicts.
- The parity gate is meaningful and cheap to keep: one script, one dataset, both
  adapters.
- The price is the embedded server, the 60 MiB one-time download, the pool and the
  roles, all of it already built, measured and green, and cheaper to keep than to
  remove.
- New content must stay inside what PostgreSQL actually does. That is a feature:
  the point of the product is to teach PostgreSQL, not a dialect that merely looks
  like it.

## Follow-up: SQLite as a *second dialect*, not a replacement

The decision above is "PostgreSQL is the default engine", not "SQLite is banned". The
owner has asked for SQLite courses too, and that is a different (and good) product idea:
a course that teaches where SQLite differs from PostgreSQL is exactly the kind of thing
this app is for. Adding it is real work precisely because of the reasons above: dialect-aware grading, error classification and
value encoding, plus a second dataset and a per-dialect parity audit. It does not change
which engine the existing three courses run on.

## What is not proven

- No measurement of what a port would actually cost; the estimate above is by
  construct and chapter, not by stopwatch.
- SQLite's `STRICT` tables, `RETURNING` (3.35+), `RIGHT`/`FULL JOIN` (3.39+) and
  JSON1 narrow the gap *somewhat*; the table above reflects the constructs this
  curriculum actually uses today, not every difference between the engines.
