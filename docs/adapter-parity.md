# Adapter parity: the same lesson on both engines

Design source: `docs/technical-design.md` §14 (M3 exit gate: *"The same lesson
content runs against both adapters unchanged"*), §4.2 (QueryResult and the
closed error codes), §6.1 (the value-comparison rule), §5.4 (Adapter B).
Instrument: `scripts/adapter-parity-audit.mjs`.

## What it proves

Every statement a learner can actually run, each exercise's reference solution
and each worked example, is executed twice: once by the browser engine
(`@electric-sql/pglite`, the real PostgreSQL compiled to WebAssembly) and once by
the Go gateway, and the two results must agree. The lesson text is never
rewritten for either side; if they disagree, the product disagrees with itself.

"The same result" is judged by the project's own §6.1 rule
(`web/src/api/compare.ts`, mirrored by `backend/internal/eval/compare.go`), not
by string equality of the JSON:

- status, error code, column names, column types, row count and the `truncated`
  flag must match exactly;
- rows compare cell by cell, numeric strings and numbers compare numerically,
  NULL only equals NULL, text is never normalised;
- row order is compared as written when the statement has its own `ORDER BY`,
  and as a multiset when it does not;
- a difference §6.1 still calls "the same result" is reported separately as a
  **representation** finding (values agree, JSON shape does not) rather than as
  a match, so it cannot hide;
- both responses are validated against `contracts/query-result.schema.json`, and
  any error code outside the closed enum is a failure on its own.

Because ADR 0003 made learner SQL writable, the comparison runs **signed in** on
the gateway so both sides are the same kind of thing: the browser's own database
on one side, the learner's per-account environment database copied from the
sealed template on the other. After every statement that writes, **both**
workspaces are restored, so probe order can never manufacture a difference. The
anonymous path is then checked separately, as the property it is, an anonymous
caller must be refused a write, must leave the shared dataset untouched, and must
be told it has no workspace rather than be given one.

A **course setup** section (M9) checks the other half of that rule: a stored
course's `setup.statements` are re-applied after the template is copied, so the
learner's database is the one the course's exercises were verified in. The
browser adapter applies exactly the statements it is handed and reports how many;
the gateway resolves the course owner-scoped, reports `setup_statements: 0` when
no course is named, and refuses a named-but-unknown course instead of ignoring
it. The gateway's applied path is proved in Go
(`backend/internal/httpapi/environment_setup_test.go`, under `go test ./...`).

## Running it

```sh
make audit-parity                 # builds the gateway, boots it on a free port, runs both engines
node scripts/adapter-parity-audit.mjs --markdown   # the tables below, for pasting into docs/
node scripts/adapter-parity-audit.mjs --include-volatile   # compare EXPLAIN/random()/now() as well
QC_GATEWAY=http://127.0.0.1:18077 node scripts/adapter-parity-audit.mjs   # against a gateway you run
QC_SKIP_PGLITE=1 node scripts/adapter-parity-audit.mjs    # gateway half only; every statement is a skip, never a pass
```

It is part of `make test`.

## What is deliberately not compared

Each exclusion is printed with its reason; nothing is normalised away silently.

- **Display-only listings.** A `syntax` block is rendered by
  `LessonScreen` as `<pre className="syntaxblock">{b.sql}</pre>`, a static
  listing with no editor and no Run button, so a learner can never execute one.
  One of them (`q3 syntax 1`) is a deliberate two-statement contrast, "not safe"
  against "safe", written to be *read*. Comparing the adapters' execution of a
  listing would measure a behaviour the UI does not have. What the audit asserts
  instead is the rule that does bind: **every runnable block is a single
  statement** (submissions are one statement), and a runnable block that is a
  script fails the audit by name.
- **Volatile statements**: `EXPLAIN`, `EXPLAIN ANALYZE`, `random()`, `now()`,
  `current_*`, `clock_timestamp()`, `pg_sleep()` and friends cannot be equal
  across two runs by construction. `--include-volatile` compares them anyway for
  manual inspection.
- **Starters**: scaffold text full of `-- placeholder` comments, not statements.

## The defect this found

Three real divergences, all fixed in the product rather than in the check:

1. The gateway emitted `numeric` as a JSON number, so `118.00` arrived as `118`: 
   the learner's own price lost its scale.
2. A naive `timestamp` was decoded into a time value and re-rendered, moving the
   wall-clock time by the host's UTC offset (`10:30` became `05:00` under
   `Asia/Calcutta`); the browser adapter had the same class of problem because
   PGlite materialises `timestamp` as a JS `Date`.
3. The gateway classified a missing relation (`42P01`) and a missing column
   (`42703`) as `QUERY_SYNTAX_ERROR` by matching the `42` SQLSTATE class prefix,
   where the design files "missing relation" under `QUERY_RUNTIME_ERROR`, and
   the browser adapter already said runtime, so one mistake was described two
   ways depending on which engine ran the lesson.

The fix on both sides is the same rule: **emit PostgreSQL's own text** for
`numeric` and the date/time family (the gateway asks the server for the text
form; the browser adapter tells PGlite not to parse those OIDs into a `Date`),
and share one error-classification table, pinned by tests in both languages.
The canonical column-type spelling is `pg_type.typname` (`int4`, `int8`, `bool`),
because that is what design §4.2's own example uses and what neither adapter has
to translate.

The timezone half is verified under two host timezones in the same run: the
naive `timestamp` literal reads the same wall clock whether `TZ` is `UTC` or
`Asia/Kolkata`, on both engines.

## The live result

Recorded 2026-10-14 on this machine (PostgreSQL 18.3; PGlite in-process under
Node, which uses the adapter's documented main-thread fallback because Node has
no `Worker`):

```
statements compared:  61
match:                61
representation:       0 (same values under §6.1, different JSON shape)
diverge:              0
excluded (volatile):  1
multi-statement:      0
display-only listings: 22 (never executed by the UI)
```

| statement | status web | status gateway | rows web | rows gateway | verdict |
| --- | --- | --- | --- | --- | --- |
| ex.m1.demo | succeeded | succeeded | 5 | 5 | match |
| ex.1.1 | succeeded | succeeded | 5 | 5 | match |
| ex.m2.demo | succeeded | succeeded | 5 | 5 | match |
| ex.2.1 | succeeded | succeeded | 79 | 79 | match |
| ex.m3.demo | succeeded | succeeded | 5 | 5 | match |
| ex.3.1 | succeeded | succeeded | 5 | 5 | match |
| ex.m4.demo | succeeded | succeeded | 5 | 5 | match |
| ex.4.1 | succeeded | succeeded | 6 | 6 | match |
| ex.m5.demo | succeeded | succeeded | 6 | 6 | match |
| ex.5.1 | succeeded | succeeded | 20 | 20 | match |
| ex.5.2 | succeeded | succeeded | 10 | 10 | match |
| ex.m6.demo | succeeded | succeeded | 8 | 8 | match |
| ex.6.1 | succeeded | succeeded | 5 | 5 | match |
| ex.m7.demo | succeeded | succeeded | 20 | 20 | excluded (volatile) |
| ex.7.1 | succeeded | succeeded | 5 | 5 | match |
| ex.m8.demo | succeeded | succeeded | 5 | 5 | match |
| ex.8.1 | succeeded | succeeded | 5 | 5 | match |
| ex.m9.demo | succeeded | succeeded | 5 | 5 | match |
| ex.m9.1 | succeeded | succeeded | 6 | 6 | match |
| ex.j1.demo | succeeded | succeeded | 4 | 4 | match |
| ex.j1.1 | succeeded | succeeded | 80 | 80 | match |
| ex.j2.demo | succeeded | succeeded | 4 | 4 | match |
| ex.j2.1 | succeeded | succeeded | 30 | 30 | match |
| ex.j3.demo | succeeded | succeeded | 3 | 3 | match |
| ex.j3.1 | succeeded | succeeded | 19 | 19 | match |
| ex.q1.demo | succeeded | succeeded | 4 | 4 | match |
| ex.q1.1 | succeeded | succeeded | 9 | 9 | match |
| ex.q2.demo | succeeded | succeeded | 4 | 4 | match |
| ex.q2.1 | succeeded | succeeded | 20 | 20 | match |
| ex.q3.demo | succeeded | succeeded | 4 | 4 | match |
| ex.q3.1 | succeeded | succeeded | 7 | 7 | match |
| ex.q4.demo | succeeded | succeeded | 3 | 3 | match |
| ex.q4.1 | succeeded | succeeded | 7 | 7 | match |
| ex.f1.demo | succeeded | succeeded | 4 | 4 | match |
| ex.f1.1 | succeeded | succeeded | 5 | 5 | match |
| ex.f2.demo | succeeded | succeeded | 7 | 7 | match |
| ex.f2.1 | succeeded | succeeded | 4 | 4 | match |
| ex.f3.demo | succeeded | succeeded | 3 | 3 | match |
| ex.f3.1 | succeeded | succeeded | 3 | 3 | match |
| ex.f4.demo | succeeded | succeeded | 1 | 1 | match |
| ex.f4.demo2 | succeeded | succeeded | 7 | 7 | match |
| ex.f4.1 | succeeded | succeeded | 4 | 4 | match |
| ex.f5.demo | succeeded | succeeded | 5 | 5 | match |
| ex.f5.1 | succeeded | succeeded | 7 | 7 | match |
| ex.f6.demo | succeeded | succeeded | 5 | 5 | match |
| ex.f6.1 | succeeded | succeeded | 6 | 6 | match |
| ex.f7.demo | succeeded | succeeded | 3 | 3 | match |
| ex.f7.1 | succeeded | succeeded | 4 | 4 | match |
| probe unknown relation | failed | failed | 0 | 0 | match |
| probe unknown column | failed | failed | 0 | 0 | match |
| probe syntax error | failed | failed | 0 | 0 | match |
| probe division by zero | failed | failed | 0 | 0 | match |
| probe bad cast | failed | failed | 0 | 0 | match |
| probe write (writable workspace) | succeeded | succeeded | 0 | 0 | match |
| probe number types | succeeded | succeeded | 20 | 20 | match |
| probe date column | succeeded | succeeded | 30 | 30 | match |
| probe timestamp column | succeeded | succeeded | 80 | 80 | match |
| probe null types | succeeded | succeeded | 1 | 1 | match |
| probe literal types | succeeded | succeeded | 1 | 1 | match |
| probe bigint aggregate | succeeded | succeeded | 1 | 1 | match |
| probe numeric average | succeeded | succeeded | 1 | 1 | match |
| probe empty result | succeeded | succeeded | 0 | 0 | match |

adapter-parity-audit: OK (4 skipped)

## What this does not prove

- It compares the statements the curriculum contains today. New lesson SQL is
  covered the moment it is added, but nothing stops a future block from being
  written against one engine only, which is why the runnable-single-statement
  rule and the type-name check are enforced rather than advisory.
- It uses the commerce dataset at its current seed. A dataset change moves both
  sides together (the schema and seed have one source of truth,
  `backend/internal/dataset/`), but a change that only one adapter loads would
  show up here as a row-count or value divergence rather than as a clear error.
- It does not compare *timing*, and it does not compare `EXPLAIN` output: plans
  are a property of the engine and its statistics, not of the contract (M5 owns
  plans).
- Running PGlite under Node exercises the adapter's main-thread path, not its
  Worker path. The Worker deadline and the "worker blocked" fallback have their
  own tests; this audit is about values.

## The SQLite dialect (`--dialect sqlite`)

`docs/technical-design.md` / `docs/m6-implementation-plan.md` add a second
dialect to the same product: a learner can write SQLite as well as PostgreSQL.
The parity gate is the same idea with a different pair of engines, so it is the
same instrument with `--dialect sqlite` (M6 step 8). The PostgreSQL half is
untouched and is still the default:

```sh
node scripts/adapter-parity-audit.mjs                     # postgresql (the M3 gate)
node scripts/adapter-parity-audit.mjs --dialect sqlite    # sqlite (the M6 gate)
make audit-parity                                         # both, and red if either is red
```

`make audit-parity` builds the gateway once and then runs both halves; the
existing knobs (`QC_GO`, `QC_BACKEND`, `QC_BIN`, `QC_GATEWAY`,
`QC_SKIP_PGLITE`) apply to each half unchanged.

### Adapter A and Adapter B for this dialect

- **Adapter A (web)** is the browser's own SQLite adapter,
  `web/src/api/sqlite.ts` over `web/src/api/sqliteEngine.ts`
  (the official `@sqlite.org/sqlite-wasm` build). In the app it runs in a Web
  Worker; the audit bundles the shipping adapter with esbuild and runs it in
  Node, where it takes its documented main-thread fallback because Node has no
  `Worker`. The audit's esbuild step carries a small plugin for
  `import sqliteWasmUrl from '@sqlite.org/sqlite-wasm/sqlite3.wasm?url'`, which
  esbuild cannot resolve on its own: the plugin answers the `?url` import with
  the **absolute filesystem path** of the wasm file, which is what
  `sqlite3InitModule`'s `locateFile` needs under Node.
- **Adapter B (gateway)** is the Go gateway's SQLite engine
  (`backend/internal/engine/sqlite.go`, pure-Go modernc) reached with
  `POST /query` and `{"dialect":"sqlite"}`, driven exactly as the browser drives
  it. The run signs a fresh learner in first, because a signed-in learner gets a
  per-account SQLite file, which is the same kind of writable workspace as the
  browser side; the anonymous read-only path is a different kind of thing and is
  not what this gate compares.

### The two sides are different builds: the semantics are the contract

The browser engine is the **wasm** build of SQLite and the gateway engine is the
pure-Go `modernc` build, so their `engine` strings differ by construction
(measured: `SQLite 3.53.4` in the browser, `SQLite 3.50.3` on the gateway). The
audit prints both strings as *information* and **never compares them**: a
version-string difference is expected and is never a finding. What is compared
is the contract: `dialect`, status, error code, column labels, the row count,
the `truncated` flag and the cell values, under the project's own §6.1 rule
(`web/src/api/compare.ts`). Two different builds that answer the same lesson SQL
with the same rows are what "the same lesson on both engines" means.

### Canonical column-type spelling for SQLite

Column *types* are compared through a canonical table, as on the PostgreSQL
side. For SQLite the canonical spelling is the **declared type verbatim**, and
`expression` for a column that has no declared type: the rule stated in
`contracts/dialects/sqlite.json` under
`value_encoding.column_type_label` (and mirrored by
`canonicalSqliteType()` in the audit). Both sides report the declared type
verbatim (`sqlite3_column_decltype`), and the dataset authors those declarations
upper-case (`INTEGER`, `TEXT`, `NUMERIC`, `DATE`, `TIMESTAMP`, `BOOLEAN`), so
there is no case translation to apply: the labels are compared as exact strings.

### What is deliberately not compared (SQLite)

Each exclusion is printed with its reason, is still **executed** on both sides,
and, where its shape allows it, carries a *verified* claim, so an exclusion
can never hide a second difference. All three are specific to SQLite (the first
mirrors the PostgreSQL half's own `EXPLAIN` exclusion):

- **`EXPLAIN QUERY PLAN`** returns the planner's own text (which scan or
  search each step chose), a property of the build and its statistics, not a
  value the contract covers. The two sides are different SQLite builds, so their
  plans are not required to agree. The PostgreSQL half excludes `EXPLAIN` for
  the same reason.
- **`rowid`** is a physical address, not a key: SQLite documents it as unstable
  across `VACUUM`, and each adapter's database is its own file with its own
  insertion history, so the two are not required to number rows the same way.
  The label differs too (the wasm build reports `INTEGER` from
  `sqlite3_column_decltype`, the Go driver reports `expression`, because
  `decltype` answers for a declared column and `rowid` is implicit). Both are
  properties of the file, like a plan.
- **`storage-layer-pragma`** (SQLite-only): `PRAGMA journal_mode`, `page_size`,
  `page_count`, `freelist_count`, the cache and locking settings, `encoding`,
  `integrity_check`, `data_version`, `wal_checkpoint` and the rest read a fact
  about the database *file*, and the two adapters are two files: the browser's
  workspace is an in-memory database (ADR 0007, `journal_mode = memory`) while
  the gateway's is a file on disk (`journal_mode = delete`), and the two builds
  ship different defaults (measured `page_size`: `8192` web, `4096` gateway,
  with `page_count` and anything computed from it moving with it). The exclusion
  is *verified*: every column in such a statement that is not a bare
  `expression` must still agree, so a real divergence in stored data cannot hide
  under it. It also recognises the table-valued form (`pragma_journal_mode()`,
  `pragma_page_size()`) the schema lessons use, not only the bare `PRAGMA x`
  spelling. A *data* pragma the lessons read for their schema
  (`pragma_table_info`, `pragma_foreign_key_list`) is **not** excluded and is
  compared normally.
A `DATE`/`TIMESTAMP`-declared column is **not** excluded. Both adapters report
the stored text verbatim: the browser through `sqlite3_column_text`, the
gateway through the same raw sqlite3 API on its read path
(`backend/internal/engine/sqlite_raw.go`), so the declared type is only a label
and no `time.Time` is ever materialised. The two date probes are ordinary
statements the audit compares cell by cell.

### Measured result

Run against the embedded gateway, `--dialect sqlite --markdown`:

```
statements compared:    51
match:                  51
representation:         0 (same values under §6.1, different JSON shape)
diverge:                0
excluded (driver rule): 6
multi-statement:        0

adapter-parity-audit: OK (4 skipped)
```

The 57 statements executed (51 compared, 6 excluded) are the real lesson SQL of
every `dialect: 'sqlite'` chapter (`sqlite-essentials` s1–s7,
`sqlite-in-production` r1–r7, `x1`–`x7`), each exercise's reference solution
and each worked example, 42 statements, plus 15 probes for the paths a curated
lesson never hits: syntax error, incomplete input, no such
table/column/function, a UNIQUE and a NOT NULL constraint failure, the storage
classes, division by zero, an empty result, a declared `DATE` and `TIMESTAMP`
column, the 500-row cap and the 5 s deadline.

| statement | status web | status gateway | rows web | rows gateway | verdict |
| --- | --- | --- | --- | --- | --- |
| ex.s1.demo | succeeded | succeeded | 10 | 10 | match |
| ex.s1.1 | succeeded | succeeded | 5 | 5 | match |
| ex.s2.demo | succeeded | succeeded | 2 | 2 | match |
| ex.s2.1 | succeeded | succeeded | 10 | 10 | match |
| ex.s3.demo | succeeded | succeeded | 1 | 1 | excluded (driver rule) |
| ex.s3.1 | succeeded | succeeded | 3 | 3 | excluded (driver rule) |
| ex.s4.demo | succeeded | succeeded | 5 | 5 | match |
| ex.s4.1 | succeeded | succeeded | 7 | 7 | match |
| ex.s5.demo | succeeded | succeeded | 1 | 1 | match |
| ex.s5.1 | succeeded | succeeded | 1 | 1 | match |
| ex.s6.demo | succeeded | succeeded | 5 | 5 | match |
| ex.s6.1 | succeeded | succeeded | 4 | 4 | match |
| ex.s7.demo | succeeded | succeeded | 1 | 1 | match |
| ex.s7.1 | succeeded | succeeded | 4 | 4 | match |
| ex.r1.demo | succeeded | succeeded | 1 | 1 | excluded (driver rule) |
| ex.r1.1 | succeeded | succeeded | 1 | 1 | excluded (driver rule) |
| ex.r2.demo | succeeded | succeeded | 6 | 6 | match |
| ex.r2.1 | succeeded | succeeded | 5 | 5 | match |
| ex.r3.demo | succeeded | succeeded | 2 | 2 | match |
| ex.r3.1 | succeeded | succeeded | 5 | 5 | match |
| ex.r4.demo | succeeded | succeeded | 2 | 2 | match |
| ex.r4.1 | succeeded | succeeded | 3 | 3 | match |
| ex.r5.demo | succeeded | succeeded | 4 | 4 | match |
| ex.r5.1 | succeeded | succeeded | 4 | 4 | match |
| ex.r6.demo | succeeded | succeeded | 1 | 1 | match |
| ex.r6.1 | succeeded | succeeded | 1 | 1 | match |
| ex.r7.demo | succeeded | succeeded | 1 | 1 | excluded (driver rule) |
| ex.r7.1 | succeeded | succeeded | 1 | 1 | excluded (driver rule) |
| ex.x1.demo | succeeded | succeeded | 1 | 1 | match |
| ex.x1.1 | succeeded | succeeded | 3 | 3 | match |
| ex.x2.demo | succeeded | succeeded | 1 | 1 | match |
| ex.x2.1 | succeeded | succeeded | 4 | 4 | match |
| ex.x3.demo | succeeded | succeeded | 1 | 1 | match |
| ex.x3.1 | succeeded | succeeded | 5 | 5 | match |
| ex.x4.demo | succeeded | succeeded | 1 | 1 | match |
| ex.x4.1 | succeeded | succeeded | 6 | 6 | match |
| ex.x5.demo | succeeded | succeeded | 1 | 1 | match |
| ex.x5.1 | succeeded | succeeded | 1 | 1 | match |
| ex.x6.demo | succeeded | succeeded | 6 | 6 | match |
| ex.x6.1 | succeeded | succeeded | 1 | 1 | match |
| ex.x7.demo | succeeded | succeeded | 4 | 4 | match |
| ex.x7.1 | succeeded | succeeded | 5 | 5 | match |
| probe sqlite syntax error | failed | failed | 0 | 0 | match |
| probe sqlite incomplete input | failed | failed | 0 | 0 | match |
| probe sqlite no such table | failed | failed | 0 | 0 | match |
| probe sqlite no such column | failed | failed | 0 | 0 | match |
| probe sqlite no such function | failed | failed | 0 | 0 | match |
| probe sqlite unique constraint | failed | failed | 0 | 0 | match |
| probe sqlite not null constraint | failed | failed | 0 | 0 | match |
| probe sqlite storage classes | succeeded | succeeded | 20 | 20 | match |
| probe sqlite division by zero | succeeded | succeeded | 1 | 1 | match |
| probe sqlite empty result | succeeded | succeeded | 0 | 0 | match |
| probe sqlite word-count aggregate | succeeded | succeeded | 1 | 1 | match |
| probe sqlite declared DATE column | succeeded | succeeded | 30 | 30 | match |
| probe sqlite declared TIMESTAMP column | succeeded | succeeded | 80 | 80 | match |
| probe sqlite row cap (500) | succeeded | succeeded | 500 | 500 | match |
| probe sqlite deadline (5 s) | failed | failed | 0 | 0 | match |
