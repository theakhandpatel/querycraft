# Adversarial suite

`scripts/adversarial-audit.mjs` sends hostile SQL and malformed requests to a
running gateway. Every case must end in a bounded, reported outcome and leave the
service healthy. A hang is a failure even when nothing throws.

The suite runs as `make audit-adversarial`. It is part of `make test`.

## What a run checks

For each case, the harness records five facts:

1. The request returned.
2. It returned within its own deadline plus slack.
3. The outcome was a code from the closed set in
   `contracts/query-result.schema.json`. Never a 5xx, never an empty body.
4. The code is the one the case is supposed to produce.
5. The service still answers afterwards.

The last section re-counts the four dataset tables. The suite tries to drop and
truncate them, and comparing the data before and after is the honest way to show
none of it worked.

Every case runs through the anonymous path unless it needs a session. The suite
also signs in a learner to read `/diagnostics`, which reports pool state and the
learner role's privileges.

## The cases

| Case | What it sends | Expected bounded outcome |
| --- | --- | --- |
| `pg_sleep` past its deadline | `select pg_sleep(5)` with `timeout_ms: 400` | `QUERY_TIMEOUT` |
| A billion-row `generate_series` | `select generate_series(1, 1000000000)` with `max_rows: 300` | A truncated result at 300 rows, or a timeout |
| An unbounded recursive CTE, streamed | A recursive CTE with no stop condition and `max_rows: 300` | A truncated result, or a timeout |
| An unbounded recursive CTE behind an aggregate | `count(*)` over the same infinite CTE with `timeout_ms: 800` | `QUERY_TIMEOUT` |
| A 100000 by 100000 cross join | `count(*)` over two 100k-row series with `timeout_ms: 600` | `QUERY_TIMEOUT` |
| A `DO` block that never ends | A loop of `pg_sleep(0.1)` with `timeout_ms: 600` | `QUERY_TIMEOUT` |
| Reading `app.progress` | `select * from app.progress` | `QUERY_RUNTIME_ERROR` |
| Reading a file | `pg_read_file`, `pg_read_binary_file`, or `pg_ls_dir` | `QUERY_RUNTIME_ERROR` |
| Running a program | `COPY ... TO PROGRAM` | `QUERY_RUNTIME_ERROR` |
| Changing the server | `CREATE EXTENSION`, `CREATE ROLE`, `CREATE DATABASE`, or `ALTER SYSTEM` | `QUERY_RUNTIME_ERROR` |
| Dropping or truncating data | `DROP TABLE dataset.orders` or `TRUNCATE dataset.orders` | `QUERY_RUNTIME_ERROR` |
| A 5 MB single value | `select repeat('x', 5000000)` with the default 1 MiB byte cap | `RESULT_LIMIT_EXCEEDED`, with a small response body |
| Connection exhaustion | At least 24 concurrent `pg_sleep(1)` statements with `timeout_ms: 400` | Every request `QUERY_TIMEOUT`, no 5xx, and the pool drains afterwards |
| A runaway query while serving | `pg_sleep(4)` with a 4000 ms deadline, plus `/health` and a fast query mid-flight | `QUERY_TIMEOUT`, while health and the fast query both answer `200` |

The row cap stops a streamed result and cancels the query rather than draining
it. An aggregate cannot be truncated, so only the deadline can end it. That is
the case that proves the timeout is not a row cap in disguise. A `DO` block has no
result set to cap, so it tests the deadline alone.

The app schema is a separate schema, not on the learner's `search_path`, and the
learner role holds no grant on it. Privileged operations fail before any policy
code runs: the role is `NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION
NOBYPASSRLS`, and the transaction is read-only. `DROP TABLE` and `TRUNCATE` are
refused twice, by the read-only transaction and by the missing grant.

## The fuzz phase

After the named cases, the script generates a seeded batch of requests against
the same gateway, learner, and base URL. The default seed is `20261019` and the
default count is 40. Set `FUZZ_SEED` to replay a different stream and
`FUZZ_CASES` to change the count.

The generator is `mulberry32`, written inline in the script. The seed appears in
the run banner and in the summary. A failing case prints the seed, the case
number, and the generated input, shortened to a readable length, so one command
reproduces it.

The SQL generators cover:

- Deep parenthesis nesting, 200 to 700 levels.
- A long `IN` list, 5,000 to 20,000 items.
- 400 to 1,200 `UNION ALL` arms.
- A multi-megabyte string literal, 1.2 to 3 MiB.
- Deeply nested subqueries, 120 to 500 levels.
- Random token soup with Unicode and NUL-like escapes.
- Huge `LIMIT` and `OFFSET` values.
- Random `generate_series` cross joins with a row cap.
- An unbounded recursive CTE in streaming and aggregate form.
- A multi-statement script.

The raw request generators cover malformed JSON, wrong field types, negative and
huge limits, deeply nested JSON, unknown fields, a 100 KB `dataset` or `dialect`
name, duplicate keys, and one 9 MiB body above the 8 MiB cap. The oversized body
is built once and sent as a string, so the harness does not encode it twice.

Each generated case must return within its own client bound, never answer with a
5xx, decode as JSON, and end as `succeeded` or a code from the closed set. A case
that asks for `max_rows` must return `truncated: true` at that cap, and its body
must stay small. The script checks health after every ten cases and once at the
end.

The last case sends `max_concurrent + 2` heavy statements at once. All must settle
within 8 seconds, and a normal query must still answer afterwards. That checks
the admission budget under contention, not only one request at a time.

## Reproduce

```sh
make audit-adversarial            # builds a gateway, boots it with its own PostgreSQL
make audit-adversarial-live       # the same against QC_GATEWAY, default :18077
node scripts/adversarial-audit.mjs --markdown   # print the case table
FUZZ_SEED=12345 FUZZ_CASES=60 make audit-adversarial
```

## What the suite records but does not protect

Several catalogs are readable by any role in PostgreSQL. The suite records the
posture instead of pretending otherwise.

- A learner can list the database names on the server. `pg_database` is
  world-readable. The gateway opens the connection, so a statement cannot switch
  databases, but the names are visible.
- Role names are visible through `pg_roles`. Password hashes are not: `rolpassword`
  reads `********` for every row, and the suite asserts that.
- Ordinary operational settings such as `max_connections`, `shared_buffers`, and
  `port` are visible. Host paths are not. `data_directory`, `config_file`, and
  `hba_file` are superuser-only settings, and the suite asserts that the learner
  sees none of them.
- `pg_stat_activity` is readable. A learner can count connections and see their
  own backend. Other sessions' rows are mostly NULL for a non-superuser, but the
  existence of other sessions is visible.

## Other limits

- The suite does not reset a workspace while a statement runs. The gateway has
  `/environment/reset`, but this suite does not race it with a query. It tests a
  runaway statement while the service keeps serving, and a separate test covers
  reset.
- Cancellation by an operator is not tested end to end. `pg_terminate_backend`
  from an administrative connection would exercise it. The same paths surface as
  `QUERY_TIMEOUT`, and Go tests cover the engine path.
- Fuzzing is not a resource-exhaustion proof. It probes parser depth, statement
  size, row, byte, and time caps, the connection budget, and malformed request
  bodies on one loopback machine with a cap of four concurrent statements. It
  does not create disk pressure, memory pressure, hundreds of environments, or a
  multi-hour soak.
- Slowloris and TLS stalls are not tested. The fuzz phase covers malformed JSON,
  wrong types, deeply nested JSON, and bodies above the 8 MiB cap. It does not
  send partial HTTP headers, hold a connection open byte by byte, or exercise TLS
  handshakes and HTTP/2. Those rely on the Go HTTP server's own timeouts.
