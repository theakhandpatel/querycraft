# ADR 0007 — The browser's SQLite engine: `@sqlite.org/sqlite-wasm` 3.53.4, chosen by `sqlite3_column_decltype`

- **Status:** accepted
- **Date:** 2026-10-15
- **Deciders:** project owner + agent session
- **Question:** M6 needs a second engine *in the page* — SQLite compiled to WebAssembly,
  running next to PGlite, obeying the same deadline, the same caps and the same honesty
  rules, and grading the same answers as the gateway's `modernc.org/sqlite` executor. Four
  builds are credible. Which one, and what does choosing it commit the adapter to?

Settles **D12** in `docs/technical-design.md` §15. Extends ADR 0004 (why the app is
PostgreSQL-first and SQLite is a second dialect) to the browser half of that dialect;
ADR 0006 settles the *gateway* half's isolation. Every number below is measured on this
machine (Linux, x86-64, Node 20.17 for the harness, Chromium for the in-page checks) and
kept in `/tmp/sqlite-spike/RESULTS.json` — the harness is `harness/*.mjs` beside it. The
in-page side was re-measured against the shipping adapter after the code landed; those
numbers are the ones quoted as "in the page".

## Candidates

| | `sql.js` 1.15.0 | `wa-sqlite` 1.0.0 | `@sqlite.org/sqlite-wasm` 3.41.2 | `@sqlite.org/sqlite-wasm` 3.53.4 |
| --- | --- | --- | --- | --- |
| SQLite version | 3.54.0 | 3.44.0 | 3.41.2 | 3.53.4 |
| WASM asset | 669 KiB | 545 KiB | 846 KiB | **849 KiB** (394 KiB gzip) |
| Boot, in-process, one shot | **8.4 ms** | 11.0 ms | 27.7 ms | 33.0 ms |
| Cold process → first row (median) | 100.9 ms | 103.1 ms | 125.4 ms | 135.6 ms |
| `sqlite3_column_decltype` reachable | **no** | **no** | **no** | **yes** |
| In-engine interrupt (`sqlite3_interrupt` / progress handler) | **no** | yes | no | yes |
| SQL-result-limits API (`sqlite3_limit`) | no | no | no | yes |
| OPFS in this context | n/a | n/a | no | no |
| Error messages match the profile as-is | **yes** | **yes** | 2 mismatches | 2 mismatches |
| Passes the 18-item SQL surface | yes | yes | yes | yes |

All four passed every surface item the app actually needs — window functions, `WITH
RECURSIVE`, aggregate `FILTER`, `RETURNING`, `STRICT`, RIGHT/FULL JOIN, `json1` with `->`
and `->>`, `rowid`, `AUTOINCREMENT`, dynamic typing, `LIKE` case-insensitive for ASCII,
`GLOB`, the `date`/`strftime`/`julianday` family, `PRAGMA journal_mode`, and `EXPLAIN
QUERY PLAN`. The engine choice is therefore *not* decided by SQL support.

## The constraint that decides it

`contracts/dialects/sqlite.json` states the column-label rule:

```json
"column_type_label": {
  "source": "sqlite3_column_decltype",
  "fallback": "expression",
  "rule": "the declared type verbatim when the column has one, otherwise the fallback label"
}
```

and `scripts/adapter-parity-audit.mjs` treats a differing `columns[i].type` between the two
adapters as a divergence. That is not an accident of the audit: the label *is* the dialect's
central lesson. A `DATE` column reports the value's storage class (TEXT) while the column's
declared type is DATE, and "a declared type names an affinity, not a constraint" is exactly
what `docs/sqlite-dialect.md` §2 teaches and what chapter `s2` of the course track will teach.

Measured, only the modern build can answer:

```
3.53.4  SELECT id, name, price, hired FROM t  ->  ["INTEGER","TEXT","REAL","TEXT"]
        SELECT COUNT(*), price * 2 FROM t     ->  [ null, null ]   (the `expression` fallback)
sql.js / wa-sqlite / 3.41
        both statements                       ->  unreachable
```

`sql.js` keeps its `Statement` pointer private and exports no `sqlite3_column_decltype`;
`wa-sqlite`'s `sqlite-api.js` wraps 71 `sqlite3_*` calls and decltype is not among them, nor
is `_sqlite3_column_decltype` in the Emscripten export list; 3.41 has no C-API surface at
all. Each of them would label **every** column `expression` — including `id`, `name` and
`price` — so the parity audit could never be green for this dialect, and the browser would
teach the opposite of what the gateway does.

**Rejected alternative:** take the smaller engine (`wa-sqlite`, with the in-engine interrupt
and clean messages) and relax the profile's label rule to `expression` everywhere. Rejected
because it would make one of the dialect's few genuinely load-bearing facts untellable in
the browser, and because the rule is shared, contract-level data — changing it to suit one
driver would change the gateway's answer too.

**Rejected alternative:** report the *storage class* instead of the declared type. Rejected
because the storage class is a property of the value, not of the column, so two rows of one
result would describe the same column differently, and the schema drawer would have to pick
one.

## The cost that choice commits us to

The modern build frames its errors differently from every other candidate, and the framing
had to be measured rather than assumed:

```
incomplete input          ->  SQLITE_ERROR: sqlite3 result code 1: incomplete input
SELEC 1                   ->  SQLITE_ERROR: sqlite3 result code 1: near "SELEC": syntax error
SELECT 'abc              ->  SQLITE_ERROR: sqlite3 result code 1: unrecognized token: "'abc"
SELECT * FROM nope        ->  SQLITE_ERROR: sqlite3 result code 1: no such table: nope
SELECT 1/0 (no error)     ->  —
interrupted (deadline)    ->  SQLITE_INTERRUPT: sqlite3 result code 9: interrupted
duplicate key             ->  SQLITE_CONSTRAINT_UNIQUE: sqlite3 result code 2067: UNIQUE constraint failed: …
```

The profile's rules are written for SQLite's own sentence — `equals "incomplete input"`,
`prefix "unrecognized token:"` — and those two rules can only fire once the driver's frame is
gone. `sql.js` and `wa-sqlite` need no such treatment (0 mismatches measured), which is the
trade: the one engine that can label columns is the one engine that needs an unframer.

The unframer is therefore **driver-specific and lives beside the driver**, in
`web/src/api/sqlite.ts` (`sqliteUnframe`), exactly as the Go executor's `sqliteUnframe` lives
beside `modernc.org/sqlite` in `backend/internal/engine/sqlite.go`. The two invert *different*
frames — modernc writes `<errstr>: <msg> (<rc>)`, this build writes `<CODE>: sqlite3 result
code <n>: <msg>` — and that is the point: `contracts/dialects/sqlite.json` stays a pure
matcher that both readers share, and neither reader has to know the other's driver. The
profile has been pinned on both sides since (`scripts/sqlite-error-codes.mjs`, and the Go
table test), which is what caught the Go half of the same defect.

The frame's own anchoring is what keeps SQLite's sentence intact: only the literal words
`sqlite3 result code <n>: ` are removed, so `no such table: nope` and
`unrecognized token: "'abc"` keep their colons. A message this build did not write is handed
on untouched — reporting the driver's own words beats reporting a sentence the adapter
guessed at.

## Deadline, caps and placement

- **The deadline is inside the engine.** `sqlite3_progress_handler(db, 1000, cb, 0)` runs
  inside SQLite's step loop; returning `1` is `SQLITE_INTERRUPT(9)`. Measured: a 200 ms
  deadline stopped a runaway recursive CTE at **200.4 ms**, `stoppedByEngine: true`, and the
  connection answered a normal query immediately afterwards (`[[4]]`). This is the first
  engine in the app that can stop itself: PGlite's deadline is enforced by *terminating the
  Worker*, which loses the database, and `sql.js` cannot be stopped at all (`sqlite3_interrupt`
  is not in its exports and it has no progress handler — its only stop is the host killing
  the thread).
- **The Worker is still required.** The progress handler cannot run while the step loop is
  not running: measured, a 137.7 ms statement on the page's thread pushed a 50 ms timer to
  139.1 ms. So the engine runs in `web/src/api/sqlite.worker.ts` for the same reason PGlite
  does, and `engineCapabilities()` reports `placement: 'worker'`; a page whose policy refuses
  Workers falls back to the main thread and *says so* rather than claiming a deadline it
  cannot enforce on a page that is blocked.
- **The caps mirror the gateway, not PGlite.** `SQLITE_MAX_ROWS = 500` and
  `SQLITE_MAX_BYTES = 1 << 20` are `engine.DefaultLimits()` on the Go side, so a capped
  result is described identically by both adapters: the row cap is a successful, truncated
  result (the callback returns literal `false`, which stops the step loop *without* an error —
  the browser's equivalent of the gateway cancelling at the cap), and the byte cap is
  `RESULT_LIMIT_EXCEEDED` with the rows already collected. `web/src/api/pglite.ts` keeps its
  own 200-row guard, which is deliberately stricter and exists to keep a 10 000-row
  `SELECT *` out of the DOM; it is not a second contract, and a parity case that crosses
  either limit is what would tell us the two numbers need to be one.
- **Boot is paid once, on the chapter.** Cold process to first row measured 135.6 ms median
  (min 111.6, max 195.7) for module boot 39.2 ms + open 5.9 ms + a 10 000-row seed 70.4 ms +
  a seeded query 19.7 ms. In the page against the shipping adapter: WASM boot, schema and the
  387-line commerce seed together settle in **111 ms**, and a `GROUP BY`-with-`ORDER BY`
  returns in the same turn. `ExecutionService.warmUp(dataset, dialect)` starts it when a
  SQLite chapter opens, so the learner spends it reading.

## Decision

1. **The browser's SQLite is `@sqlite.org/sqlite-wasm` 3.53.4** (npm `latest`,
   `dist/index.mjs` + `dist/sqlite3.wasm`), driven through the `oo1` API. Serverless.
2. **It is loaded lazily and only for SQLite chapters.** The WASM URL comes from Vite's
   `?url` import and is passed as an explicit `locateFile`; the package is excluded from
   dependency pre-bundling for the same reason PGlite is. A learner who never opens a SQLite
   chapter never downloads the 849 KiB build.
3. **The engine module knows nothing about the dialect's rules.** `web/src/api/sqliteEngine.ts`
   opens a database, runs SQL and returns SQLite's values, sentence and result code.
   Classification, encoding, caps and column labels are applied in `web/src/api/sqlite.ts`
   through `web/src/api/sqliteProfile.ts`, which reads `contracts/dialects/sqlite.json`
   directly. One rule table, two readers, no mirror in the browser.
4. **The workspace is an in-memory database**, private to the page, restored by closing and
   reopening it — the browser's equivalent of ADR 0006's replace-the-file reset, with the copy
   made unnecessary. `PRAGMA foreign_keys = ON` is applied per connection because SQLite's
   default is off and the dataset declares its references.
5. **Anonymous and signed-in learners get the same in-page workspace.** The read-only posture
   of an anonymous learner is the *gateway's* job (ADR 0003, ADR 0006); the in-page engine is
   a scratch workspace and saying otherwise would be a promise the page cannot keep.
6. **`explain` is refused, not approximated.** The profile says `plan: false` and
   `analyze: false`; the capability block carries `explain: false`, `dialects` names both
   engines, and `explainDialects` names only PostgreSQL, so the plan action is disabled with
   the profile's own reason (`sqlitePlanRefusal`) instead of a guessed one. `EXPLAIN QUERY
   PLAN` is *available* (`explain_query_plan: true`) and is a different thing — planner text
   with no row counts and no costs — and is the dialect's own plan affordance for the course
   track, not this app's plan contract.

## Consequences

- The parity audit can run its SQLite half against two real engines, because both can label a
  column. That is the whole reason the audit is credible for this dialect.
- The adapter owns an unframer, and the frame shape is pinned in
  `scripts/sqlite-error-codes.mjs` against the exact strings above, so a dependency bump that
  changes the frame fails a test instead of silently downgrading every syntax error to a
  runtime error — which is the defect that produced this ADR's unframer in the first place.
- 849 KiB is the largest candidate and the slowest to boot. That cost is accepted because it
  is the only one that can teach the lesson, and it is not paid by anyone who does not open a
  SQLite chapter.
- `sqlite3_limit` (1e9 SQL length, 1e9 blob length, 500 compound selects measured) is
  available but unused; the row/byte caps above are the app's, and a second cap nobody asked
  for would be a behaviour the two adapters could disagree about.
- When the package's `oo1` API changes, this is the file to re-measure:
  `web/src/api/sqliteEngine.ts` is the only place that speaks to the build's own classes.
