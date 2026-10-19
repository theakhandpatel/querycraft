# The SQLite dialect

SQLite is a second dialect in Querycraft, not a replacement for PostgreSQL.
PostgreSQL is the default engine. This page lists the places where a lesson
written for one dialect is wrong in the other, and the checks that keep the two
honest.

## The dialect profile

The machine-readable half of this page is the dialect profile:

| Artifact | Role |
| --- | --- |
| `contracts/dialects/sqlite.json` | The language-neutral profile: capabilities, the affinity algorithm, value encoding, and the ordered message-to-code table. Both languages read it. |
| `backend/internal/dialect/sqlite.json` | The Go side's embedded copy. `go:embed` cannot reference a path outside its package, so the Go package embeds a byte-identical mirror. A test fails if the two differ. Copy the contract file over the mirror; never edit one alone. |
| `backend/internal/dialect/dialect.go`, `sqlite.go` | The reader: `Profile`, `SQLite()`, `ClassifyError`, and `EncodeValue`. |
| `backend/internal/dialect/dialect_test.go` | Table tests for the error map, the encoding rules, and the profile shape. |
| `backend/internal/dataset/mapping.json` | Column-by-column mapping of the commerce dataset between the dialects. Every row where they diverge carries a reason. |
| `scripts/dataset-map-audit.mjs` | The mapping audit: `make audit-dataset-map`. It recomputes each column's affinity from SQLite's algorithm instead of trusting the manifest. It fails on a missing or unmapped column, a type or affinity disagreement, a mismatch without a reason, a row-count drift, or a missing edge case. |

## How the claims are checked

Each claim has a check behind it. The kind of check matters:

- A test or audit reads the claim and fails when it stops being true. For the
  error map and the encoding rules, that is `dialect_test.go`. For the data, it is
  `dataset-map-audit.mjs`.
- A claim about the engine's own behavior is measured by the adapter-parity
  audit. That audit runs the same statements through the browser SQLite build and
  the gateway SQLite build and compares the results cell by cell. See
  [`adapter-parity.md`](adapter-parity.md).
- A profile entry is a contract, not proof of the engine. The parity audit is the
  proof.

The probes below ran in a `sqlite3` shell against a SQLite copy of the commerce
dataset, `qc.db`. The audit repeats the executable lesson statements and its own
probes through both adapters.

## The error map

PostgreSQL reports a five-character SQLSTATE, and both the browser adapter and
the gateway switch on it. SQLite has no SQLSTATE. It reports a primary result
code, mostly an integer, exposed by the C API as `SQLITE_CONSTRAINT(19)`,
`SQLITE_BUSY(5)`, and so on, plus a message. Several distinct primary codes and
most extended codes share one message prefix, so the message separates "you wrote
it wrong" from "the table does not exist".

Classification is by message pattern. The rules are ordered, and the first match
wins. The last rule is the catch-all.

| SQLite result code | Message test (kind) | Contract code |
| --- | --- | --- |
| `SQLITE_ERROR`(1) | `syntax error` (contains) | `QUERY_SYNTAX_ERROR` |
| `SQLITE_ERROR`(1) | `incomplete input` (equals) | `QUERY_SYNTAX_ERROR` |
| `SQLITE_ERROR`(1) | `unrecognized token:` (prefix) | `QUERY_SYNTAX_ERROR` |
| `SQLITE_ERROR`(1) | `unterminated` (contains) | `QUERY_SYNTAX_ERROR` |
| `SQLITE_BUSY`(5) | `database is locked` (equals) | `QUERY_TIMEOUT` |
| `SQLITE_LOCKED`(6) | `is locked` (contains) | `QUERY_TIMEOUT` |
| `SQLITE_INTERRUPT`(9) | `interrupted` (equals) | `QUERY_TIMEOUT` |
| `SQLITE_CANTOPEN`(14) | `unable to open database file` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_NOTADB`(26) | `file is not a database` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_CORRUPT`(11) | `database disk image is malformed` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_IOERR`(10) | `disk I/O error` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_FULL`(13) | `database or disk is full` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_PERM`(3) | `access permission denied` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_NOMEM`(7) | `out of memory` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_MISUSE`(21) | `API misuse` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_MISUSE`(21) | `called out of sequence` (contains) | `ENVIRONMENT_UNAVAILABLE` |
| `SQLITE_TOOBIG`(18) | `string or blob too big` (contains) | `RESULT_LIMIT_EXCEEDED` |
| `SQLITE_ERROR`(1) | `no such table:` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `no such column:` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `no such function:` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `no such module:` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `ambiguous column name:` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_CONSTRAINT`(19) and subtypes | `constraint failed` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_MISMATCH`(20) | `datatype mismatch` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `misuse of` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_READONLY`(8) | `attempt to write a readonly database` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_AUTH`(23) | `not authorized` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_AUTH`(23) | `authorization denied` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_RANGE`(25) | `out of range` (contains) | `QUERY_RUNTIME_ERROR` |
| anything else | `""` (contains, last rule) | `QUERY_RUNTIME_ERROR` |

Four decisions in that table are choices rather than transcriptions:

- `SQLITE_BUSY` and `SQLITE_LOCKED` are `QUERY_TIMEOUT`, not an environment
  fault. Contention spends the learner's deadline, which is how the PostgreSQL
  pool-saturation path already reports a timeout. Reporting
  `ENVIRONMENT_UNAVAILABLE` would say the engine is down when it is merely busy.
- `SQLITE_BUSY` is matched with `equals`, so the shorter `SQLITE_LOCKED` message
  (`database table is locked`) is not swallowed by the `is locked` rule. The rule
  order is part of the profile, and a test pins it.
- `SQLITE_READONLY` is a runtime error. That is the code the anonymous, read-only
  dataset produces when a write is refused. A refused write is a learner mistake,
  not a broken environment.
- The host's caps share `RESULT_LIMIT_EXCEEDED`. `SQLITE_TOOBIG` is the engine's
  own size cap. The adapter's row and byte caps use the same code with
  `truncated` set, so a contract has one code to reason about.

`EMPTY_QUERY`, `PERSISTENCE_FAILED`, and `EVALUATION_ERROR` are not in the table
and never appear there. The first is the executor's blank-SQL check. The other
two are not engine errors. The profile cannot name a code outside the closed
`QueryResult` enum, and the classifier cannot return one of those three.

## Value encoding

SQLite reports a value's storage class, and there are five: `NULL`, `INTEGER`,
`REAL`, `TEXT`, and `BLOB`. There is no boolean class and no date class. The
adapter encodes the storage class it received, never the declared type of the
column it came from. Re-applying the declaration would report a value the engine
never stored.

Both adapters read the class from the engine itself. The browser uses
`@sqlite.org/sqlite-wasm`. The gateway reads through the raw sqlite3 API in
`backend/internal/engine/sqlite_raw.go`, not through a `database/sql` driver that
might turn a `DATE`-declared `TEXT` value into a `time.Time`. A date column
therefore arrives as its stored text on both sides.

| Storage class | JSON | Rule, and the case worth teaching |
| --- | --- | --- |
| `INTEGER` | number | Within ±2^53−1 it is a JSON number. Above that it is a decimal string, because a JSON number cannot hold it exactly. |
| `REAL` | number | `NaN` is reported as `null`, which is what SQLite stores. `Infinity` and `-Infinity` are reported as the strings `"Infinity"` and `"-Infinity"`, because JSON has no literal for them. |
| `TEXT` | string | Verbatim. No ISO-8601 parsing and no numeric coercion. A date stays the text it was stored as, and a numeric-looking string stays a string. |
| `BLOB` | string | `\x` followed by lowercase hex, the same form PostgreSQL `bytea` uses. |
| `NULL` | `null` | SQL NULL, never the empty string and never zero. |

A result column's `type` label is the declared type verbatim, from
`sqlite3_column_decltype`: `INTEGER`, `TEXT`, `NUMERIC`, `VARCHAR(20)`. A column
that never had a declared type, such as `COUNT(*)` or `price * 2`, gets the label
`expression`. A `DATE` column labels `DATE` while every value in it is `TEXT`. A
label is not a type.

## The differences

### No SQLSTATE, so classification is by message

The PostgreSQL adapters switch on SQLSTATE. SQLite has no such value. The SQLite
classifier is a message-pattern table, and the table is data, so both languages
read the same one.

A lesson should say that a missing table is a runtime error, not a syntax error.
A lesson that tells the learner to expect `42P01` cannot be written for this
dialect.

### Dynamic typing and affinity

A PostgreSQL column has a type. A SQLite column has an affinity, applied when a
value is written. Afterwards the column may hold a value of any storage class.
The affinity comes from text-substring rules on the declared type, which is why
`DATE`, `BOOLEAN`, `NUMERIC`, and `TIMESTAMP` all select the same NUMERIC
affinity:

| Affinity | Selected when the declared type contains |
| --- | --- |
| `INTEGER` | `INT` |
| `TEXT` | `CHAR`, `CLOB`, or `TEXT` |
| `BLOB` | `BLOB`, or nothing at all |
| `REAL` | `REAL`, `FLOA`, or `DOUB` |
| `NUMERIC` | anything else |

The dataset shows the difference rather than hiding it. `customers.signup_date` is
declared `DATE`, which selects NUMERIC affinity. The ISO-8601 text it stores is
not a numeric literal, so the value stays `TEXT`:

```sh
$ sqlite3 qc.db "SELECT signup_date, typeof(signup_date) FROM customers LIMIT 1"
2025-11-02|text
```

That is the "one column whose value class differs from its declared affinity"
case. `mapping.json` records it as a required mismatch.

A lesson should say that the declared type is a hint to the engine, not a promise
to the reader. `typeof(x)` is the dialect's answer to "what is this, really".
`STRICT` tables are how you ask for the promise back, one table at a time. This
dataset does not use them, so the dynamic behavior stays visible.

### Money has no fixed scale

`products.price` and `order_items.unit_price` are `numeric(10,2)` in PostgreSQL.
SQLite has no decimal type. NUMERIC affinity stores a value in its smallest
lossless class, so the same column reports different classes for different rows:

```sh
$ sqlite3 qc.db "SELECT group_concat(DISTINCT typeof(price)) FROM products"
integer,real
$ sqlite3 qc.db "SELECT price FROM products WHERE id = 1"
59
```

The value was written as `59.00`. Declaring the column `TEXT` would keep the
scale, but it would turn `WHERE price > 100` into a lexicographic comparison. A
measured probe returns `'59.00'` and `'9.99'` that way, which breaks every
filtering lesson. NUMERIC keeps comparisons numeric and gives up the scale. A
course prints money with `printf('%.2f', price)`.

A lesson should say that exact decimal arithmetic does not exist here. Scale is a
formatting decision. `SUM` over these values returns a REAL. A contract that
needs exact scale needs a cast, a tolerance, or integer minor units.

### LIKE is case-insensitive for ASCII

`LIKE` in SQLite is case-insensitive for ASCII by default, so `'ADA' LIKE 'ada'`
is true. `ILIKE` does not exist. `GLOB` is the case-sensitive one.

```sh
$ sqlite3 :memory: "SELECT 'ADA' LIKE 'ada', 'ADA' GLOB 'ada'"
1|0
```

A PostgreSQL lesson that relies on `LIKE` being case-sensitive teaches a habit
that gives different answers here. Use `LIKE` for the ASCII-insensitive case, or
`UPPER(...) LIKE ...` to make the intent explicit. Use `GLOB` or
`COLLATE BINARY` for the sensitive case. The insensitivity is ASCII-only, so
`LIKE` is not equivalent to `lower()` for non-ASCII text.

### Dates are not a type

There is no `DATE`, `TIME`, `TIMESTAMP`, or `INTERVAL` storage class. There is no
`DATE '...'` literal, no interval arithmetic, no `DATE_TRUNC`, and no `TO_CHAR`:

```sh
$ sqlite3 :memory: "SELECT DATE_TRUNC('month', '2025-11-02')"
Error: no such function: DATE_TRUNC
$ sqlite3 :memory: "SELECT TO_CHAR(1, '9')"
Error: no such function: TO_CHAR
$ sqlite3 :memory: "SELECT date('2025-11-02','start of month')"
2025-11-01
```

A date is ISO-8601 text with functions over it: `date()`, `strftime()`,
`julianday()`. The dialect's `DATE_TRUNC('month', d)` is
`strftime('%Y-%m-01', d)` or `date(d, 'start of month')`. The dataset stores
`2025-11-02` and `2026-01-04 10:12` verbatim, the same characters PostgreSQL
prints, so a lesson can compare text to text.

A lesson should say that date arithmetic is string or number arithmetic with
named modifiers. Month ends are not special. A "timestamp" sorts correctly only
because ISO-8601 sorts lexicographically in this form.

### No roles and no schemas

There is no `CREATE ROLE`, no `GRANT`, no `current_user`, no schema namespace,
and no `search_path`. A SQLite database is one namespace, so the dataset tables
are unqualified and the PostgreSQL `dataset` schema has no counterpart.

The `app` schema's isolation, where learner SQL cannot reach gateway metadata, is
enforced by the host instead of the engine's privilege model. The host uses a
separate file and opens it read-only with `query_only`.

A lesson should say that `information_schema`, `pg_roles`, and `GRANT ... ON
SCHEMA` have no analogues. What a learner can see is what the host handed them.
"Who may read this table" is a different question in the two dialects.

### No EXPLAIN ANALYZE and no cost-based plan output

`EXPLAIN ANALYZE` does not exist. The token sequence is not even the same
statement:

```sh
$ sqlite3 :memory: "EXPLAIN ANALYZE SELECT 1"
Error: near "SELECT": syntax error
$ sqlite3 :memory: "EXPLAIN QUERY PLAN SELECT 1"
QUERY PLAN
`--SCAN CONSTANT ROW
```

`EXPLAIN QUERY PLAN` returns one text row per step. It says which scan or search
is used, but not estimated rows, actual rows, or cost, so there is nothing to
compare against a threshold. A lesson about reading and improving a PostgreSQL
plan cannot be written in this dialect. The profile records
`capabilities.analyze = false`, `capabilities.plan = false`, and
`capabilities.explain_query_plan = true`.

A lesson should say that an index's effect is visible, because the step changes
from `SCAN` to `SEARCH ... USING INDEX`. A plan cannot be measured the way the
PostgreSQL plan lessons measure one. Any timing claim is the learner's stopwatch,
not the engine's numbers.

### AUTOINCREMENT is not GENERATED or IDENTITY

PostgreSQL generates identities with `GENERATED ... AS IDENTITY` or `serial`.
SQLite's nearest spelling is `INTEGER PRIMARY KEY AUTOINCREMENT`, and it is not
the same thing.

`INTEGER PRIMARY KEY` already generates a rowid. The id is the rowid alias, with
no keyword at all. `AUTOINCREMENT` adds the "never reuse an id, even after the
row is deleted" guarantee, and it pays for that with a `sqlite_sequence` table:

```sh
$ sqlite3 :memory: "CREATE TABLE t(id INTEGER PRIMARY KEY AUTOINCREMENT, a TEXT); INSERT INTO t(a) VALUES ('x');
> SELECT name FROM sqlite_master WHERE name='sqlite_sequence'"
sqlite_sequence
```

This dataset uses neither. The seed supplies explicit ids exactly as the
PostgreSQL seed does, so ids are identical across the dialects and an
`INSERT ... SELECT` of the seed is reproducible. The profile records
`capabilities.autoincrement = true` with a note that the dataset does not use it.

A lesson should say that identity in SQLite is a rowid alias, not a sequence
object. `AUTOINCREMENT` is a choice about monotonic ids with a cost. The dialect
has no `GENERATED ... AS IDENTITY` and no `serial`.

### PRAGMA is not SET

PostgreSQL configures a session with `SET search_path = ...` and
`SET statement_timeout = ...`. SQLite has no `SET`. The equivalent is a `PRAGMA`,
which is also how you read engine internals:

```sh
$ sqlite3 :memory: "SET search_path = dataset"
Error: near "SET": syntax error
$ sqlite3 qc.db "SELECT name, type FROM pragma_table_info('customers')"
```

The pieces that matter for this app:

- `PRAGMA foreign_keys = ON`, because foreign keys are not enforced by default.
- `PRAGMA query_only = ON`, which is how the anonymous read-only dataset stays
  read-only.
- `PRAGMA journal_mode`, because WAL adds `-wal` and `-shm` sidecars a reset has
  to handle.
- `PRAGMA synchronous`.

A lesson should say that session configuration is a `PRAGMA` call and that some
settings are per connection rather than per database. `foreign_keys` being off by
default is why `orders.customer_id`'s `REFERENCES` clause is a declaration here
but an enforcement in PostgreSQL.

### rowid and WITHOUT ROWID

Every ordinary SQLite table has a hidden 64-bit `rowid` unless it is declared
`WITHOUT ROWID`. `INTEGER PRIMARY KEY` is an alias for it. Without that alias,
the rowid is addressable but not stable across a `VACUUM`:

```sh
$ sqlite3 :memory: "CREATE TABLE w(a TEXT PRIMARY KEY) WITHOUT ROWID; SELECT rowid FROM w"
Error: no such column: rowid
```

A lesson should say that `rowid` is a physical address, not a key. `ORDER BY
rowid` is insertion order today, and answers must not depend on insertion order.
A lesson that wants an identity should declare one. A lesson that wants a
clustered storage layout can say `WITHOUT ROWID`. The profile records both
`capabilities.rowid_tables` and `capabilities.without_rowid`.

## When SQLite is the right choice

SQLite suits a second dialect course that teaches where the engines differ:
affinity, `rowid`, `PRAGMA`, text dates, and the absence of roles and plans. That
course needs a real SQLite, not a simulation.

It also suits a `SELECT`-only tutor over a toy table, where the file is the
database and there is no server to run. It suits any case where the whole
database should be one file you can copy, hand to a learner, and delete. A
per-learner workspace costs a file copy rather than a `CREATE DATABASE ...
TEMPLATE`. It suits air-gapped or tiny installs where a PostgreSQL download is
the objection.

It is the wrong choice when the lesson is the PostgreSQL feature: plan reading
with actual row counts, `DATE_TRUNC` and `INTERVAL` date work, `DISTINCT ON`,
`ILIKE`, schemas and roles, or column types that are guarantees rather than
hints. PostgreSQL stays the default for those lessons, and every chapter names
its dialect.

## Limits of the checks

- The dialect tests prove that the profile states a claim and that the Go reader
  matches it. They do not prove the engine. The parity audit runs the same SQL
  through both adapters and compares the values.
- The two adapters ship different SQLite builds. The audit prints both build
  strings as information and never compares them. A version difference is
  expected. A value difference is a finding.
- The SQLite dataset is an authored mirror of the PostgreSQL schema and seed. The
  mapping audit checks every column and every mismatch reason. It also guards the
  row counts against the Go generator.
- Plans, `rowid` values, and storage-layer pragmas are properties of a database
  file. The parity audit excludes them from comparison and prints the reason. It
  still executes them, and it still compares any stored data in the statement.
