# The SQLite dialect: what a course must teach

M6 step 3. SQLite is a **second dialect**, not a replacement for PostgreSQL
(ADR `0004-postgresql-not-sqlite.md`); the default engine stays PostgreSQL, and
this document is about the places where a lesson written for one dialect is
*wrong* in the other.

The machine-readable half of this document is the dialect profile:

| Artifact | Role |
| --- | --- |
| `contracts/dialects/sqlite.json` | The language-neutral profile: capabilities, the affinity algorithm, the value-encoding rules, and the ordered message-to-code table. It is the file both languages read, like `contracts/fixtures/` is for the evaluator. |
| `backend/internal/dialect/sqlite.json` | The Go side's embedded copy. `go:embed` cannot reference a path outside its package directory, so the Go package embeds a byte-identical mirror and `TestEmbeddedProfileMatchesContract` fails the build's tests if the two ever differ. Copy the contract file over the mirror; never edit one alone. |
| `backend/internal/dialect/dialect.go`, `sqlite.go` | `Profile`/`ByName`, `ClassifyError`, `Affinity`, `EncodeValue`: the reader. |
| `backend/internal/dialect/dialect_test.go` | The table tests. One case per contract code and one per affinity class, plus the shape checks on the profile itself. |
| `backend/internal/dataset/mapping.json` | Column-by-column mapping of the commerce dataset between the dialects, with a reason on every row where they diverge. |
| `scripts/dataset-map-audit.mjs` | The audit of that mapping: `node scripts/dataset-map-audit.mjs` (`make audit-dataset-map`). It recomputes each column's affinity from SQLite's algorithm rather than trusting the manifest, and fails on a missing column, an unmapped column, a type or affinity disagreement, a mismatch without a reason, a row-count drift, or a missing §7.1 edge case. |

## How to read the "verified by" lines

Each claim below names the artifact that holds it to account. Be precise about
what that means, because the difference matters:

- A **test or audit** line means a program in this repository reads the claim
  and fails when it stops being true. For the error map and the encoding rules
  that is `backend/internal/dialect/dialect_test.go`; for the data it is
  `scripts/dataset-map-audit.mjs`.
- Where the claim is about **the engine's own behaviour** (what SQLite does with
  `LIKE`, or what `EXPLAIN ANALYZE` does), no test in this step executes a
  SQLite engine: the adapters arrive in steps 4–6. Those claims were measured
  with the local `sqlite3` CLI, **version 3.31.1**, and the probes are quoted
  so they can be re-run. Their permanent home is step 8's per-dialect
  adapter-parity audit, which runs the same statements through the wasm and
  gateway SQLite adapters.
- What the tests in *this* step guarantee about those claims is that the profile
  states them, that the statement is well formed, and that the Go reader's
  behaviour matches it. A profile entry is a contract, not a proof of the
  engine; the parity audit is the proof.

## Table 1: the error map (there is no SQLSTATE)

PostgreSQL reports a five-character SQLSTATE (`42P01`, `42703`, …) and
`web/src/api/pglite.ts` and the gateway both switch on it. SQLite has no
SQLSTATE: it reports a **primary result code**: mostly an integer, exposed by
the C API as `SQLITE_CONSTRAINT(19)`, `SQLITE_BUSY(5)`, …: plus a message.
Several distinct primary codes and most extended codes share one message
prefix, so the message is what separates "you wrote it wrong" from "the table
does not exist".

Classification is therefore by **message pattern**, and every pattern has a
case. The rules are ordered and the first match wins; the profile's last rule
is the catch-all.

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
| `SQLITE_CONSTRAINT`(19) + subtypes | `constraint failed` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_MISMATCH`(20) | `datatype mismatch` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_ERROR`(1) | `misuse of` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_READONLY`(8) | `attempt to write a readonly database` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_AUTH`(23) | `not authorized` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_AUTH`(23) | `authorization denied` (contains) | `QUERY_RUNTIME_ERROR` |
| `SQLITE_RANGE`(25) | `out of range` (contains) | `QUERY_RUNTIME_ERROR` |
| anything else | `""` (contains, last rule) | `QUERY_RUNTIME_ERROR` |

Four decisions in that table are worth stating, because they are choices rather
than transcriptions:

- **`SQLITE_BUSY`/`SQLITE_LOCKED` are `QUERY_TIMEOUT`, not an environment
  fault.** Contention spends the learner's deadline, which is exactly how the
  PostgreSQL pool-saturation path already reports a timeout. Reporting
  `ENVIRONMENT_UNAVAILABLE` would tell the learner the engine is down when it is
  merely busy, and the message names the writer when SQLite knows it.
- **Constant-contention semantics.** `SQLITE_BUSY` is matched with `equals` so
  the shorter `SQLITE_LOCKED` message (`database table is locked`) is not
  swallowed by the `is locked` rule; the ordering of the two rules is part of
  the profile, and the table test pins it.
- **`SQLITE_READONLY` is a runtime error.** That is the code the anonymous,
  read-only dataset produces when a write is refused (step 5), and a refused
  write is a learner-visible mistake, not a broken environment.
- **The host's caps share `RESULT_LIMIT_EXCEEDED`.** `SQLITE_TOOBIG` is the
  engine's own size cap; the adapter's row and byte caps use the same code with
  `truncated` set, so a contract has one code to reason about.

`EMPTY_QUERY`, `PERSISTENCE_FAILED` and `EVALUATION_ERROR` are **not** in the
table and must never be. The first is the executor's blank-SQL check; the other
two are not engine errors at all. The profile cannot name a code outside the
closed `QueryResult` enum, and the classifier cannot return one of those three
for any message.

**Verified by:** `dialect_test.go`: `TestClassifyError` (a case per message
family), `TestClassifyCasesCoverEveryReachableCode` (a code with no case fails
the suite), `TestReachableCodesAreTheEngineOnes` (the five reachable codes and
the three that must never appear), `TestClassifyErrorNeverEmitsExecutorCodes`
(the corpus cannot produce an executor code), `TestClassifyNilErrorHasNoCode`
(a nil error has no code at all), `TestErrorClassificationShape` (rule
shape, ordering, and the catch-all position) and `TestErrorCodesAreClosed`
(the closed enum), and
`TestEmbeddedProfileMatchesContract` (the Go mirror is the contract file).

## Table 2: value encoding

SQLite reports a value's **storage class**, and there are five: `NULL`,
`INTEGER`, `REAL`, `TEXT`, `BLOB`. There is no boolean class and no date class.
The adapter must encode the storage class it received, never the declared type
of the column it came from: re-applying the declaration would report a value
the engine never stored.

Both adapters read that class from the engine itself, so the declared type is
only a label: the browser goes through `@sqlite.org/sqlite-wasm`, and the
gateway's read path (`backend/internal/engine/sqlite_raw.go`) goes through the
same raw sqlite3 API rather than modernc's `database/sql` driver, which used to
turn a `DATE`/`TIMESTAMP`-declared `TEXT` value into a `time.Time`. A date
column therefore arrives as its stored text on both sides.

| Storage class | JSON | Rule, and the case worth teaching |
| --- | --- | --- |
| `INTEGER` | number | Within ±2^53−1 it is a JSON number; above that it is a decimal string, because a JSON number cannot hold it exactly. PostgreSQL `bigint` already follows this rule in `web/src/api/pglite.ts`. |
| `REAL` | number | `NaN` is reported as `null` (SQLite itself stores NaN as NULL, so that *is* the engine's answer); `Infinity`/`-Infinity` are reported as the strings `"Infinity"`/`"-Infinity"`, because JSON has no literal for them and rounding them to a number would fabricate a value. |
| `TEXT` | string | Verbatim. No ISO-8601 parsing, no numeric coercion: a date stays the same text it was stored as, and a numeric-looking string in a TEXT column stays a string. |
| `BLOB` | string | `\x` + lowercase hex, which is PostgreSQL `bytea`'s text form and the convention `pglite.ts` already uses. |
| `NULL` | `null` | SQL NULL, never the empty string and never zero. |
| *(a boolean handed over as a `bool`)* | number | A `BOOLEAN`-declared column stores `1`/`0`; it is reported as an integer and is never coerced to `true`/`false`. Neither adapter produces a boolean: the storage class is `INTEGER`, so this is the encoder's defensive answer for a driver that hands one over. |
| *(a date handed over as a `time.Time`)* | string | A `DATE`/`TIMESTAMP`-declared column stores ISO-8601 text and is reported **verbatim** by both adapters; the stored text is what a lesson compares. Neither side materialises a `time.Time`/`Date` (see the read-path note above), so the RFC3339 rule is only the encoder's defensive answer for a driver that hands one over: a version that reported a host `Date` instead would reintroduce the timezone shift the adapter-parity audit already found against PGlite. |

**Column labels.** A result column's `type` label is the *declared* type
verbatim (`sqlite3_column_decltype`: `INTEGER`, `TEXT`, `NUMERIC`, `VARCHAR(20)`)
and the fallback label `expression` for a column that never had one, such as
`COUNT(*)` or `price * 2`. The two must not be confused: a `DATE` column labels
`DATE` while every value in it is `TEXT`. A label is not a type.

**Verified by:** `dialect_test.go`: `TestEncodeValue` (a case per storage class
and per edge: the 2^53−1 boundary, `uint64` overflow, NaN, ±Infinity, an empty
BLOB, and the defensive `time.Time`/`bool` fallbacks),
`TestEncodeValueIgnoresAffinity` (the same value encodes identically under all
five affinity labels, `""` and a bogus label), `TestValueEncodingRulesAreDocumented`
(every encoding rule carries its reason, the boundary is 9007199254740991, the
blob prefix is `\x`). Storage classes are what the engine returns; the parity
audit in step 8 is where the engine is asked.

## The differences, one at a time

### 1. No SQLSTATE: classification is by message

`web/src/api/pglite.ts`'s `mapError` and the gateway's `classify` switch on
SQLSTATE. That table has no SQLite column, because the value does not exist.
The SQLite classifier is a message-pattern table, and the pattern table is data
(a profile), not code, so both languages read the same one.

What a course must teach: an error message is not an API. A lesson that says
"you will see `42P01`" cannot be written for this dialect; a lesson that says
"a missing table is a runtime error, not a syntax error" can, and holds in both.

*Verified by:* Table 1 above, `dialect_test.go::TestClassifyError`, and
`contracts/dialects/sqlite.json` → `error_classification`.

### 2. Dynamic typing and affinity

A PostgreSQL column has a type; a SQLite column has an **affinity**, applied
when a value is written, and afterwards the column may hold a value of any of
the five storage classes. The affinity is chosen by text-substring rules on the
declared type, which is why `DATE`, `BOOLEAN`, `NUMERIC` and `TIMESTAMP` all
select the *same* NUMERIC affinity:

| Affinity | Selected when the declared type contains |
| --- | --- |
| `INTEGER` | `INT` |
| `TEXT` | `CHAR`, `CLOB` or `TEXT` |
| `BLOB` | `BLOB`, or nothing at all |
| `REAL` | `REAL`, `FLOA` or `DOUB` |
| `NUMERIC` | anything else (the catch-all) |

The dataset exercises this honestly rather than hiding it: `customers.signup_date`
is declared `DATE`, which selects NUMERIC affinity, and the ISO-8601 text it
stores is *not* a numeric literal, so the value stays `TEXT`:

```sh
$ sqlite3 qc.db "SELECT signup_date, typeof(signup_date) FROM customers LIMIT 1"
2025-11-02|text
```

That is the "one column with a value whose class differs from the declared
affinity" case the mapping calls out, and it is a required row in
`mapping.json` (`customers.signup_date`, `mismatch: true`).

What a course must teach: the declared type is a hint to the engine, not a
promise to the reader. `SELECT typeof(x)` is the dialect's own answer to "what
is this, really", and `STRICT` tables (3.37+) are how you ask for the promise
back, one table at a time, and this dataset does not, so that the dynamic
behaviour is visible.

*Verified by:* `dialect_test.go::TestAffinity` (a case per class, plus
`VARCHAR(20)`, `DECIMAL(10,2)`, `DATE`, `TIMESTAMP`, `BOOLEAN`, the empty
declaration, and case-insensitivity), `TestAffinityRulesMatchTheProfile` (the
profile's own `declared` map is executed), and
`dataset-map-audit.mjs` (`affinity matches the SQLite algorithm` recomputes
every mapped column's affinity from its declared type).

### 3. Money has no fixed scale

`products.price` and `order_items.unit_price` are `numeric(10,2)` in
PostgreSQL. SQLite has no decimal type, and NUMERIC affinity stores a value in
its smallest lossless class, so the same column reports different classes for
different rows:

```sh
$ sqlite3 qc.db "SELECT group_concat(DISTINCT typeof(price)) FROM products"
integer,real
$ sqlite3 qc.db "SELECT price FROM products WHERE id = 1"
59          -- written as 59.00
```

The mapping records this as a divergence with a reason, including the
alternative that was **rejected**: declaring the column `TEXT` would preserve
`59.00` exactly, but a TEXT-affinity money column turns `WHERE price > 100`
into a lexicographic comparison: measured, it returns `'59.00'` and `'9.99'`, 
which would break every filtering lesson. NUMERIC keeps comparisons numeric and
gives up the scale; a course prints money with `printf('%.2f', price)`.

What a course must teach: exact decimal arithmetic does not exist here. Scale is
a formatting decision, `SUM` over these values returns a REAL, and a contract
that needs exact scale needs a cast, a tolerance, or a different storage plan
(integer minor units).

*Verified by:* `mapping.json` rows `products.price` and
`order_items.unit_price` (both `mismatch: true`, both with a reason);
`dataset-map-audit.mjs` refuses a mismatch without one.

### 4. `LIKE` is case-insensitive for ASCII

`LIKE` in SQLite is case-insensitive for ASCII by default, so `'ADA' LIKE 'ada'`
is true, and `ILIKE` does not exist. `GLOB` is the case-sensitive one.

```sh
$ sqlite3 :memory: "SELECT 'ADA' LIKE 'ada', 'ADA' GLOB 'ada'"
1|0
```

What a course must teach: a PostgreSQL course that relies on `LIKE` being
case-sensitive teaches a habit that silently gives different answers here. The
dialect-correct pair is `LIKE` for the ASCII-insensitive case (or
`UPPER(...) LIKE ...` for explicitness) and `GLOB`/`COLLATE BINARY` for the
sensitive one. `LIKE`'s insensitivity is ASCII-only, so it is not
`lower()`-equivalent for non-ASCII text.

*Verified by:* the probe above (measured, SQLite 3.31.1) and the profile's
`capabilities.like_case_insensitive_ascii = true` /
`capabilities.ilike = false`, whose notes state the rule; the engine assertion
belongs to step 8's parity audit (`dialect_test.go::TestProfileShape` holds the
capability, not the engine behaviour).

### 5. Dates are not a type

There is no `DATE`, `TIME`, `TIMESTAMP` or `INTERVAL` storage class, no
`DATE '…'` literal, no interval arithmetic, no `DATE_TRUNC`, and no `TO_CHAR`:

```sh
$ sqlite3 :memory: "SELECT DATE_TRUNC('month', '2025-11-02')"
Error: no such function: DATE_TRUNC
$ sqlite3 :memory: "SELECT TO_CHAR(1, '9')"
Error: no such function: TO_CHAR
$ sqlite3 :memory: "SELECT date('2025-11-02','start of month')"
2025-11-01
```

A date is ISO-8601 text with functions over it (`date()`, `strftime()`,
`julianday()`); the dialect's `DATE_TRUNC('month', d)` is
`strftime('%Y-%m-01', d)` or `date(d, 'start of month')`. The dataset stores
`2025-11-02` and `2026-01-04 10:12` verbatim, the same characters PostgreSQL
prints, so that a lesson comparing the two dialects compares text to text.

What a course must teach: date arithmetic is string-or-number arithmetic with
named modifiers, month ends are not special, and a "timestamp" is text that
sorts correctly only because ISO-8601 sorts lexicographically in this form.

*Verified by:* the probes above (measured, SQLite 3.31.1);
`mapping.json` rows `customers.signup_date` and `orders.ordered_at` (both
`mismatch: true`, stored class TEXT); `dialect_test.go::TestEncodeValue`'s
"dynamic typing: ISO-8601 text in a NUMERIC-affinity column"; and
`engine.TestSQLiteDateDeclaredColumnsArriveAsTheStoredText`
(`backend/internal/engine/sqlite_test.go`), which reads a `DATE`- and a
`TIMESTAMP`-declared column through the gateway and asserts the stored text: 
the adapter-parity audit compares the same two probes cell by cell.

### 6. No roles and no schemas

There is no `CREATE ROLE`, no `GRANT`, no `current_user`, no schema namespace
and no `search_path`. A SQLite database is one namespace, so the dataset tables
are unqualified and the PostgreSQL `dataset` schema has no counterpart. The
`app` schema's isolation, learner SQL cannot reach gateway metadata, has to be
enforced by the host (a separate file, `query_only`, an authorizer), not by the
engine's privilege model.

What a course must teach: `information_schema`, `pg_roles` and
`GRANT … ON SCHEMA` have no analogues; what a learner can see is what the host
handed them. The difference is a feature to name, not a gap to paper over:
"who may read this table" is a different question in the two dialects.

*Verified by:* the profile's `capabilities.roles = false` and
`capabilities.schemas = false`, with their notes, held by
`dialect_test.go::TestProfileShape`; the dataset's unqualified DDL is checked by
`dataset-map-audit.mjs`, which parses `CREATE TABLE <name>` without a schema
qualifier on the SQLite side.

### 7. No `EXPLAIN ANALYZE` and no cost-based plan output

`EXPLAIN ANALYZE` does not exist, and the token sequence is not even the same
statement:

```sh
$ sqlite3 :memory: "EXPLAIN ANALYZE SELECT 1"
Error: near "SELECT": syntax error
$ sqlite3 :memory: "EXPLAIN QUERY PLAN SELECT 1"
QUERY PLAN
`--SCAN CONSTANT ROW
```

`EXPLAIN QUERY PLAN` returns one text row per step. It says which scan or
search is used, but not estimated rows, not actual rows and not cost, so there
is nothing to compare against a threshold. M5's "read and improve a query plan"
capability cannot exist in this dialect, and the profile says so:
`capabilities.analyze = false`, `capabilities.plan = false`,
`capabilities.explain_query_plan = true`.

What a course must teach: an index's effect can be *seen* (the step changes
from `SCAN` to `SEARCH … USING INDEX`), but a plan cannot be *measured* the way
M5's lesson does. Any timing claim is the learner's stopwatch, not the engine's
numbers.

*Verified by:* the probes above (measured, SQLite 3.31.1) and the profile's
three capabilities with their notes, held by
`dialect_test.go::TestProfileShape`. The plan-shape assertion is step 8's;
nothing in this step runs `EXPLAIN` against SQLite.

### 8. `AUTOINCREMENT` vs `GENERATED`/`IDENTITY`

PostgreSQL generates identities with `GENERATED … AS IDENTITY` (or `serial`).
SQLite's nearest spelling is `INTEGER PRIMARY KEY AUTOINCREMENT`, and it is not
the same thing:

- `INTEGER PRIMARY KEY` already generates a rowid: the id *is* the rowid
  alias, with no keyword at all. `AUTOINCREMENT` only adds the "never reuse an
  id, even after the row is deleted" guarantee, and it pays for it with a
  `sqlite_sequence` table:

  ```sh
  $ sqlite3 :memory: "CREATE TABLE t(id INTEGER PRIMARY KEY AUTOINCREMENT, a TEXT); INSERT INTO t(a) VALUES ('x');
  > SELECT name FROM sqlite_master WHERE name='sqlite_sequence'"
  sqlite_sequence
  ```

This dataset uses neither: the seed supplies explicit ids exactly as the
PostgreSQL seed does, so ids are identical across the dialects and an
`INSERT … SELECT` of the seed is reproducible. The profile records
`capabilities.autoincrement = true` (the keyword exists) with the note that the
dataset does not use it.

What a course must teach: identity in SQLite is a rowid alias, not a sequence
object; `AUTOINCREMENT` is a durability-of-monotonicity choice with a cost, and
the dialect has no `GENERATED … AS IDENTITY` or `serial` at all.

*Verified by:* the probe above (measured, SQLite 3.31.1);
`mapping.json` rows `customers.id`/`products.id`/`orders.id`/`order_items.id`,
whose notes record the rowid-alias decision; `dialect_test.go::TestProfileShape`
holds the capability.

### 9. `PRAGMA` vs `SET`

PostgreSQL configures a session with `SET search_path = …`,
`SET statement_timeout = …`. SQLite has no `SET`; the equivalent is a
`PRAGMA`, which is also how you read engine internals:

```sh
$ sqlite3 :memory: "SET search_path = dataset"
Error: near "SET": syntax error
$ sqlite3 qc.db "SELECT name, type FROM pragma_table_info('customers')"   -- table-valued form, usable in a JOIN
```

The pieces that matter for this app: `PRAGMA foreign_keys = ON` (foreign keys
are **not** enforced by default), `PRAGMA query_only = ON` (how the anonymous
read-only dataset stays read-only), `PRAGMA journal_mode` (WAL adds `-wal`/`-shm`
sidecars a reset has to handle), and `PRAGMA synchronous`. `docker`-free
operation and the M3 environment story are unaffected: the same `POST /query`
contract carries the statement.

What a course must teach: session configuration is a `PRAGMA` call, some
settings are per-connection rather than per-database, and `foreign_keys` being
off by default is why `orders.customer_id`'s `REFERENCES` clause is a
declaration here but an enforcement in PostgreSQL (see the mapping note on that
row).

*Verified by:* the probes above (measured, SQLite 3.31.1);
`mapping.json` rows `orders.customer_id` and `order_items.order_id`, whose notes
state the `PRAGMA foreign_keys` difference. The adapter's own pragmas are
step 4/5 work and are not asserted here.

### 10. `rowid` and `WITHOUT ROWID`

Every ordinary SQLite table has a hidden 64-bit `rowid` unless it is declared
`WITHOUT ROWID`, and `INTEGER PRIMARY KEY` is an alias for it. Without that
alias the rowid is addressable but not stable across a `VACUUM`:

```sh
$ sqlite3 :memory: "CREATE TABLE w(a TEXT PRIMARY KEY) WITHOUT ROWID; SELECT rowid FROM w"
Error: no such column: rowid
```

What a course must teach: `rowid` is a physical address, not a key: 
`ORDER BY rowid` is insertion order *today*, and it is exactly the
"answers must not depend on insertion order" rule §7.2 already states. A lesson
that wants an identity should declare one; a lesson that wants a clustered
storage layout can say `WITHOUT ROWID`, and the profile records both
(`capabilities.rowid_tables`, `capabilities.without_rowid`).

*Verified by:* the probe above (measured, SQLite 3.31.1);
`mapping.json` rows for the four `id` columns; `TestProfileShape` holds the two
capabilities.

## When SQLite is the right choice

Stated as the product decision it is (ADR 0004 records the argument in full):

- **A second dialect to teach.** The point of M6 is a course that teaches
  *where SQLite differs*: affinity, `rowid`, `PRAGMA`, text dates, the absence
  of roles and plans. That course needs a real SQLite, not a simulation.
- **A generic `SELECT`-only tutor**, where the product is
  "learn `SELECT`/`JOIN`/`GROUP BY` against a toy table": the file is the
  database, no server, no download, no roles, no pool: strictly less machinery.
- **Anywhere the whole database should be one file.** Copy it, hand it to the
  learner, delete it. A per-learner workspace costs a file copy rather than a
  `CREATE DATABASE … TEMPLATE`, which is the one place SQLite is *cheaper* than
  the PostgreSQL design the app already runs (ADR 0003, ADR 0004).
- **Air-gapped or tiny installs**, where a 60 MiB PostgreSQL download is the
  objection: a single-file engine removes it, at the cost of the dialect
  differences above.

It is the wrong choice when the lesson *is* the PostgreSQL feature: plan
reading with actual row counts (M5), `DATE_TRUNC`/`INTERVAL`/`TO_CHAR` date
work, `DISTINCT ON`, `ILIKE`, schemas and roles, or column types that are
guarantees rather than hints. That is why PostgreSQL stays the default and
SQLite is a second dialect rather than a replacement, and why a chapter names
its dialect up front.

## What this step does not prove

- **No SQLite engine runs in this step.** Every engine-behaviour claim above was
  measured with the local `sqlite3` CLI (3.31.1, older than the versions the
  adapters will pin) and is *described* by the profile and its tests. Step 4
  (gateway, `modernc.org/sqlite`) and step 6 (browser wasm) turn the
  descriptions into assertions; step 8 runs the same statements through both
  adapters.
- **No value crosses an adapter.** `EncodeValue` is tested against Go values the
  driver is *expected* to hand back (`int64`, `float64`, `string`, `[]byte`,
  `time.Time`, `bool`). What a SQLite driver materialises for a DATE column is
  now settled: neither adapter does; both report the stored text (Table 2 and
  the read-path note above), and the `time.Time` rule remains only the
  encoder's defensive answer, so a driver that *did* materialise one could not
  silently produce a `Date`.
- **No feature version is pinned.** `window_functions` (3.25), the aggregate
  `FILTER` (3.30), `RETURNING` (3.35), `STRICT` (3.37), JSON1 (3.38) and
  `RIGHT`/`FULL JOIN` (3.39) are recorded in the profile as available; the
  version floor the app requires is decided in steps 4 and 6, not here.
- **The SQLite dataset is not embedded in Go yet.** `backend/internal/dataset/dataset.go`
  still embeds only the PostgreSQL pair; `schema.sqlite.sql` and
  `seed.sqlite.sql` are checked by `scripts/dataset-map-audit.mjs` and read as
  text, and the adapter work that adds them to the `go:embed` set is step 4.
- **Nothing in this step changes runtime behaviour.** No lesson, contract,
  adapter or dataset row visible to a learner today changes: the PostgreSQL
  dataset is untouched, and the SQLite files are new.
