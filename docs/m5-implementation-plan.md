# M5 implementation plan — "SQL performance lab"

Design source: `docs/technical-design.md` §14 (M5), §4.2 (QueryResult), §5.4 (Adapter B),
§6.1–6.4 (evaluation), §13.1–13.3 (observability and testing). Related decisions already on
record: ADR `0002` (template challenge environments), ADR `0003` (writable per-account
learner workspaces), ADR `0004` (PostgreSQL is the default dialect), ADR `0005` (AI is
optional).

Steps are ordered so the shared seam lands first, then the Go engine and the gateway
(Track B), then the browser surface (Track F), and the two meet at one plan-based
evaluation fixture suite. Nothing in M5 changes the behaviour of an existing lesson: a
performance exercise is a new exercise kind that a chapter opts into.

## What M5 is

> Scope: performance exercise type, `EXPLAIN` viewing, controlled `EXPLAIN ANALYZE`,
> larger controlled datasets, plan-based evaluation before timing, environment metadata in
> results.
>
> Exit gate:
> - Plans are reproducible on pinned fixtures.
> - Results report engine version, scale and indexes, and do not overstate timing
>   precision.

### The gate, operationalized

| Gate clause | What proves it | Step |
| --- | --- | --- |
| Plans are reproducible on pinned fixtures | the same statement is planned N times against the sealed `perf` template and the **structural** facts (node types, relations, indexes, row estimates) are compared; timings are explicitly excluded from the comparison and each exclusion is printed with a reason | 2, 5, 8 |
| Results report engine version, scale and indexes | every `PlanBundle` carries an `environment` block — `engine`, `engine_version`, `scale_factor`, per-relation row counts, the index list, and the planner settings that change plans (`random_page_cost`, `effective_cache_size`, `work_mem`, the `enable_*` toggles) | 2, 3 |
| Results do not overstate timing precision | `PlanBundle.timing_precision` is one of `analyze` / `estimate` / `none`, the UI renders the label next to every number, and no graded assertion reads a timing field; the audit reads the schema and fails if a fixture asserts on `actual_time` or `time_ms` | 5, 6, 8 |
| A learner who is not signed in cannot analyze | `EXPLAIN ANALYZE` executes the statement, so it requires a writable environment (ADR 0003); an anonymous request is refused with the existing `ENVIRONMENT_UNAVAILABLE` code and a reason, exactly like a write | 3 |

## Where we are today (measured, not assumed)

- `POST /query` runs learner SQL through `engine.Executor` inside a deadline, a semaphore
  and the row/byte caps (`backend/internal/engine/execute.go`). There is **no** `EXPLAIN`
  anywhere in the tree.
- The planner's own numbers are unreachable today: `QueryResult` carries columns, rows,
  `row_count`, `duration_ms` and nothing about *how* the rows were found.
- The sealed template (ADR 0002) and the per-account writable copy (ADR 0003) already
  exist, so a larger "performance" dataset is a second template rather than new machinery.
- The `plans` course exists in the library as a **preview** (`built:false`, `modules:null`)
  — the promise M5 has to keep.

## Sequencing

| # | Step | Track | Ships | Depends on |
| --- | --- | --- | --- | --- |
| 1 | The plan seam | shared | `contracts/plan.schema.json`, `contracts/perf-contract.schema.json`, `web/src/api/planContract.ts`, `backend/internal/perf/plan.go` (types only) | — |
| 2 | Engine: capture a plan | backend | `Executor.Explain` — `EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON)` under the existing caps; node walk; honest `timing_precision` | 1 |
| 3 | Gateway: `POST /explain` + the `plan` capability block | backend | route, `plan` on `/capabilities`, OpenAPI, metrics, the anonymous refusal | 2 |
| 4 | The performance dataset and its template | shared | `dataset/perf.sql` + `dataset/perf_indexes.sql`, a `perf` template per ADR 0002, `dataset_meta` rows | 1 |
| 5 | Plan-based evaluation | shared | `backend/internal/perf/grade.go` + `contracts/fixtures/perf-*.json`, read by Go and by `scripts/fixtures-audit.mjs` | 1–4 |
| 6 | Frontend: the plan viewer and the perf exercise | frontend | plan tree with cost share, index chips, seq-scan warning, estimate-vs-actual; the exercise kind in the lesson; honest timing labels | 3, 5 |
| 7 | The `plans` course, authored | shared | `web/src/data/content/p1..p8.md` + `PLANS_MODULES`, `built:true` | 6 |
| 8 | Exit-gate audit and release | shared | `make audit-perf` (the four clauses above, measured); `make verify`; tag with the owner's go-ahead | 1–7 |

Step 1 before everything: the seam is what keeps the Go grader and the browser grader
from inventing two different vocabularies for the same plan. Step 5 before step 6 so the
UI cannot render a verdict the grader cannot produce. Step 7 last because a course about
plans is only honest once the plans are real.

## The contract

`contracts/plan.schema.json` — a `PlanBundle`, returned by `POST /explain`:

| Field | Meaning |
| --- | --- |
| `status` | `succeeded` / `failed` — the same two-state discipline as `QueryResult` |
| `analyze` | whether the statement was executed (`EXPLAIN ANALYZE`) or only planned |
| `timing_precision` | `analyze` (executed, per-node timings reported), `estimate` (planner cost only), `none` (no plan available) |
| `plan` | the raw PostgreSQL JSON plan node tree, verbatim, untruncated up to the node cap |
| `summary` | derived, deterministic facts: `node_types[]`, `relations[]`, `indexes_used[]`, `seq_scans[]`, `max_estimated_rows`, `total_cost`, `plan_rows` |
| `environment` | `engine`, `engine_version`, `scale_factor`, `row_counts{}`, `indexes[]`, `settings{}` |
| `warnings[]` | stable codes — e.g. `PLAN_SEQ_SCAN_LARGE_TABLE`, `PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE`, `PLAN_NODE_CAP_REACHED` |
| `error` | the same envelope as `QueryResult`, so one error taxonomy serves both |

`contracts/perf-contract.schema.json` — a `PerfContract`, attached to an exercise alongside
its `ResultContract`:

| Field | Meaning |
| --- | --- |
| `require_nodes[]` | node types that must appear (e.g. `Index Scan`) |
| `forbid_nodes[]` | node types that must not appear *on a named relation above a row threshold* (e.g. `Seq Scan` on `orders` with more than 1000 rows — a Seq Scan on a 12-row dimension table is correct and must stay legal) |
| `require_indexes[]` | index names that must be used |
| `max_plan_rows` | a ceiling on the planner's own row estimate for the statement |

Every assertion is a **structural fact**. There is deliberately no `max_duration_ms`
field: the exit gate forbids grading a timing, and a field that exists is a field someone
will grade.

## Why evaluation comes before timing

`EXPLAIN ANALYZE` reports `actual_time` and `buffers` for each node, and those numbers are
exactly what a learner wants to see. They are also the numbers that move when a laptop is
busy, when a page is cold, and when the plan was chosen under a different `work_mem`.
Grading them would make the exercise a lottery.

M5 therefore reports timing **honestly and ungraded**: the bundle names its precision, the
UI labels it, and the grader reads structure. Where M5 wants to say "this query does less
work", it says it in unit-free structural terms — rows the planner expects to touch,
whether an index was used, how many buffers were read — which are reproducible on a pinned
fixture in a way that milliseconds are not.

## The performance dataset

The shared `dataset` is deliberately small (30 customers, 80 orders) so lessons are fast.
A plan lesson on 80 rows cannot teach anything: the planner correctly chooses a sequential
scan and there is nothing to fix.

Step 4 adds a second sealed template, `perf`:

- The same four relations and the same grain, at a scale factor (`10000` orders, `20000`
  order items, `2000` customers, `500` products) where access paths differ.
- A deliberate index set that a learner can add to, drop and rebuild inside their own
  environment (ADR 0003 gives them `CREATE`), so "watch the plan change" is an experiment
  rather than a claim.
- Rows that make selectivity real: a status column where one value is 90% of the table and
  another is 0.5%, and a `customer_id` skew that makes an index the wrong answer for a
  broad predicate.

The dataset is generated deterministically (a fixed `seed` in `dataset_meta`) so the same
fixture produces the same plan on every machine.

## Honesty rules (do not violate)

1. No graded assertion reads a timing field. `duration_ms` is reported, never compared.
2. `EXPLAIN ANALYZE` executes the learner's statement — so it runs only in a writable
   environment, and an anonymous request is refused rather than silently downgraded to
   `EXPLAIN`.
3. A plan is captured under the **same** caps, deadline and semaphore as a query, so a
   runaway `EXPLAIN ANALYZE` is stopped by the host exactly like a runaway query, with
   `QUERY_TIMEOUT`.
4. When the gateway has no database, `POST /explain` reports `ENVIRONMENT_UNAVAILABLE` —
   it never invents a plan.
5. The raw plan is untrusted text from the engine: it is size-capped, node-capped and never
   inserted into HTML without escaping.

## What this does NOT prove

- It does not prove the learner's query is *fast*; it proves the plan does the work the
  exercise asked for. A course that promised wall-clock speed on arbitrary hardware would
  be lying.
- It does not tune the planner. `settings` is reported read-only; M5 never changes
  `random_page_cost` behind the learner's back to make an exercise pass.
- It does not extend to SQLite. SQLite has no `EXPLAIN ANALYZE` and no cost model of this
  shape; M6's dialect profile is where that difference is declared, and the perf exercise
  type refuses a SQLite chapter with an actionable message.

## Constraints

- The `ExecutionService` seam keeps its shape: `explain()` is a new method on the same
  interface, and a screen never calls `fetch`.
- PostgreSQL stays the only dialect M5 grades (ADR 0004); the plan contract carries
  `engine` so a second dialect can be added without reshaping it.
- `make test`, `make verify` and `make verify-mock` stay green after every step.
