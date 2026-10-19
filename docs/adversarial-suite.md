# Adversarial suite: published results (technical design §13.3)

The design asks for ten named adversarial cases, and adds the sentence that
makes them worth publishing: *"Each case must end in a bounded, reported outcome
and leave the service healthy."* A hang is a failure even when nothing throws,
and the code that runs learner SQL is only as good as what it does when the SQL
is hostile.

This file is the result of an actual run of `scripts/adversarial-audit.mjs`
against a gateway with its own embedded PostgreSQL 18.3 (see the run banner
below). The table is the harness's own output, pasted, not retyped.

## How to reproduce

```sh
make audit-adversarial            # builds a gateway, boots it with its own PostgreSQL, runs all cases
make audit-adversarial-live       # the same against a gateway you already have (QC_GATEWAY, default :18077)
node scripts/adversarial-audit.mjs --markdown   # print the table below again
FUZZ_SEED=12345 FUZZ_CASES=60 make audit-adversarial   # replay another fuzz stream
```

The harness records five facts per case: that the request returned, that it
returned within its own deadline plus slack, that the outcome was a code from the
closed set in `contracts/query-result.schema.json` (never a 5xx and never an
empty body), that the code is the one the case is supposed to produce, and that
the service still answers afterwards. The last section re-counts the four dataset
tables: the suite tries to drop and truncate them, and the honest way to show
none of it worked is to compare the data before and after.

Run banner:

```
engine:  PostgreSQL 18.3, pool max 8, max_concurrent 4
learner: querycraft_learner (search_path=dataset, can_read_app=false)
fuzz:    seed 20261019, 40 cases
```

## Results

| Case | What it sends | Observed outcome | Time | Healthy after |
| --- | --- | --- | --- | --- |
| pg_sleep past its deadline | `select pg_sleep(5)` with `timeout_ms: 400` | QUERY_TIMEOUT | 406 ms | yes |
| a billion-row generate_series | `select generate_series(1, 1000000000)` with `max_rows: 300` | succeeded, truncated at 300 rows | 4 ms | yes |
| 3a. an unbounded recursive CTE, streamed | `WITH RECURSIVE t(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM t) SELECT n FROM t` with `max_rows: 300` | succeeded, truncated at 300 rows | 5 ms | yes |
| 3b. an unbounded recursive CTE behind an aggregate | `SELECT count(*) FROM (WITH RECURSIVE t(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM t) SELECT n FROM t) s` with `timeout_ms: 800` | QUERY_TIMEOUT | 804 ms | yes |
| 3b. unbounded CTE aggregate | count(*) over an infinite recursive CTE | QUERY_TIMEOUT | 804 ms | yes |
| a 100000 x 100000 cross join | `count(*)` over two 100k-row series with `timeout_ms: 600` | QUERY_TIMEOUT | 603 ms | yes |
| a DO block that never ends | `DO $$ BEGIN LOOP PERFORM pg_sleep(0.1); END LOOP; END $$` with `timeout_ms: 600` | QUERY_TIMEOUT | 602 ms | yes |
| 6a. reading app.progress directly | `select * from app.progress` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 6b. role catalog posture | `select rolname, rolpassword from pg_roles` | succeeded, 18 rows | 4 ms | yes |
| 6c. server settings posture | `count(*) from pg_settings` for hidden and operational names | host paths hidden: 0; operational settings visible: 3 | 6 ms | yes |
| 6e. environment enumeration | `select datname from pg_database` | succeeded, 4 rows | 4 ms | yes |
| 6d. information_schema probe | counts app vs dataset tables | succeeded, 1 rows | 9 ms | yes |
| 7a. COPY ... TO PROGRAM | `copy (select 1) to program 'id'` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 7b. pg_read_file | `select pg_read_file('/etc/passwd')` | QUERY_RUNTIME_ERROR | 4 ms | yes |
| 7c. pg_ls_dir | `select pg_ls_dir('/')` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 7d. CREATE EXTENSION | `create extension if not exists hstore` | QUERY_RUNTIME_ERROR | 2 ms | yes |
| 7e. CREATE ROLE | `create role qc_attacker login` | QUERY_RUNTIME_ERROR | 2 ms | yes |
| 7f. CREATE DATABASE | `create database qc_attacker` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 7g. ALTER SYSTEM | `alter system set work_mem = '1GB'` | QUERY_RUNTIME_ERROR | 2 ms | yes |
| 7h. DROP TABLE | `drop table dataset.orders` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 7i. TRUNCATE | `truncate table dataset.orders` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| 7j. pg_read_binary_file | `select pg_read_binary_file('/etc/hostname')` | QUERY_RUNTIME_ERROR | 3 ms | yes |
| a 5 MB single value | `select repeat('x', 5000000)` with the default 1 MiB byte cap | RESULT_LIMIT_EXCEEDED | 74 ms | yes |
| 9. 24 concurrent slow statements | `select pg_sleep(1)`, `timeout_ms: 400` | 24/24 QUERY_TIMEOUT, 0 5xx | 426 ms | yes |
| 10. runaway query while serving | `select pg_sleep(4)`, `timeout_ms: 4000`; health and a fast query sent mid-flight | QUERY_TIMEOUT; health and a real query both 200 during the run | 4003 ms | yes |
| dataset integrity | counts for all four tables | 30 customers, 20 products, 80 orders, 160 order_items | 4 ms | yes |

## What each result means

| Case | Why that outcome is the right one |
| --- | --- |
| `pg_sleep` past the deadline | The deadline is enforced server-side by `SET LOCAL statement_timeout`, derived from the request's own budget. The statement is cancelled, not abandoned. |
| Billion-row `generate_series` | The row cap stops the read, and the gateway *cancels* the query rather than draining it. See "Defects this suite found" below: before the fix this case took 4003 ms and held a pool connection for the whole deadline. |
| Unbounded recursive CTE | The streaming form is cut off by the row cap in 5 ms. The aggregate form cannot be truncated, so the deadline ends it: that is the case that proves the timeout is not just a row cap in disguise. |
| Cross join explosion | An aggregate means every row must be produced, so only the deadline can end it. It does, in 608 ms against a 600 ms budget. |
| `DO` block with a loop | No result set to cap, so the deadline is the only control. It holds. |
| Reading `app.progress` | The app schema is a separate schema that is not on the learner's `search_path`, and the learner role holds no grant on it. `permission denied for schema app`. |
| `COPY ... TO PROGRAM`, `pg_read_file`, `pg_ls_dir`, `CREATE EXTENSION`, `CREATE ROLE`, `CREATE DATABASE`, `ALTER SYSTEM` | Every one is a runtime error within milliseconds. The learner role is `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS` and the transaction is `READ ONLY`, so PostgreSQL refuses all of these before any policy code runs. |
| `DROP TABLE` / `TRUNCATE` | Refused by the read-only transaction as well as by the missing grant: two independent controls. |
| 5 MB single value | The 1 MiB serialised-byte cap is a *hard* failure (`RESULT_LIMIT_EXCEEDED`) rather than a truncation, because a single row that does not fit cannot be represented partially. The response body stays tiny: the value is never marshalled into the response. |
| Connection exhaustion | 24 concurrent slow statements against a cap of 4 all returned `QUERY_TIMEOUT`, none was dropped and none was a 5xx, and the pool drained to 0 acquired. Saturation is reported, not hidden. |
| Runaway query while serving | `/health` and a real learner query both answered 200 *during* the runaway statement: one request cannot take the gateway out. |

## Resource-exhaustion fuzzing

The ten named cases are fixed. The same script then generates a seeded batch of
requests against the same gateway, learner and `base`. The default seed is
`20261019` and the default count is 40. Set `FUZZ_SEED` to replay a different
stream and `FUZZ_CASES` to change the count.

The generator is `mulberry32`, written inline in
`scripts/adversarial-audit.mjs`. The seed appears in the run banner and in the
markdown summary. A failing case prints the seed, the case number and the
generated input, shortened to a readable length, so one command reproduces it.

The SQL generators cover:

- deep parenthesis nesting, 200 to 700 levels;
- a very long `IN` list, 5,000 to 20,000 items;
- 400 to 1,200 `UNION ALL` arms;
- a multi-megabyte string literal, 1.2 to 3 MiB;
- deeply nested subqueries, 120 to 500 levels;
- random token soup with Unicode and NUL-like escapes;
- huge `LIMIT` and `OFFSET` values;
- random `generate_series` cross joins with a row cap;
- an unbounded recursive CTE in streaming and aggregate form;
- a multi-statement script.

The raw request generators cover malformed JSON, wrong field types, negative and
huge limits, deeply nested JSON, unknown fields, a 100 KB `dataset` or `dialect`
name, duplicate keys, and one 9 MiB body above the 8 MiB cap. The oversized body
is built once and sent as a string, so the harness does not encode it twice.

Each generated case must return within its own client bound, never answer with a
5xx, decode as JSON, and end as `succeeded` or a code from the closed set in
`contracts/query-result.schema.json`. A case that asks for `max_rows` must return
`truncated: true` at that cap, and its body must stay small. The script calls
`healthy()` after every ten cases and once at the end.

The last case sends `max_concurrent + 2` heavy statements at once. All of them
must settle within 8 seconds, and a normal query must still answer afterwards.
That checks the admission budget under contention, not just one request at a
time.

The run below used seed `20261019` and 40 cases, and took 1.6 s with no failures.
A second run with `FUZZ_SEED=12345 FUZZ_CASES=60` also had no failures and took
2.9 s. The generators hit the parser depth limit, `statement_timeout`, the row
cap, the byte cap and the concurrency limit. None produced a 5xx or an
unclassified outcome.

| Metric | Value |
| --- | --- |
| Cases | 40 |
| Seed | 20261019 |
| Failures | 0 |
| Duration | 1.6 s |
| Generators exercised | huge LIMIT/OFFSET, malformed JSON, recursive CTE, streamed, deep parenthesis nesting, random generate_series cross join, huge limits (clamped to the server caps), many UNION ALL arms, huge dataset name, random token soup, wrong limit type (max_rows is a string), deeply nested JSON, very long IN list, huge dialect name, multi-megabyte string literal, negative limits, deeply nested subqueries, recursive CTE behind an aggregate, oversized body (9 MiB), multi-statement script, wrong field type (sql is a number), duplicate keys, unknown fields, concurrency burst |

## Defects this suite found

The point of publishing results is that they are not all good news. Three real
defects were found and fixed while building this suite, each by a case that
looked like it should already pass:

1. **Permission denied was reported as a syntax error.** PostgreSQL puts
   `42501 insufficient_privilege` in SQLSTATE class 42, whose name is "syntax
   error or access rule violation", and the classifier mapped the whole class to
   `QUERY_SYNTAX_ERROR`. A learner running `pg_read_file('/etc/passwd')` was told
   their SQL had a syntax problem: the one thing that could not have helped.
   Class 42 still maps to a syntax error except for `42501`, which is now
   `QUERY_RUNTIME_ERROR` (`engine/execute.go`, tested in
   `engine/classify_test.go`).
2. **Waiting for a pool connection was reported as an unreachable database.**
   `PgxExecutor.Query` wrapped every `BeginTx` failure as
   `ErrEnvironmentUnavailable`, including a pool acquire that hit the request's
   own deadline. Under the concurrency case, 15 of 24 requests told the learner
   "the execution environment is unavailable" when the environment was fine and
   the gateway was merely busy. Pool-wait deadlines are now `ErrPoolTimeout` and
   report `QUERY_TIMEOUT` with a message that names the wait
   (`engine/pgx.go`, `engine/engine.go`).
3. **Truncation did not stop the server.** Hitting the row cap left the driver
   to drain the remaining rows so the connection could be reused, so
   `generate_series(1, 1000000000)` returned its 300 rows after 4003 ms and held
   a pool connection for the whole request deadline. The gateway now cancels the
   query context as soon as a cap is hit: the same case returns in 4 ms and frees
   the connection immediately (`engine/execute.go`).

## What is not protected

This is the part that matters more than the green ticks. Everything here was
observed in the run above; none of it is hypothetical.

- **A learner can enumerate the databases on the server.** `pg_database` is
  world-readable; the run listed four. Today that is one application database,
  but D11 gives each challenge its own database, and if those databases grant
  `CONNECT` to `PUBLIC` then a learner can both name and connect to someone
  else's environment. Per-environment isolation must revoke `CONNECT` from
  `PUBLIC` and issue a per-environment role. **Not implemented in M3**; see
  `docs/adr/0002-challenge-environments.md`.
- **Role names are readable.** `pg_roles` exposed 18 rows, including
  `querycraft` and `querycraft_learner`. Password hashes are not: `rolpassword`
  is `********` for every row, which the suite asserts. Names are low-value for a
  loopback, single-user install and were left alone deliberately.
- **Ordinary operational settings are readable.** `max_connections`,
  `shared_buffers`, `port` and `ssl_key_file` are visible to the learner role
  (three of the four names checked returned a value; the count of visible
  settings is 375). Host paths are not: `data_directory`, `config_file` and
  `hba_file` are superuser-only GUCs and PostgreSQL filters them out: the suite
  asserts that too, because "the catalog is readable" and "the data directory is
  exposed" are very different claims.
- **`pg_stat_activity` is readable.** The learner can count connections and see
  their own backend. Other sessions' rows are mostly NULL for a non-superuser,
  but the existence of other sessions is visible.
- **No reset endpoint exists, so "reset while a query is running" is not tested
  as written.** The design's tenth case assumes Adapter B can rebuild a dataset
  while a statement runs. Adapter B has no reset route: the browser adapter
  resets a Worker, and on the gateway the equivalent controls are the deadline
  and, in future, a template recreate per challenge. The suite tests the
  reachable half: a runaway statement being ended while the service keeps
  serving, and this paragraph is here so that nobody reads a green tick as
  proof of the other half.
- **A cancelled statement is tested through the deadline, not through an
  operator.** `pg_terminate_backend` from an administrative connection would
  exercise cancellation by hand, and both paths surface as the same
  `QUERY_TIMEOUT`; the harness has no PostgreSQL client, so this is covered by
  Go tests (`engine/engine_test.go`) rather than end-to-end.
- **Fuzzing is not a resource-exhaustion proof.** The seeded phase probes parser
  depth, statement size, row, byte and time caps, the connection budget and
  malformed request bodies on one loopback machine with a cap of 4 concurrent
  statements. It does not create disk pressure, memory pressure, a
  hundreds-of-environments burst or a multi-hour soak. Public hosting that
  accepts SQL from untrusted users remains out of scope until per-query
  container isolation exists, exactly as the design says.
- **Slowloris and TLS stalls are not tested.** The fuzz phase sends malformed
  JSON, wrong types, deeply nested JSON and bodies above the 8 MiB cap, so the
  request decoder and that cap are covered. It does not send partial HTTP
  headers, hold a connection open byte by byte, or exercise TLS handshakes and
  HTTP/2. Those stay with the Go HTTP server's own timeouts and are not measured
  here.
