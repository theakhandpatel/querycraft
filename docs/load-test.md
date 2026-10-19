# Load test

`scripts/load-test.mjs` drives concurrent `POST /query` traffic and records what
the client observes next to what the gateway says about itself. The two are
printed together so one can be checked against the other.

The target is `make audit-load`. It is not part of `make test`, because it starts
its own gateway.

## What it measures

A run mixes two real statements:

- An analytic three-table join with an aggregate, a `HAVING` clause, and an
  `ORDER BY`. It opens a transaction on the restricted learner role, plans and
  executes across three tables, builds the aggregate, and serializes the rows.
  Those are the parts that can degrade under contention.

  ```sql
  SELECT c.city, count(*) AS line_items, round(avg(oi.unit_price), 2) AS avg_price
  FROM customers c
  JOIN orders o ON o.customer_id = c.id
  JOIN order_items oi ON oi.order_id = o.id
  GROUP BY c.city
  HAVING count(*) >= 2
  ORDER BY line_items DESC
  ```

- A deadline statement, `SELECT pg_sleep(1)` with `limits.timeout_ms: 120`.
  `pg_sleep` runs inside the server, so the row and byte caps never see it. Only
  `statement_timeout`, derived from the request's own budget, or the client
  deadline can end it. The run shows that a query being cancelled does not poison
  its neighbors.

The default workload is 8 workers with 40 requests each. Every tenth request is
the deadline statement, so 320 requests run and 32 are meant to time out.

The analytic statement is not `SELECT 1`. A trivial statement would measure the
HTTP layer and the JSON codec, and the harness would call that "the engine". The
join exercises the path the load test exists for.

After the main load, the harness runs one deterministic saturation probe, then
checks that the service is still healthy.

## Run it

The harness builds the gateway, boots it on a free port with a fresh embedded
PostgreSQL, runs the workload, and tears the gateway down by PID:

```sh
export PATH=$PATH:/usr/local/go/bin
export GOTOOLCHAIN=local
QC_GO=go QC_BACKEND=backend node scripts/load-test.mjs
```

To load a gateway that is already running, use `QC_GATEWAY`. Nothing is built and
nothing is stopped. The gateway must have a live PostgreSQL, or the run measures
the `ENVIRONMENT_UNAVAILABLE` response path:

```sh
QC_GATEWAY=http://127.0.0.1:18077 node scripts/load-test.mjs
```

## Knobs

All are optional and read from the environment:

| Variable | Default | Effect |
| --- | --- | --- |
| `QC_LOAD_WORKERS` | `8` | Concurrent client workers. |
| `QC_LOAD_REQUESTS` | `40` | Requests per worker in request-count mode. |
| `QC_LOAD_DURATION` | `0` | When greater than `0`, each worker loops for that many seconds and `QC_LOAD_REQUESTS` is ignored. |
| `QC_LOAD_TIMEOUT_SHARE` | `0.1` | Fraction of requests that are deadline statements. |
| `QC_MAX_CONCURRENT` | unset | Passed to the gateway the harness boots, to change the cap under test. |
| `QC_LOAD_SATURATION_SLEEP_MS` | `800` | How long each slot-holder sleeps in the saturation probe. |
| `QC_GATEWAY` | unset | Use a gateway someone else started. |
| `QC_GO`, `QC_BACKEND`, `QC_BIN` | `go`, `backend`, unset | How to build, or where to find, the gateway binary. |

## What the run asserts

The harness checks four groups:

- Outcomes are bounded and reported. Every request received a response. No
  response was a 5xx. Every failed response names a code from the closed enum.
  Every analytic statement succeeded and returned rows. Every deadline statement
  ended as `QUERY_TIMEOUT`.
- The gateway counted every request it answered. The request counter and the
  duration histogram both grew by the number of requests the client sent. The
  statement counter's code split matches the client's own tally.
- The pool never exceeded its own maximum, and the pool is not the concurrency
  cap.
- The service is healthy after the load. `/health` answers `200`, the engine is
  ready, and the learner role is still the restricted one.

## The saturation probe

The main load bounds client concurrency at 8 and uses short requests. A request
that waits a few milliseconds for a free slot is still well inside its budget, so
the main load can finish with zero refusals even when the cap works. That is why
the harness prints `saturation refusals` separately and does not treat a zero as
proof.

The deterministic probe runs after the main load:

1. It sends exactly `cap` requests that each `pg_sleep` for
   `QC_LOAD_SATURATION_SLEEP_MS` with a 10-second deadline, then waits 200 ms so
   they have all taken a slot.
2. It sends two more analytic requests with a 120 ms deadline. Every slot is held
   by a statement that cannot finish inside that window.
3. It requires both to return `400 QUERY_TIMEOUT` with the configured cap named
   in the message, and it requires the four slot-holders to succeed afterwards.

A client-side "nobody was refused" count cannot prove a cap exists. A broken cap
and an under-loaded cap look the same. The probe makes saturation deterministic.

## Cross-check the instrumentation

The point of recording both sides is that they can disagree:

- `requests_total{route="/query",method="POST"}` grows by the number of requests
  the client sent.
- `querycraft_request_duration_seconds_count` for the same route grows by the
  same number. Both come from one middleware recorder, so the check catches a
  route label or recorder that silently changes.
- The statement counter's code split matches the client's response codes.
- Pool gauges are sampled during the load, not only read before and after.
- The route label is `/query`, never the raw path or a query string. An unbounded
  label would blow up cardinality.

## What this does not prove

- A run is loopback on one machine. The load generator shares the box with the
  gateway and the PostgreSQL it drives, so the client and server compete for the
  same cores and memory. Latency on real hardware will differ in both directions.
- There is no network in the measurement. No RTT, TLS handshake, proxy, WAN, or
  packet loss. These are best-case transport numbers.
- The run is a short burst, not a soak. It does not measure drift in latency,
  memory, or connection counts over hours.
- It watches latency, outcomes, and pool gauges. It does not sample the gateway's
  RSS, the PostgreSQL backend's memory, or the cluster's data directory size.
- The statement cap, not the pool, is the binding limit at the default settings.
  A run that raised the cap past the pool size would exercise a different
  bottleneck.
- Only two statement shapes are tested. Pathological workloads such as large
  sorts, many transactions on the same rows, and lock contention belong to the
  adversarial suite.
- Eight workers run in one Node process. The harness does not reproduce slow
  clients, half-closed connections, or a slowloris consumer. It does not test the
  8 MiB request-body cap.
