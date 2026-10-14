# Course audit — every exercise against the live engine

Each exercise's `solution` was executed on the real gateway (`POST /query`
against the seeded `dataset` schema, PostgreSQL 18.3) and then graded against
itself (`POST /answers/evaluate` with the solution as `reference_solution`). If a
reference query does not run, the evaluator can only fall back to a column check,
so a broken reference is a grading bug.

**Result: 16 / 16 exercises run, return the declared columns in order, and pass
their own contract.**

```
exercises: 16  broken: 0
  ok     sql-analytics ex.1.1     rows=5   colsOk=true self=true
  ok     sql-analytics ex.2.1     rows=79  colsOk=true self=true
  ok     sql-analytics ex.3.1     rows=5   colsOk=true self=true
  ok     sql-analytics ex.4.1     rows=6   colsOk=true self=true
  ok     sql-analytics ex.5.1     rows=20  colsOk=true self=true
  ok     sql-analytics ex.5.2     rows=10  colsOk=true self=true
  ok     sql-analytics ex.6.1     rows=5   colsOk=true self=true
  ok     sql-analytics ex.7.1     rows=5   colsOk=true self=true
  ok     sql-analytics ex.8.1     rows=5   colsOk=true self=true
  ok     joins         ex.j1.1    rows=80  colsOk=true self=true
  ok     joins         ex.j2.1    rows=30  colsOk=true self=true
  ok     joins         ex.j3.1    rows=19  colsOk=true self=true
  ok     subqueries    ex.q1.1    rows=9   colsOk=true self=true
  ok     subqueries    ex.q2.1    rows=20  colsOk=true self=true
  ok     subqueries    ex.q3.1    rows=7   colsOk=true self=true
  ok     subqueries    ex.q4.1    rows=7   colsOk=true self=true
```

## Fixes applied

| Where | Problem | Fix |
| --- | --- | --- |
| `ex.q4.1` (subqueries) | Every `Home` product has sold, so the expected answer was **0 rows**: the exercise was degenerate, and any query returning an empty result with the right single column satisfied the contract. | Re-anchored on customers — "every customer who has never placed an order" (7 rows) — and updated the check that referenced `Home` to the `orders` join. |
| `ex.q4.demo` (subqueries) | The bundled `result` claimed `Electronics, Outdoor`; the engine returns `Electronics, Home, Kitchen`. `block.result` is not rendered, but the mock adapter serves it as canned output. | Corrected to the engine's rows. |

## Notes

- `block.result` is never rendered in the live UI. Grading uses the reference
  query executed on the engine (HTTP and PGlite) or the bundled rows (mock only).
- A stale `result` therefore only affects mock mode, and the PGlite fallback used
  when an exercise ships no `solution`.

## Appendix — reproducing

```bash
# a gateway must be running (make serve, or ./backend/bin/querycraft):
make audit-courses
# override the target host with QC_GATEWAY=http://host:port
```

The script lives at `scripts/course-audit.mjs`.
