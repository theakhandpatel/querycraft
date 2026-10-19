# Course audit

`make audit-courses` runs every authored exercise `solution` through a real
gateway and grades it against its own contract. A reference query that no longer
matches its contract is caught before a learner meets it. The audit is part of
`make test`.

## What it runs

The audit imports the catalog (`web/src/data/curriculum.ts`) and the app's own
resolvers, `web/src/lib/dialect.ts` and `web/src/lib/dataset.ts`. The dialect and
dataset for each exercise are therefore the ones a screen would choose, with the
same precedence: block, then module, then course, then the default.

It signs up a learner first, because that is who exercises are graded as. The
perf fixture used by the `plans` course exists only inside a per-account writable
workspace. An anonymous request is correctly refused, and grading that refusal
would prove nothing. The audit also checks that the workspace is the learner's
own, not the shared dataset.

For each exercise it:

1. Resolves the dialect and dataset.
2. Posts the reference solution to `/answers/evaluate` with the same contract the
   learner is graded against.
3. Requires the result to succeed, match the declared columns in order, and pass
   its own grading.
4. Checks that the engine that answered is the dialect that was requested.

## Why the checks matter

The audit is not vacuous. It asserts four things beyond the per-exercise result:

- The catalog produced exercises to run.
- At least one built course is in the catalog.
- Every declared dialect was served, and no exercise names a dialect or dataset
  the app does not serve.
- The engine that answered is the dialect that was asked for.

A request without `dialect` or `dataset` would run every statement as PostgreSQL
over the commerce dataset. SQLite exercises would then fail for reasons unrelated
to their content, and a PostgreSQL-shaped SQLite exercise could pass by accident.
A plan exercise would be graded on the wrong fixture. Sending both values, and
asserting the engine that answered, removes both false failures and false passes.

The audit is a `Reporter`. One broken exercise exits non-zero, so the target can
gate a build.

## Reproduce

```sh
# Hermetic: builds the gateway and boots it on its own embedded PostgreSQL.
make audit-courses

# Against a gateway someone else started. Nothing is built or stopped.
QC_GATEWAY=http://127.0.0.1:18077 node scripts/course-audit.mjs
```

The script is `scripts/course-audit.mjs`. An external gateway must have a live
database. Grading a reference against `ENVIRONMENT_UNAVAILABLE` proves nothing,
and the audit refuses that case.

## Notes

The authored `result` rows are not rendered in the live UI. Grading uses the
reference query executed on the engine, in both HTTP and PGlite mode. The browser
adapter falls back to the bundled rows only when a block carries no solution. A
stale `result` therefore affects that fallback and nothing else.

The audit checks the solutions, not the lesson prose. `make audit-lessons` and
`make audit-content` cover the chapter structure and the content seeder.
