# Adapter parity

Querycraft runs the same lesson SQL on two adapters: the browser engine and the
Go gateway. The parity audit checks that both return the same result. It compares
each row and each value, not the query text.

The audit is `scripts/adapter-parity-audit.mjs`.

## What the audit proves

Every statement a learner can run is executed twice: the reference solution of
each exercise, each worked example, and the probes the audit adds. One run uses
the browser engine, which is PostgreSQL compiled to WebAssembly through PGlite.
The other uses the Go gateway. The lesson text is never rewritten for either
side.

The comparison uses the project's result rule, not string equality of the JSON:

- Status, error code, column names, column types, row count, and the `truncated`
  flag must match exactly.
- Rows compare cell by cell. Numeric strings and numbers compare numerically.
  NULL equals only NULL. Text is never normalized.
- Row order is compared as written when the statement has its own `ORDER BY`. It
  is compared as a multiset when the statement does not.
- A difference the rule still calls "the same result" is reported separately as
  a representation finding. Values agree and the JSON shape does not. It cannot
  hide as a match.
- Both responses are validated against `contracts/query-result.schema.json`. An
  error code outside the closed enum fails on its own.

The comparison runs signed in on the gateway, because learner SQL can write. Both
sides are then the same kind of thing: the browser's own database on one side and
the learner's per-account environment on the other. In the PostgreSQL half, after
every statement that writes, both workspaces are restored, so probe order cannot
manufacture a difference.

The anonymous path is checked separately as the property it is. An anonymous
caller must be refused a write, must leave the shared dataset untouched, and must
be told it has no workspace rather than be given one.

A course setup section checks the other half of the rule. A stored course's
`setup.statements` are applied after the template is copied, so the learner's
database is the one the course's exercises were verified in. The browser adapter
applies exactly the statements it is handed and reports how many. The gateway
resolves the course owner-scoped, reports `setup_statements: 0` when no course is
named, and refuses a named but unknown course instead of ignoring it.

## Run it

```sh
make audit-parity                  # both dialects, gateway on a free port
node scripts/adapter-parity-audit.mjs                     # PostgreSQL
node scripts/adapter-parity-audit.mjs --dialect sqlite    # SQLite
node scripts/adapter-parity-audit.mjs --markdown          # print the comparison table
node scripts/adapter-parity-audit.mjs --include-volatile  # compare EXPLAIN, random(), now()
QC_GATEWAY=http://127.0.0.1:18077 node scripts/adapter-parity-audit.mjs
QC_SKIP_PGLITE=1 node scripts/adapter-parity-audit.mjs    # gateway half only
```

The audit is part of `make test`. `make audit-parity` builds the gateway once and
runs both dialect halves. The existing knobs (`QC_GO`, `QC_BACKEND`, `QC_BIN`,
`QC_GATEWAY`, `QC_SKIP_PGLITE`) apply to each half.

## What is not compared

Each exclusion prints its reason. Nothing is normalized away silently.

- Display-only listings. A `syntax` block is a static listing with no editor and
  no Run button, so a learner can never execute one. One listing is a deliberate
  two-statement contrast, written to be read. Comparing the adapters' execution
  of it would measure a behavior the UI does not have. The audit asserts the rule
  that does bind instead: every runnable block is a single statement. A runnable
  block that is a script fails by name.
- Volatile statements. `EXPLAIN`, `EXPLAIN ANALYZE`, `random()`, `now()`,
  `current_*`, `clock_timestamp()`, and `pg_sleep()` cannot be equal across two
  runs. `--include-volatile` compares them for manual inspection.
- Starters. These are scaffold text full of placeholder comments, not statements.

## What the comparison catches

The audit exists because a defect in one of these areas is hard to see by
reading lesson text:

- A numeric value loses its scale. PostgreSQL `numeric` must reach the learner as
  the server's own text, so `118.00` does not arrive as `118`.
- A naive timestamp moves by the host's UTC offset. A `timestamp` without a zone
  must keep its wall clock. The audit runs under two host timezones and requires
  the same wall clock under both.
- A missing relation or column is classified as a syntax error. `42P01` and
  `42703` are runtime errors. Both adapters share one error-classification table,
  and tests in both languages pin it.

The fix for the first two is the same rule: emit PostgreSQL's own text for
`numeric` and the date and time family. The gateway asks the server for the text
form. The browser adapter tells PGlite not to parse those OIDs into a `Date`. The
canonical column-type spelling is `pg_type.typname` (`int4`, `int8`, `bool`),
because neither adapter has to translate it.

## The SQLite half

The SQLite half uses the same instrument with `--dialect sqlite`. It compares the
browser's SQLite adapter (`web/src/api/sqlite.ts` over
`web/src/api/sqliteEngine.ts`, the official `@sqlite.org/sqlite-wasm` build)
against the gateway's SQLite engine.

The browser audit bundles the shipping adapter with esbuild and runs it in Node.
Node has no `Worker`, so the adapter takes its documented main-thread fallback.
The audit's esbuild plugin answers the `?url` import of `sqlite3.wasm` with the
absolute filesystem path, which is what `sqlite3InitModule` needs under Node.

The gateway side is reached with `POST /query` and `{"dialect":"sqlite"}`, driven
as the browser drives it. The run signs a fresh learner in first, because a
signed-in learner gets a per-account SQLite file. That is the same kind of
writable workspace as the browser side. The anonymous read-only path is a
different kind of thing and is not what this gate compares.

The two sides are different builds. The browser engine is the wasm build and the
gateway engine is the pure-Go `modernc` build, so their `engine` strings differ by
construction. The audit prints both strings as information and never compares
them. What it compares is the contract: `dialect`, status, error code, column
labels, row count, `truncated`, and cell values.

Column types use the declared type verbatim, and `expression` for a column that
has no declared type. Both sides report `sqlite3_column_decltype`. The dataset
authors its declarations in upper case, so the labels compare as exact strings.

Three SQLite exclusions print a reason, still execute on both sides, and verify
what they can:

- `EXPLAIN QUERY PLAN` returns the planner's own text. It is a property of the
  build and its statistics.
- `rowid` is a physical address, not a key. Each adapter has its own file and its
  own insertion history.
- Storage-layer pragmas such as `PRAGMA journal_mode`, `page_size`, `page_count`,
  `freelist_count`, and `integrity_check` read facts about the file. The browser
  workspace is in memory and the gateway workspace is a file on disk, so the
  values differ. The exclusion is verified: every declared column in such a
  statement must still agree.

A `DATE` or `TIMESTAMP`-declared column is not excluded. Both adapters report the
stored text verbatim, so the declared type is only a label and no `time.Time` is
materialized. The date probes compare cell by cell.

## What this does not prove

- The audit compares the statements the curriculum contains. New lesson SQL is
  covered when it is added. Nothing stops a future block from being written
  against one engine only, which is why the single-statement rule and the type
  check are enforced rather than advisory.
- It uses the commerce dataset at its current seed. A dataset change moves both
  sides together, because the schema and seed have one source of truth. A change
  that only one adapter loads shows up as a row-count or value divergence, not as
  a clear error.
- It does not compare timing. It does not compare `EXPLAIN` output, because plans
  are a property of the engine and its statistics.
- Running PGlite under Node exercises the adapter's main-thread path, not its
  Worker path. The Worker deadline and the blocked-worker fallback have their own
  tests. This audit is about values.
