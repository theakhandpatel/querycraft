# Adapter parity — the same lesson on both engines

Design source: `docs/technical-design.md` §14 (M3 exit gate: *"The same lesson
content runs against both adapters unchanged"*), §4.2 (QueryResult and the
closed error codes), §6.1 (the value-comparison rule), §5.4 (Adapter B).
Instrument: `scripts/adapter-parity-audit.mjs`.

## What it proves

Every statement a learner can actually run — each exercise's reference solution
and each worked example — is executed twice: once by the browser engine
(`@electric-sql/pglite`, the real PostgreSQL compiled to WebAssembly) and once by
the Go gateway, and the two results must agree. The lesson text is never
rewritten for either side; if they disagree, the product disagrees with itself.

"The same result" is judged by the project's own §6.1 rule
(`web/src/api/compare.ts`, mirrored by `backend/internal/eval/compare.go`), not
by string equality of the JSON:

- status, error code, column names, column types, row count and the `truncated`
  flag must match exactly;
- rows compare cell by cell — numeric strings and numbers compare numerically,
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
anonymous path is then checked separately, as the property it is — an anonymous
caller must be refused a write, must leave the shared dataset untouched, and must
be told it has no workspace rather than be given one.

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
  `LessonScreen` as `<pre className="syntaxblock">{b.sql}</pre>` — a static
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

1. The gateway emitted `numeric` as a JSON number, so `118.00` arrived as `118` —
   the learner's own price lost its scale.
2. A naive `timestamp` was decoded into a time value and re-rendered, moving the
   wall-clock time by the host's UTC offset (`10:30` became `05:00` under
   `Asia/Calcutta`); the browser adapter had the same class of problem because
   PGlite materialises `timestamp` as a JS `Date`.
3. The gateway classified a missing relation (`42P01`) and a missing column
   (`42703`) as `QUERY_SYNTAX_ERROR` by matching the `42` SQLSTATE class prefix,
   where the design files "missing relation" under `QUERY_RUNTIME_ERROR` — and
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
  written against one engine only — which is why the runnable-single-statement
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
