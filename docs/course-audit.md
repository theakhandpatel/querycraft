# Course audit: every authored exercise against a live engine

`make audit-courses` runs every authored exercise `solution` through a real
gateway and grades it against itself, so a reference query that no longer
matches its own contract is caught before a learner meets it. It is part of
`make test`.

The audit is not a script that reads the catalog and hopes: it imports the
catalog (`web/src/data/curriculum.ts`) and the app's own two resolvers
(`web/src/lib/dialect.ts`, `web/src/lib/dataset.ts`), so the dialect and the
dataset each exercise is graded on are the ones a *screen* would choose: 
block, then module, then course, then the default. It signs up a learner first,
because that is who exercises are graded as: the perf fixture (the `plans`
course) exists only inside a per-account writable environment, and an anonymous
request is correctly refused with "sign in and run the lesson again".

**Result: 53 / 53 exercise solutions run, return the declared columns in order,
and pass their own contract, on the dialect and dataset the course names.**

```
the audit grades as a signed-in learner
  ok     the learner signed up
  ok     the learner has a writable workspace (which is where the perf fixture lives)
  ok     the workspace is the learner's own, not the shared dataset

every authored solution runs and grades against itself
  ok     all 53 exercise solutions ran, matched their contract columns and passed their own grading

the audit is not vacuous
  ok     the catalog produced exercises to run
  ok     at least one built course is in the catalog
  ok     every declared dialect was served (postgresql, sqlite)
  ok     no exercise names a dialect or dataset the app does not serve
  ok     the engine that answered is the dialect that was asked for

by course
  ok     sql-analytics          10 exercise(s)  postgresql/commerce
  ok     joins                   3 exercise(s)  postgresql/commerce
  ok     plans                   8 exercise(s)  postgresql/perf
  ok     subqueries              4 exercise(s)  postgresql/commerce
  ok     foundations             7 exercise(s)  postgresql/commerce
  ok     sqlite-essentials       7 exercise(s)  sqlite/commerce
  ok     sqlite-in-production    7 exercise(s)  sqlite/commerce
  ok     postgres-to-sqlite      7 exercise(s)  sqlite/commerce

course-audit: OK
```

## Why the script was rewritten

The first version POSTed `{sql}` and nothing else. Ask the gateway that and it
runs every statement as PostgreSQL over the commerce dataset: the documented
default, which is fine until a chapter is authored for another dialect.

| Defect | What it hid | Fix |
| --- | --- | --- |
| no `dialect` on the request | after M6, 16 SQLite exercises reported **BROKEN** for a reason that had nothing to do with their content (`sqlite_master`, `typeof`, `rowid`, `strftime`), and 5 more reported **ok** only because their SQL happened to be valid PostgreSQL too: a false pass, which is worse | the dialect is resolved through `dialectFor({ block, module, course })` and sent with the statement; the audit also asserts that *the dialect the result claims* is the one that was asked for |
| no `dataset` on the request | every `plans` exercise was graded against the 80-row commerce fixture instead of the 10 000-row perf fixture its contract was authored for | the dataset is resolved through `datasetFor({ block, exercise, course })` and sent; `plans` is now graded on `perf` |
| no account | the perf fixture lives in a signed-in learner's environment; the audit read the honest refusal as a broken exercise | the audit signs up a learner and grades as them, and asserts the workspace is theirs |
| no exit code | the script printed `broken: 16` and exited 0, so it could not fail a build | it is a `Reporter`; one broken exercise exits 1, and `audit-courses` is in `make test` |

## Notes

- `block.result` is never rendered in the live UI. Grading uses the reference
  query executed on the engine (HTTP and PGlite), and the browser adapter falls
  back to the bundled rows when a block carries no solution. A stale `result`
  therefore only affects that fallback.
- The audit asserts the run is not vacuous (exercises exist, every declared
  dialect was served, no exercise names a dialect or dataset the app does not
  serve), so a catalog that stopped producing statements cannot pass quietly.

## Appendix: reproducing

```bash
# hermetic: builds the gateway and boots it on its own embedded PostgreSQL
make audit-courses

# against a gateway someone else started (nothing is built or stopped)
QC_GATEWAY=http://127.0.0.1:18077 node scripts/course-audit.mjs
```

The script lives at `scripts/course-audit.mjs`.
