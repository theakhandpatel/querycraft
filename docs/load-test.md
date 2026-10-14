# Load test — published results (technical design §13.2)

The design's test matrix asks for one thing here: *"`/query` under concurrent
load with timeouts and pool metrics recorded."* That is deliberately two claims
rather than one — what a client observes, and what the server says about itself —
recorded next to each other so the second can be checked against the first
instead of trusted.

This file is the result of an actual run of `scripts/load-test.mjs` against a
gateway the harness built itself and booted with its own embedded PostgreSQL
18.3. The output below is pasted from that run, not retyped.

## Machine and run context

- CPU: 11th Gen Intel(R) Core(TM) i5-1135G7 @ 2.40GHz, 8 vCPU
- RAM: 7.7 GiB
- OS: Linux 6.18.54.1-microsoft-standard-WSL2
- Node: v20.17.0
- PostgreSQL: 18.3 (embedded; the gateway starts and owns it for the run)
- Date: 2026-10-14
- Topology: everything on loopback, one host. The load generator, the gateway
  and the database all share this box.

The run opened with these facts about the surface it was about to load:

```
engine: ready=true version="PostgreSQL 18.3" pool={"max_conns":8,"total_conns":2,"idle_conns":2,"acquired_conns":0,"constructing_conns":0}
limits: max_bytes=1048576 max_concurrent=4 max_rows=500 timeout_ms=5000
workload: 8 workers × 40 requests, timeout share 0.1
```

`max_concurrent=4` is the gateway's own statement cap (`/capabilities`,
`/diagnostics`). It is the number the "concurrency cap is enforced" block below
is about.

## How to reproduce

```sh
# The run published below: the harness builds the gateway, boots it on a free
# port with a fresh embedded PostgreSQL, runs the workload, then tears the
# gateway down by PID and waits for PostgreSQL to release its port.
export PATH=$PATH:/usr/local/go/bin
export GOTOOLCHAIN=local
QC_GO=go QC_BACKEND=backend node scripts/load-test.mjs
```

`make audit-load` is the thin Makefile wrapper added with this step; it runs the
command above.

To load a gateway that is already running (nothing is built, nothing is stopped
— and it must have a live PostgreSQL or the test would only measure the
`ENVIRONMENT_UNAVAILABLE` 400 path):

```sh
QC_GATEWAY=http://127.0.0.1:18077 node scripts/load-test.mjs
```

Knobs, all optional, read from the environment by `scripts/load-test.mjs`:

| Variable | Default | Effect |
| --- | --- | --- |
| `QC_LOAD_WORKERS` | `8` | Concurrent client workers. |
| `QC_LOAD_REQUESTS` | `40` | Requests per worker in request-count mode. |
| `QC_LOAD_DURATION` | `0` | When `> 0`, each worker loops for that many seconds instead and `QC_LOAD_REQUESTS` is ignored. |
| `QC_LOAD_TIMEOUT_SHARE` | `0.1` | Fraction of requests that are deadline statements (a fixed 1-in-10 cadence here). |
| `QC_MAX_CONCURRENT` | unset | Passed through to the gateway the harness boots, to change the cap under test. |
| `QC_LOAD_SATURATION_SLEEP_MS` | `800` | How long each slot-holder sleeps in the deterministic saturation probe. |
| `QC_GATEWAY` | unset | Use a gateway someone else started; the harness neither builds nor stops anything. |
| `QC_GO` / `QC_BACKEND` / `QC_BIN` | `go` / `backend` / unset | How to build, or where to find, the gateway binary. |

## The workload

Eight workers each send forty requests; every tenth request is a deadline
statement rather than an analytic one, so the run issues 320 requests of which
32 are meant to time out. Two statements are mixed, both real:

- **The analytic half** is a three-table join with an aggregate, run with the
  request defaults:

  ```sql
  SELECT c.city, count(*) AS line_items, round(avg(oi.unit_price), 2) AS avg_price
  FROM customers c
  JOIN orders o ON o.customer_id = c.id
  JOIN order_items oi ON oi.order_id = o.id
  GROUP BY c.city
  HAVING count(*) >= 2
  ORDER BY line_items DESC
  ```

  It opens a transaction on the restricted learner role, plans and executes
  across three tables, builds an aggregate and serialises the rows — the parts
  that can actually degrade under contention. It is cheap here (15.2 ms p50),
  which is the point: the load is a realistic mix, not a race to saturate CPU.

- **The deadline half** is `SELECT pg_sleep(1)` sent with
  `limits.timeout_ms: 120`. `pg_sleep` runs inside the server, so the row and
  byte caps never see it and only `statement_timeout` (derived from the
  request's own budget) or the client deadline can end it. It is the §13.3
  "`pg_sleep` beyond the deadline" case, re-measured under load; 32 of them run
  alongside the analytic traffic to show that a query that is being cancelled
  does not poison its neighbours.

**Why the analytic statement is not `SELECT 1`.** A trivial statement would
measure the HTTP layer and the JSON codec and the harness would call that "the
engine". The join is the smallest statement that exercises the parts the load
test exists for.

After the main load the harness runs one deterministic probe (see below), then
checks the service is still healthy before tearing everything down.

## Results

Verbatim output of the run:

```
load-test: http://127.0.0.1:34375 (harness-booted gateway, embedded PostgreSQL on 45773)
engine: ready=true version="PostgreSQL 18.3" pool={"max_conns":8,"total_conns":2,"idle_conns":2,"acquired_conns":0,"constructing_conns":0}
limits: max_bytes=1048576 max_concurrent=4 max_rows=500 timeout_ms=5000
workload: 8 workers × 40 requests, timeout share 0.1
main load

  requests            320
  elapsed_ms          1601.8
  throughput_rps      199.8
  latency_ms          p50=16.3 p95=123.3 p99=130.7 max=141.4
  latency_ms analytic p50=15.2 p95=44.7 p99=59.3 max=69.4
  latency_ms deadline p50=123.3 p95=134.8 max=141.4
  outcomes            2xx=288 4xx=32
  error codes         (none)=288 QUERY_TIMEOUT=32
  client in flight    peak 8 (worker-bounded, not the server cap 4)
  saturation refusals 0

gateway instrumentation (POST /query deltas across the load)
  requests_total      200=288 400=32
  statements_total    QUERY_TIMEOUT=32 (none)=288
  histogram           count +320 sum +7.684s
  pool max_conns      8
  pool total        2
  pool idle         2
  pool acquired     0
  pool constructing 0
  pool samples        22 over the load; acquired peak 4, max_conns 8

outcomes are bounded and reported
  ok     every request received a response
  ok     no response was a 5xx
  ok     every failed response names a code from the contract enum
  ok     every analytic statement succeeded
  ok     the analytic statement returned rows
  ok     at least one deadline statement ran
  ok     every deadline statement was stopped as QUERY_TIMEOUT
  ok     the gateway counted every request it answered
  ok     the gateway recorded one statement per request, none dropped
  ok     a timeout statement is counted with its code
the pool never exceeded its own maximum
  ok     acquired_conns never exceeded max_conns
  ok     the pool reported a real maximum
  ok     the pool is shared, not the cap: peak acquired <= max_concurrent + the gateway probes
  ok     every 400 was either a deadline statement or a cap refusal
  ok     a cap refusal names the cap in its message
the concurrency cap is enforced, not just advertised
  ok     all 4 slots were filled by the slow statements
  ok     the two requests past the cap were refused, not dropped
  ok     the refusal names the configured cap
  ok     the slow statements still completed once their slots were free
the service is healthy after the load
  ok     /health answers 200
  ok     the engine is still ready
  ok     the learner role is still the restricted one
slowest requests
     141.4 ms  timeout  400 QUERY_TIMEOUT
     134.8 ms  timeout  400 QUERY_TIMEOUT
     131.0 ms  timeout  400 QUERY_TIMEOUT
     130.7 ms  timeout  400 QUERY_TIMEOUT
     130.4 ms  timeout  400 QUERY_TIMEOUT

shutdown: gateway stopped
  ok     embedded PostgreSQL released 45773

load-test: OK
```

## What each number means

| Line | Reading |
| --- | --- |
| `requests 320` | 8 workers × 40 requests. Nothing was dropped by the client. |
| `elapsed_ms 1601.8` / `throughput_rps 199.8` | Wall time for the whole workload across all workers, and the resulting rate. This is one 1.6-second burst, not a sustained rate. |
| `latency_ms p50=16.3 p95=123.3 p99=130.7 max=141.4` | Every request, both kinds mixed. The distribution is bimodal by construction (see the next two rows); a single p95 over a mixed workload is only meaningful because the mix is known. |
| `latency_ms analytic …` | The 288 real queries: p50 15.2 ms, p99 59.3 ms, worst 69.4 ms. Under a client fleet twice the size of the statement cap, the analytic half still answered in tens of milliseconds. |
| `latency_ms deadline …` | The 32 `pg_sleep` statements: p50 123.3 ms against a 120 ms budget. The 3 ms of overshoot is return-path and scheduling, not a missed deadline. |
| `outcomes 2xx=288 4xx=32` | 288 succeeded (all analytic), 32 failed (all deadline). No 5xx, no dropped connection, no unparsed body. |
| `error codes (none)=288 QUERY_TIMEOUT=32` | The success half carries no code; every failure carries `QUERY_TIMEOUT`, a member of the closed enum in `contracts/query-result.schema.json`. |
| `client in flight peak 8` | The most requests the client ever had outstanding — the worker count, not a server property. |
| `saturation refusals 0` | No main-load request was refused by the statement cap. This is expected here, and is **not** the proof that the cap works; the deterministic probe below is. See the next section. |
| `requests_total 200=288 400=32` | The gateway's own counter, by HTTP status, over the load window. |
| `statements_total QUERY_TIMEOUT=32 (none)=288` | The gateway's statement counter, by contract code. It agrees with the client's `error codes` line. |
| `histogram count +320 sum +7.684s` | A real Prometheus histogram over `POST /query`: 320 observations totalling 7.684 s of server-side request time. |
| `pool max_conns 8`, `samples 22 … acquired peak 4` | The pool can open 8 connections and was sampled 22 times during the load; it never had more than 4 checked out. The 8-connection pool was never the binding limit — the `max_concurrent=4` statement cap was. |
| the three `ok` blocks | The claims being asserted, each one a line the harness would print as `FAIL` with the observed values if it did not hold. |

## Why `saturation refusals 0` is not a contradiction

The cap is 4 and the client ran 8 workers, so it would be reasonable to expect
some requests to be turned away. None were, and the reason is the shape of the
workload rather than a broken cap:

- `saturation refusals` only counts **main-load** responses whose message names
  the cap. In the main load the client's own concurrency is bounded at 8, and
  each request is short: the analytic half is ~15 ms and the deadline half ends
  at ~120 ms. A request that waits a few milliseconds for a free slot is still
  well inside its own budget (analytic requests default to 5000 ms), so nothing
  had time to be refused.
- `client in flight peak 8` is the client's bound; the server's bound is 4. They
  are different numbers describing different things, and the harness prints both
  rather than pretending they are the same.

The actual proof that `max_concurrent` is enforced is the probe under
`the concurrency cap is enforced, not just advertised`, which runs after the
main load and is deterministic:

1. It sends exactly `cap` (4) requests that each `pg_sleep` for
   `QC_LOAD_SATURATION_SLEEP_MS` (800 ms) with a 10 s deadline, and waits 200 ms
   so they have all taken a slot.
2. It then sends two more analytic requests with a 120 ms deadline. Because
   every slot is held by a statement that cannot finish inside that window, the
   only honest outcomes are a refusal or a hang.
3. It requires both to come back `400 QUERY_TIMEOUT` with the configured cap
   named in the message (the gateway replies
   "…already running its maximum of 4 statements…"), and requires the four
   slot-holders to still succeed afterwards.

A client-side "nobody was refused" count cannot prove a cap exists — a broken
cap and an under-loaded cap look identical. The probe removes that ambiguity by
making saturation deterministic.

## How the gateway's instrumentation is cross-checked

The point of recording both sides is that they can be compared:

- `requests_total{route="/query",method="POST"}` grew by exactly **320** — the
  number of requests the client sent.
- `querycraft_request_duration_seconds_count` for the same route grew by exactly
  **320**. The request counter and the histogram come from the *same* middleware
  recorder (`withObservability`), so they cannot disagree about how many requests
  happened; the check exists to catch a route label or a recorder that silently
  changes.
- The statement counter's code split (`QUERY_TIMEOUT=32`, `(none)=288`) matches
  the client's own tally of response codes exactly.
- Pool gauges were sampled *during* the load (22 samples, peak
  `acquired_conns=4`), not merely read before and after, so the peak is observed
  rather than inferred.
- The route label is `/query`, never the raw path and never a query string: a
  load test is exactly the situation where an unbounded label would blow up
  cardinality, so the harness reads the labels it got rather than trusting them.

This is why the load test was sequenced behind metrics (M3 steps 1–2): the
numbers the client reports can be checked against the numbers the server reports,
and neither is taken on faith.

## What this does not prove

Everything here was observed on one machine; none of it should be read as a
capacity plan.

- **It is loopback on a single 8-vCPU i5 under WSL2.** The load generator shares
  a box with the gateway *and* the PostgreSQL it drives, so the client and the
  server compete for the same cores and memory. Real latency on real hardware
  will differ in both directions.
- **There is no network in the measurement.** No RTT, no TLS handshake, no
  proxy, no WAN, no packet loss. These are best-case transport numbers.
- **The embedded PostgreSQL contends with the load generator.** The database
  that serves every analytic request runs on the same 8 vCPUs as the 8 workers
  sending them, so the analytic latencies include self-inflicted contention.
- **The numbers are a lower bound for this class of hardware, not an SLO.**
  199.8 rps for 1.6 seconds is a burst rate, not a sustained one, and nothing
  here establishes a service level.
- **This is not a soak or endurance run.** The whole load is ~1.6 s. There is no
  multi-hour run, no steady-state observation, no drift measurement, and no
  verification that latency, memory or connection counts stay flat over time.
- **No memory or disk growth was measured.** The harness watches latency,
  outcomes and pool gauges; it does not sample the gateway's RSS, the
  PostgreSQL backend's memory, or the cluster's data directory size across the
  run. A slow leak over hours would be invisible to this test.
- **The statement cap, not the pool, was the binding limit.** The pool has 8
  connections and `max_concurrent` was 4, so at most 4 statements could ever be
  in flight and the pool was never stressed (peak `acquired_conns=4`, never
  above 4 of 8). A run that raised the cap past the pool size — or sized the
  pool below the cap — would exercise a different bottleneck that this run does
  not touch.
- **Only two statement shapes were tested.** A representative analytic join and a
  `pg_sleep` that must time out. Pathological-but-real workloads (large sorts,
  many concurrent transactions on the same rows, lock contention) are the
  adversarial suite's job, not this one's.
- **No client-side resource limits were modelled.** Eight workers on one Node
  process; the harness does not reproduce slow clients, half-closed connections
  or a slowloris-style consumer, and it does not test the 8 MiB request-body cap.
