# ADR 0002 — One PostgreSQL database per challenge environment, created from a sealed template

- **Status:** accepted
- **Date:** 2026-10-14
- **Deciders:** project owner + agent session
- **Question:** when M4 generates a throwaway challenge, what *is* the environment —
  a separate database, a separate schema, or the one shared schema with row
  scoping — and what does creating and dropping one actually cost here?

Supersedes nothing. Resolves **D11** in `docs/technical-design.md` §2.

## Decision

Each challenge environment is its **own PostgreSQL database**, created as a
physical copy of one **sealed template database**:

```
template0 --CREATE DATABASE--> qc_template  (schema + seed + learner grants, then sealed)
                                    |
              CREATE DATABASE qc_env_<id> TEMPLATE qc_template   -- one statement, ~35 ms
                                    |
              DROP DATABASE  qc_env_<id> WITH (FORCE)            -- ~25 ms, terminates stragglers
```

The template is built once per cluster and **sealed** —
`ALTER DATABASE qc_template IS_TEMPLATE true ALLOW_CONNECTIONS false` — which is
what makes it copyable at any time: PostgreSQL refuses to copy a database that
another session is attached to (measured below), and `ALLOW_CONNECTIONS false`
guarantees no session ever is.

Reset is `DROP` + `CREATE ... TEMPLATE`, not a truncate-and-reseed: the copy *is*
the reseed, so a corrupted or half-written environment is replaced rather than
repaired.

Helpers live in `backend/internal/dataset/template.go`; the measurements are
`backend/internal/dataset/template_test.go`.

## Alternatives rejected

| Alternative | Concrete reason it lost |
| --- | --- |
| **One shared `dataset` schema** (today's shape) | A generated challenge can damage the environment every other learner is using. There is no way to reset one challenge without resetting all of them, and no way to give one challenge different tables or a different scale. |
| **Schema per challenge** | PostgreSQL cannot copy a schema, so "create" becomes re-running schema + seed + grants for every challenge (measured at 55–63 ms, but once per environment instead of once per cluster), and the DDL/seed grows with every generated challenge. It also leaves every challenge inside the gateway's own database next to `app`, so one `search_path` mistake widens the blast radius. |
| **Shared schema with row-level scoping** | Learner SQL is arbitrary. `SELECT * FROM orders` with no filter is an ordinary first move, and a `WHERE` clause the learner writes is not a security boundary. RLS would have to be correct for every future table and every future generated challenge; a database cannot be wrong about who may see it. |
| **A container per environment** | Rejected already in the design (§5.4, §12): far slower to create, and it moves the isolation problem into an orchestration layer the app does not have. The database copy is the fast path; a container per *tenant* remains an open decision for hostile remote users. |

## Measured on this machine

All numbers below are **measured**, not estimated. Cluster: the embedded
PostgreSQL the gateway ships (PostgreSQL **18.3**, `max_connections=100`,
`superuser_reserved_connections=3`), 8-vCPU Linux host, local disk, data
directory under a temporary path. Date: **2026-10-14**. Three runs; ranges are
given where run-to-run variance was visible. Raw JSON from the last run is
reproduced by setting `QC_SPIKE_OUT` when running the test.

| Measurement | Value |
| --- | --- |
| `CREATE DATABASE qc_template TEMPLATE template0` (cold, once per cluster) | 57–71 ms |
| Load schema + seed + learner grants into the template (once per cluster) | 55–63 ms |
| **`CREATE DATABASE qc_env_x TEMPLATE qc_template`** | **min 32 ms, median 34–44 ms, max 65 ms** (n=7/run, 3 runs) |
| First copy in a fresh cluster ("cold") | 32–45 ms — **no meaningful cold penalty**; the template's pages are already in cache |
| Burst of 8 copies | 353–457 ms total, 44–57 ms each — linear, no contention |
| `DROP DATABASE` (idle) | min 24 ms, median 24–29 ms; **the first drop in a cluster took ~2.9 s** (see below) |
| `DROP DATABASE ... WITH (FORCE)` with a live learner session | 128–133 ms — the session is terminated and the database goes |
| Reset (`DROP` + `CREATE` from template) | 164–228 ms |
| Sealing the template | 1–4 ms |
| Copy size on disk | **7.6 MiB per environment** (template itself 7.8 MiB); 15 environments = 114.5 MiB |
| Data carried by the copy | identical to the template: `customers` 30, `products` 20, `orders` 80, `order_items` 160, `dataset_version` 1 |
| Learner role in a fresh environment | connects without any re-grant; `search_path = 'dataset'`; `SELECT` on `dataset` = true, `INSERT` = false; a join + `GROUP BY` + aggregate returns 4 rows using unqualified names |
| Learner sessions before the server refused | 96 measured against an arithmetic limit of 97 (`max_connections` − reserved) |
| Server restart with 3 environments present | back in ~156–184 ms; all 3 databases survived, holding 22.9 MiB; **nothing reaps them** |

**The 2.9 s first drop is not noise, it is a checkpoint.** PostgreSQL forces a
checkpoint before it can unlink a dropped database's files. The first drop after
write activity pays for it; the next drops in the same cluster were 24–29 ms.
Budget for it (do drops off the request path, or reap in a batch) rather than
believing a single sample.

### The two questions the spike was asked to answer by executing them

**Can the restricted learner role connect to a new database, and what grants does
it need?** Yes, and none beyond what the template already holds. `querycraft_learner`
is a cluster-wide role; the copy carries `pg_class.relacl` (the `GRANT SELECT`)
and the schema's `USAGE` grant with it, because grants are per-database *objects*
stored in the catalogs being copied. Measured: `has_table_privilege('dataset.orders','SELECT')`
is true in a fresh copy, `INSERT` is false, and a real analytic query runs. A copy
made from an un-seeded or un-granted database would instead fail every learner
query with `permission denied` — grants are the one thing that cannot be assumed.

**How does this interact with `search_path = dataset` as a role default?** The
default is a *cluster-wide* `pg_db_role_setting` row (`datbase = 0`), so it
applies in every database on the cluster with no work per environment — which is
why the copy needs no session setup. The flip side is that it is also not
scoped: the learner role can attach to the gateway's own metadata database
(measured: `CONNECT` is allowed because `PUBLIC` holds it by default) and there
finds its own copy of the `dataset` schema. That is not a leak of another
learner's rows — progress and events live in `app`, which the role cannot read —
but it is a surface that should be closed explicitly (below).

## Failure modes and mitigations

1. **Connection-slot exhaustion.** `max_connections = 100`; a non-superuser role
   may hold 97 (measured 96). An environment costs **0 slots at rest** — the
   database exists, no session does — but a pool kept open per environment costs
   `MinConns` (1) and can grow to `MaxConns` (8 on this 8-vCPU host, pgxpool's
   `max(4, NumCPU)`). 12 environments at full pool exhaust the server.
   *Mitigation:* cap the number of **active** environments (not just created
   ones); close an environment's pool after an idle period; set a smaller
   per-environment `MaxConns` than the global default; keep the existing global
   `MaxConcurrent` semaphore as the statement-level backstop.
2. **Disk growth.** 7.6 MiB per environment plus WAL. A thousand leaked
   environments is ~7.6 GiB. *Mitigation:* a hard cap on environments, the reaper
   below, and a `sum(pg_database_size(...))` gauge in the metrics registry so
   growth is visible before it is a problem.
3. **Orphaned databases after a crash or a killed process.** Measured: three
   environments survived a server restart and nothing on the boot path noticed.
   A crash between "create the database" and "record it" leaks one forever.
   *Mitigation:* `ReapEnvironmentDatabases(ctx, admin, keep)` — call it on boot
   with the environments the store still references; it drops the rest. The
   store, not the database, is the source of truth. `DROP ... WITH (FORCE)` means
   one abandoned connection cannot pin a database indefinitely.
4. **A burst of creates.** 8 copies took 353–457 ms and scaled linearly; there is
   no lock contention to exploit. The cap must therefore be enforced *before* the
   burst, not discovered from it. A burst of drops is cheap after the first one,
   which pays the checkpoint.
5. **Grants missing in a fresh environment.** The copy carries whatever the
   template had. A template built without `EnsureLearnerRole` produces
   environments where every learner query fails with `permission denied`.
   *Mitigation:* the template is only published after `LoadIntoTemplate`; M4 must
   never copy from `template0`; add a boot assertion that the sealed template
   answers `has_table_privilege('dataset.orders','SELECT')` as the learner.

## Operational plan for M4

- **Cap concurrent environments** in the store (a small, explicit number —
  single-digit on this machine), enforced before `CREATE DATABASE`, surfaced as a
  typed failure rather than a PostgreSQL error.
- **Reap on boot**: `ReapEnvironmentDatabases` with the environments the store
  still references.
- **Drop after idle**: a bounded idle window after which an environment's pool is
  closed and the database is dropped; the next request recreates it from the
  template (~35 ms), which is cheaper than keeping 97 of them alive.
- **Reset = DROP + CREATE from template**, exposed as one operation that measures
  and reports its own duration.
- **Shrink the per-environment pool.** The global pool's `max(4, NumCPU)` default
  is far more than one challenge needs; environments should open 1–2 connections.
- **Revoke `CONNECT` on the metadata database from the learner role** so the
  cluster-wide role is not also a cluster-wide reader.
- **Record create/drop duration** in the metrics registry (M3 step 1 already asks
  for "environment create/reset time").

## What is not proven

This is a spike, not the M4 implementation:

- **No request path uses it.** The gateway still runs every learner statement
  against the one shared `dataset` schema. Nothing here is wired into
  `backend/internal/engine/` or `backend/internal/httpapi/`.
- **No isolation proof against a determined learner.** The spike proves the
  learner role's grants travel with the copy; it does not prove a learner cannot
  reach another environment's database, nor that the role's PUBLICATION/CONNECT
  defaults are closed. Adversarial work is M3 step 3, and it does not yet cover
  cross-environment access.
- **No multi-tenant resource accounting.** Disk and connections were measured for
  one environment at a time plus one burst; the growth curve for hundreds of
  concurrent environments, and the interaction with WAL, autovacuum and
  checkpoints, is not measured.
- **No multi-statement or long-running workload measured.** Copies were idle
  templates. A copy taken while the source is being written is not a case this
  covers (the seal prevents it, by construction, but it is not tested).
- **Nothing here runs on the deployed Docker path.** `docker-compose.yml` and
  `deploy/postgres/initdb/` describe one server with a database per environment;
  the spike ran against the embedded server only.
- **The 96-vs-97 connection count is unexplained.** Measured 96 learner sessions
  where the arithmetic says 97; recorded honestly rather than smoothed.
