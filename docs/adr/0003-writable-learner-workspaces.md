# ADR 0003: Learner SQL may write, inside a per-account environment database with a reset

- **Status:** accepted
- **Date:** 2026-10-14
- **Deciders:** project owner + agent session
- **Question:** the course claims to teach SQL, but every learner statement runs in a
  read-only transaction (`BEGIN READ ONLY` in PGlite, `pgx.TxOptions{AccessMode: ReadOnly}`
  in the gateway) against a `SELECT`-only grant. `INSERT`, `UPDATE`, `DELETE`, DDL and
  transactions therefore cannot be taught *or practised* at all. What should change, 
  and what has to stay protected?

Supersedes nothing. Extends ADR 0002 (which settled *how* a throwaway environment is
created) by deciding *who gets one and when it is writable*.

## The problem with the current posture

Read-only was the right default for the wrong reason. M1 needed a runner that could not
corrupt the one shared dataset, and read-only was the cheapest way to get it. But it
silently made a large part of the subject unteachable:

- A course that cannot `INSERT` cannot teach `INSERT`, `UPDATE`, `DELETE`, `UPSERT`,
  `RETURNING`, constraints, or `ON CONFLICT`.
- It cannot teach DDL: `CREATE TABLE`, `ALTER TABLE`, `DROP`, indexes, views.
- It cannot teach transactions: `BEGIN`/`COMMIT`/`ROLLBACK`, isolation, savepoints.
- It teaches a false model of the engine: a learner's first instinct, "change
  something and look at it", is refused with an error that is about this app, not
  about PostgreSQL.

Grading is result-based, so the *evaluation* of an answer genuinely only needs to read.
But practice is not grading, and the workbook is where practice happens.

## Decision

**Learner SQL runs writable.** The protection moves from "the statement may not write"
to "the statement writes somewhere it is safe to write":

1. **Signed-in learners get their own environment database**, created as a physical copy
   of the sealed template (ADR 0002) and granted the writable privileges the template
   now carries: `USAGE, CREATE` on `dataset`, `SELECT, INSERT, UPDATE, DELETE` on its
   tables, and `USAGE, SELECT` on its sequences. No `TRUNCATE`, no ownership, no
   `REFERENCES`, no `TRIGGER`.
2. **The environment is reset, not repaired.** `POST /environment/reset` drops the
   database and copies the template again: measured at ~35 ms to create and ~25 ms to
   drop (ADR 0002). A learner who drops a table, deletes every row, or wedges the
   database gets the original back in one action, and so does the UI when the engine
   reports the environment unusable.
3. **Anonymous visitors keep the shared, read-only dataset.** They have no stable
   identity to hang an environment on, and a cookie-keyed environment would turn every
   cookie-less client (curl, the load test, the adversarial suite) into a
   `CREATE DATABASE`. Reads stay open to them; writes require an account, and the
   refusal says so.
4. **`app` is unreachable by construction.** The environment copy contains the `dataset`
   schema and nothing else, so gateway metadata is not merely un-granted, it does not
   exist in the database the learner is connected to. The privilege checks the
   adversarial suite already makes (no `pg_read_file`, no `CREATE ROLE`, no
   `ALTER SYSTEM`, no server-file access) stay true because they were never read-only
   checks: the learner role is `NOSUPERUSER NOCREATEDB NOCREATEROLE`.
5. **The dataset schema is still never writable in place.** The gateway's own database
   keeps the `SELECT`-only grant. A learner's writes cannot reach the shared data
   because their statements do not run there.

## Alternatives rejected

| Alternative | Concrete reason it lost |
| --- | --- |
| **Stay read-only; add a fake "write mode" that rolls back** | Teaches a lie. `INSERT` would report success and the next statement would not see the row. Half the subject stays unteachable, and the first time a learner notices the data did not change the app looks broken. |
| **Writable shared `dataset` schema** | One learner's `DELETE FROM orders` is every learner's. Unrecoverable without a restore, and the app has no way to know who broke it. |
| **Writable per-account *schema* in the shared database** | Splits isolation across a single `querycraft_learner` role: schema grants in PostgreSQL are per-role, so granting every account's schema to the one shared role gives every learner read and write access to every other learner's workspace. Per-account roles would fix it, but then a "session" is a role *and* a schema *and* still inside the database that holds `app`. |
| **Per-statement auto-rollback** | Multi-statement teaching breaks (`INSERT` then `SELECT`), and `DROP TABLE` appearing to work while leaving the table in place is the same lie as the fake write mode. |
| **Refuse writes in the browser adapter too** | The browser already has a private database per learner: nothing to protect. Making the two adapters disagree about the language they teach would also break the "same lesson content on both adapters" rule at exactly the moment it matters. |

## Consequences

- **Connection budget.** Each live environment costs a small pool (ADR 0002 recommends
  1–2 connections, not pgxpool's `max(4, NumCPU)`). `MaxEnvironments` is enforced before
  `CREATE DATABASE`, idle environments are reaped, and both are the M4 hard numbers.
- **Disk.** 7.6 MiB per environment plus WAL (ADR 0002). A cap plus a size gauge makes
  growth visible before it is a problem.
- **Grading must assume a dirty database.** An exercise that expects `SELECT count(*)
  FROM orders` to answer 80 can be satisfied by *changing* the table so that it does.
  This is inherent to result-based grading and is accepted for a learning tool; it is
  the same reason the reset exists.
- **Content must say which environment it assumes.** A chapter that inserts a row must
  be honest that the row is the learner's, not the world's. The reading copy already
  distinguishes "the dataset" from "your workspace"; that language becomes load-bearing.
- **The adversarial suite's read-only cases move.** "Learner cannot INSERT" is no longer
  a security property; "learner cannot INSERT into the *shared* dataset" and "learner
  cannot reach another learner's environment" are. The published suite is updated to
  say which one it proves.
- **New surface.** `GET /environment` and `POST /environment/reset` are authenticated;
  reset is destructive to the caller's own data only, and is rate-limit-able.

## What is not proven here

- No multi-tenant resource accounting at scale (the same gap ADR 0002 records).
- No isolation proof against a learner who can *choose* their account id in the
  environment name; the name is derived, not interpolated, and the derivation is part of
  the implementation review rather than this ADR.
- The reset path is not yet rate limited.
