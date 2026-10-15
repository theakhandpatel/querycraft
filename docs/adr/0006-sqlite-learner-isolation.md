# ADR 0006 — Per-learner SQLite isolation: one file per account, reset by re-copy

- **Status:** accepted
- **Date:** 2026-10-15
- **Deciders:** project owner + agent session
- **Question:** ADR 0003 gives a signed-in learner a writable workspace and a reset, and
  PostgreSQL implements that as *database copied from a sealed template*. SQLite has no
  server, no `CREATE DATABASE … TEMPLATE`, no role and no `DROP DATABASE` — the file **is**
  the database. What is the equivalent, what does it cost here, and what must a reset do
  that PostgreSQL's reset does not have to do?

Settles **D14** in `docs/technical-design.md` §15. Extends ADR 0003 (who gets a writable
workspace, and what reset means) and ADR 0002 (how one is created) to the second dialect
ADR 0004 accepted; ADR 0004's own estimate — *"a file copy costs ~1 ms and no connection
at all"* — is re-measured below rather than restated.

## Context

M6 step 4 shipped SQLite as the gateway's second dialect (`modernc.org/sqlite`, pure Go,
cgo-free). What it did **not** ship is the reset half of ADR 0003: `backend/internal/httpapi/sqlite.go`
holds the store (`SQLiteEnvironments`), `/query` and `/answers/evaluate` route to it, and a
signed-in learner's writes land in their own file — but there is no way to give that learner
their fixture back, and no template for them to be copied from.

ADR 0003's model is three sentences, and each one needs a SQLite answer:

| ADR 0003 (PostgreSQL) | SQLite equivalent | Difference that matters |
| --- | --- | --- |
| A signed-in learner gets **their own database**, copied from `qc_template` | a file `<root>/accounts/<hash>.db`, seeded from the committed schema + seed | a copy of a 45 056-byte file, not `CREATE DATABASE … TEMPLATE` |
| **Anonymous** visitors keep the shared, read-only dataset | one shared `<root>/commerce.db`, opened `mode=ro` **and** `query_only(1)` | the same file can be handed to every visitor, because no one can write it |
| **Reset** is `DROP` + `CREATE … TEMPLATE` — the copy *is* the reseed | close the pool, replace the file, remove the journal sidecars | dropping is an `unlink`; there is no `WITH (FORCE)` to terminate a straggler |

The isolation property is the same in both dialects and is *by file*: one learner's `INSERT`
is visible only to the file it was written to, so a wrong `DELETE` breaks that learner's copy
and nobody else's.

## Decision

1. **One database file per account**, `<env-root>/accounts/<sha256(account-id)[:16]>.db`. The
   digest keeps an email address out of the data directory's filenames while still giving two
   accounts two files. This is the direct analogue of `envdb`'s `acct_<digest>` database name.
2. **A sealed template file is the copy source**, built once from the committed SQLite
   schema + seed, and then treated as read-only. "Create" is *copy the template*, not *re-run
   the DDL and the seed*: this is what makes create and reset the same cheap operation, and it
   is what ADR 0002 chose for PostgreSQL.
3. **Reset is replace, not repair**: close the environment's pool, replace the file with a
   fresh copy of the template, and delete any `<file>-wal` / `<file>-shm` sidecars. A
   half-written or corrupted file is replaced, exactly as a wedged database is dropped.
4. **The replacement must be atomic** (`copy to <file>.reset`, then `rename`), so a *fresh*
   reader sees either the whole old fixture or the whole new one. This is the same private-
   file-then-rename pattern the seeding path already uses.
5. **Reset must invalidate the environment's cached pool first.** Measured below: renaming a
   new file over an open connection's path leaves that connection serving the *pre-reset*
   data. PostgreSQL has the same rule (`DROP` invalidates the executor) and the same
   consequence in code: `envdb.Reset` returns a duration and the manager forgets the pool.
6. **Journal mode stays SQLite's default (`delete`), i.e. no `journal_mode` pragma anywhere.**
   The measured reasons are in §"Journal mode" below: a WAL file's committed rows are not in
   the `.db`, so a copy-based reset would need a checkpoint *and* sidecar removal, and a stale
   `-wal` left next to a replaced file silently resurrects the pre-reset data.
7. **Write contention is answered with a busy timeout, not with retries.** The shipped pragma
   set (`foreign_keys(1)`, `busy_timeout(2000)`) makes a second writer wait up to 2 s and then
   fail with `SQLITE_BUSY`; the profile classifies that as `QUERY_TIMEOUT` (a retryable
   condition), not as an environment fault.
8. **Anonymous stays read-only**, exactly as ADR 0003 requires: no account, no per-learner
   file, and a write is refused by the engine (`SQLITE_READONLY` → `QUERY_RUNTIME_ERROR`) with
   no app-level check to keep in sync.

## Measured on this machine

Everything below was measured **on this repository**, not taken from the literature. Host:
Linux, 8 vCPU, local disk, Go 1.25.3, `modernc.org/sqlite` v1.38.2, SQLite **3.50.3**
(`SELECT sqlite_version()`), PostgreSQL **18.3** (the embedded server the gateway ships).
The working tree was uncommitted at `7c951de11`; the files the numbers come from were:

```
fcae4fa72285251d9d96ec7782c7d807106d601e200b27fef1b9d3ceec441c5f  backend/internal/engine/sqlite.go
db0b4ed3db909ed6bda271fa0a2393b54f94dfe8c85d5e79fcf7ef46574c04a6  backend/internal/httpapi/sqlite.go
625c1ba7f8b4044802257513dc5c45f11a018c101ec8b2da8aa70fd9eeacdf17  backend/internal/dataset/sqlite_dataset.go
```

Two probes were used, both throwaway and both outside the tree:

- **The manager's own test helper**, for the PostgreSQL side (so the comparison is the
  package that ships, not a re-implementation):

  ```sh
  cd backend
  go test ./internal/envdb/... -count=1 -v                      # the shipped suite
  # plus a /tmp test that calls envdb.New + pgtest.Start directly:
  cd /tmp/qcisol && go test . -run TestEnvdbManagerNumbers -count=1 -v
  ```

- **A file-lifecycle probe**, for the SQLite side, which imports the real packages
  (`dataset.SQLiteSchemaSQL/SeedSQL`, `engine.NewSQLiteExecutor`,
  `engine.SQLiteReadOnlyPragmas`, `httpapi.NewSQLiteEnvironments`):

  ```sh
  cd /tmp/qcisol && go run .            # phases 1–8 and 9–14
  ```

  and a **live gateway** for the learner-visible shape:

  ```sh
  cd backend
  QC_ADDR=127.0.0.1:8123 QC_NO_LOCAL_DB=1 QC_SQLITE_DATA=/tmp/qcisol/gw-sqlite  go run ./cmd/api
  QC_ADDR=127.0.0.1:8124 QC_PG_PORT=54399 QC_PG_DATA=/tmp/qcisol/pg2 QC_SQLITE_DATA=/tmp/qcisol/gw2-sqlite  go run ./cmd/api
  bash /tmp/qcisol/http-probe.sh    # anonymous + two accounts, no PostgreSQL
  bash /tmp/qcisol/http-probe2.sh   # the same, plus a real PostgreSQL workspace and reset
  ```

### PostgreSQL baseline, measured through `envdb` itself

```
envdbprobe_test.go:44: EnsureTemplate (CREATE DATABASE template0 + schema + seed + learner grants + seal) = 112.015404ms
envdbprobe_test.go:52: Ensure(0) create-from-template = 42.649953ms
envdbprobe_test.go:52: Ensure(1) create-from-template = 44.457343ms
envdbprobe_test.go:52: Ensure(2) create-from-template = 44.486351ms
envdbprobe_test.go:52: Ensure(3) create-from-template = 61.368255ms
envdbprobe_test.go:52: Ensure(4) create-from-template = 45.623034ms
envdbprobe_test.go:62: pg_database_size(qc_env_acct_40d4469bf5a151dddf3bbe3c1c8c5ecb) = 8165055 bytes (7.8 MiB)
envdbprobe_test.go:72: Reset(0) drop+create-from-template = 988.749389ms (reported 986.663877ms)
envdbprobe_test.go:72: Reset(1) drop+create-from-template = 54.53436ms (reported 53.131346ms)
envdbprobe_test.go:72: Reset(2) drop+create-from-template = 47.320894ms (reported 46.024822ms)
envdbprobe_test.go:78: Drop (close pool + DROP DATABASE) = 169.691333ms
envdbprobe_test.go:81: metric querycraft_environment_create_seconds: count=5 sum=0.231s mean=46.3ms
envdbprobe_test.go:84: metric querycraft_environment_reset_seconds: count=3 sum=1.086s mean=361.9ms
```

The same day, `go test ./internal/dataset -run TestTemplateEnvironmentSpike -v -count=1`
(ADR 0002's instrument, re-run) reported `template copies (n=7 …): min=27ms median=30ms
max=42ms`, `drop (n=4): min=21ms median=22ms max=2.89s` (the **first** drop pays a forced
checkpoint), `reset (DROP + CREATE from template) = 158ms`, and `one environment = 7.6 MiB`.

### SQLite file lifecycle

Template build once, then every account operation, from one run of the probe (verbatim):

```
root: /tmp/qcprobe-1655110653
host: linux /, GOMAXPROCS-ish NumCPU=8
schema script = 2737 bytes, seed script = 11806 bytes
conn pragmas (engine.SQLiteReadOnlyPragmas) = [foreign_keys(1) busy_timeout(2000)]

== phase 1: template build (schema + seed into one file, once) ==
  template build (open + schema Exec + seed Exec + close) = 29.4ms
  template file size = 45056 B (0.04 MiB)
  row counts in the template = map[customers:30 order_items:160 orders:80 products:20]
  PRAGMA journal_mode (fresh template, as seeded) = "delete"
  PRAGMA page_count*page_size = 11 * 4096
  files on disk after build = [template.db(45056 B)]

== phase 2: per-account create by copy of the sealed template ==
  copy template -> <account>.db (+fsync+close)   n=10 min=1.9ms median=2.5ms max=35.6ms
                                               raw=[1.9ms, 2.0ms, 2.1ms, 2.4ms, 2.5ms, 2.5ms, 2.6ms, 2.7ms, 3.3ms, 35.6ms]
  copy template -> <acct>.db.reset + rename      n=10 min=1.9ms median=2.7ms max=35.3ms
                                               raw=[1.9ms, 2.1ms, 2.1ms, 2.3ms, 2.4ms, 2.7ms, 3.4ms, 4.1ms, 30.6ms, 35.3ms]
  os.Link template -> <account>.db (rejected?)   n=10 min=8µs median=12µs max=14µs
  per-account file size = 45056 B (0.04 MiB)
  copied file answers SELECT count(*) FROM orders = 80

== phase 3: the shipped store (httpapi.SQLiteEnvironments) ==
  root = /tmp/qcprobe-1655110653/store, accounts dir = /tmp/qcprobe-1655110653/store/accounts
  account executor: dialect=sqlite engine=SQLite 3.50.3 count=[[30]] readable=false
  ForAccount cold (seed from schema+seed)        n=5 min=13.7ms median=16.0ms max=48.1ms
                                               raw=[13.7ms, 14.0ms, 16.0ms, 47.3ms, 48.1ms]
  ForAccount warm (cached pool)                  n=5 min=0µs median=1µs max=1µs
                                               raw=[0µs, 1µs, 1µs, 1µs, 1µs]
  account file 4803fb5de77be0b0ad9d0c3c744bc0a9.db = 45056 B; sidecars=[4803fb5de77be0b0ad9d0c3c744bc0a9.db(45056 B)]
  Shared() cold (build the anonymous fixture) = 15.0ms
  shared executor: dialect=sqlite count=[[30]] readOnly=true
  shared fixture (anonymous) file: [commerce.db(45056 B)]

== phase 4: reset (re-copy from the template) ==
  after 500 INSERTs (autocommit, one connection): size = 65536 B, count = 530, journal_mode = "delete"
  reset by copy over the file (no rename)        n=5 min=1.5ms median=1.8ms max=2.0ms
  reset by copy + atomic rename                  n=5 min=1.6ms median=1.7ms max=1.9ms
  a learner's rows are gone after reset: 500 writes then re-copy -> count = 30

== phase 5: drop (unlink) ==
  os.Remove one account file                     n=5 min=33µs median=37µs max=40µs
  drop 8 account files (one at a time) = 260µs
```

The same probe run a second time on the same host, verbatim, for the ranges that moved:

```
  template build (open + schema Exec + seed Exec + close) = 16.0ms
  copy template -> <account>.db (+fsync+close)   n=10 min=1.9ms median=2.1ms max=2.6ms
  copy template -> <acct>.db.reset + rename      n=10 min=1.7ms median=2.1ms max=3.1ms
  ForAccount cold (seed from schema+seed)        n=5 min=12.6ms median=13.0ms max=13.7ms
  Shared() cold (build the anonymous fixture) = 13.0ms
  reset by copy over the file (no rename)        n=5 min=1.8ms median=2.2ms max=2.2ms
  reset by copy + atomic rename                  n=5 min=1.7ms median=2.1ms max=2.3ms
  os.Remove one account file                     n=5 min=34µs median=37µs max=44µs
  drop 8 account files (one at a time) = 121µs
```

**Reading it.** Copy-create is ~2 ms (min 1.7–1.9, median 2.1–2.7) and reset-by-re-copy is the
same ~2 ms — roughly **20× cheaper than PostgreSQL's 42–46 ms create**, and ~25–80× cheaper
than its 47–160 ms reset. The 30–38 ms outliers appear only in the run taken while a second
agent session was compiling on the same host; with the host quiet the maximum over 40 copies
was 3.1 ms. The 45 056-byte file is **0.04 MiB against PostgreSQL's 7.8 MiB per environment**
(the seeded page count is 11 × 4096 B). Dropping is an `unlink`: 33–44 µs, against
`DROP DATABASE`'s 22 ms (and 169.7 ms with the pool closure `envdb.Drop` performs).

The shipped store's create path is **not yet** a template copy: `ForAccount` runs the schema
and the seed into a private `…db.seed` file and renames it (~13–16 ms, and 47–48 ms under
load), because no template file exists yet. Everything else about it is already the shape
this ADR decides: lazy creation on first use, a cached pool per account (a *warm* call is
sub-microsecond), no sidecars on disk, and the anonymous fixture built the same way.

Introducing the template changes the *latency*, not the *content*: the probe builds the
template with the same two scripts the store seeds from, and the two are byte-identical —
one `sha256` covers the template and every account file the shipped `ForAccount` produced,
and `cmp` reports 0 differing bytes:

```
$ sha256sum /tmp/qcprobe-600998246/template.db /tmp/qcprobe-600998246/store/accounts/*.db
29f919672d5eb60f2f9fc9344d80e8768fd487c54acff9e1d189c6c101c1b2ac  …/template.db
29f919672d5eb60f2f9fc9344d80e8768fd487c54acff9e1d189c6c101c1b2ac  …/accounts/4803fb5de77be0b0ad9d0c3c744bc0a9.db
$ cmp -l template.db store/accounts/4803fb5de77be0b0ad9d0c3c744bc0a9.db | wc -l
0
```

### Journal mode, sidecars, and what a copy misses

Verbatim from the probe:

```
== phase 6: journal mode, sidecars, and what a copy misses ==
  after _pragma=journal_mode(WAL): PRAGMA journal_mode = "wal"; files = [wal.db(45056 B)]
  after 200 committed INSERTs with the writer still open (no checkpoint):
    files = [wal.db(45056 B) wal.db-shm(32768 B) wal.db-wal(836392 B)]
    copy of the .db alone while the writer is open -> count = 30 (want 230)
    PRAGMA wal_checkpoint(TRUNCATE) -> busy=0 log=0 checkpointed=0
    files after checkpoint = [wal.db(53248 B) wal.db-shm(32768 B) wal.db-wal(0 B)]
    copy of the .db after a checkpoint -> count = 230 (want 230)
  stale-sidecar hazard (rename a fresh copy over a WAL file with a live -wal):
    before reset: count = 130, files = [hazard.db(45056 B) hazard.db-shm(32768 B) hazard.db-wal(424392 B)]
    after copy+rename over the same path: on-disk files = [hazard.db(45056 B) hazard.db-shm(32768 B) hazard.db-wal(424392 B)]
    read back through a fresh connection: count = 130 (30 = reset worked, 130 = the stale -wal was replayed)

== phase 12: opening a WAL-mode file read-only (why journal_mode is absent from the read-only set) ==
  writer open in WAL mode, 100 committed inserts; files = [walro.db(45056 B) walro.db-shm(32768 B) walro.db-wal(424392 B)]
  read-only open (shipped pragma set) OK: count = [[130]]
  read-only open with journal_mode(DELETE) forced FAILED: execution environment unavailable: database is locked (5) (SQLITE_BUSY)
  after the writer closed: files = [walro.db(53248 B)]
  read-only open after close OK: count = [[130]]

== phase 13: cost of 200 autocommit INSERTs, rollback journal (shipped) vs WAL ==
  converted cost-wal.db: PRAGMA journal_mode = "wal"
  rollback journal (DELETE): total=966.0ms mean=4.8ms/statement
  WAL: total=389.1ms mean=1.9ms/statement
  counts: delete-mode=230, wal-mode=230
```

Three findings, stated as measured:

1. **A WAL file's committed rows are not in the `.db` file.** 200 committed INSERTs with the
   writer still open produced an 836 392-byte `-wal` sidecar and a 45 056-byte `.db` that
   still answered **30** rows; only after `wal_checkpoint(TRUNCATE)` did a copy of the `.db`
   answer 230. A copy-based reset therefore either checkpoints first or copies a file that is
   missing whatever has not been checkpointed.
2. **A stale sidecar defeats the reset.** Renaming a fresh copy over a file that still had
   `-wal`/`-shm` next to it left **130** rows visible through a fresh connection: the old
   `-wal` was replayed on top of the new file. `journal_mode` is deliberately absent from
   `SQLiteReadOnlyPragmas`, so with today's default (`delete`) there are no sidecars to leave
   behind — and if WAL were ever adopted, replacement would have to delete them explicitly.
3. **WAL is genuinely faster for writes** — 1.9–2.1 ms per autocommit INSERT against
   4.8–4.9 ms in rollback-journal mode, ~2.5×. That is the real price of decision 6, and it
   is worth paying for a reset that is a plain file copy: the write-heavy lessons here are
   tens of statements, not tens of thousands.

Also measured, because the read-only path's comment in `engine/sqlite.go` warns about it: a
read-only open of the WAL file with the shipped pragma set **succeeded** on this host and read
the uncheckpointed 100 rows (130 total); forcing `journal_mode(DELETE)` on a read-only open
of the same file **failed** — with `SQLITE_BUSY`, not the "disk I/O error (3850)" the comment
predicts (the difference is a live writer holding the file when the open was attempted).
Recorded as measured: the `delete` default keeps the anonymous read-only path free of both
cases, and the comment's rationale is right for the wrong reason.

### Write contention, one connection per environment

Verbatim from the probe:

```
== phase 7: writer contention, one connection per environment ==
  (a) two pools on the SAME file, busy_timeout(2000), MaxOpenConns=1 each, 50 autocommit INSERTs each:
      elapsed = 1.45s; errors = []
  (b) the same, with busy_timeout(0) so a lock is not waited out:
      elapsed = 465.9ms; errors = [w1 i0: database is locked (5) (SQLITE_BUSY)]
  (c) two ACCOUNTS, two different files, both writing: 2 x 200 INSERTs
      elapsed = 3.29s; counts = 230 / 230 (want 230 / 230)
  (d) the shipped pool size (sqlitePoolConns=4) inside ONE account, 4 goroutines x 50 INSERTs:
      elapsed = 2.09s; errors = []; count = 230

== phase 8: anonymous path stays read-only (engine seam) ==
  SELECT count(*) = [[30]] (dialect sqlite, engine SQLite 3.50.3)
  DELETE FROM customers -> status=failed code=QUERY_RUNTIME_ERROR message="attempt to write a readonly database"

== phase 10: reset while a reader is running (copy-in-place vs atomic rename) ==
  copy over the file in place    readers: 59 readable, 0 UNREADABLE (torn); observed: 30 rows x58, 530 rows x1
  copy to .reset then rename     readers: 195 readable, 0 UNREADABLE (torn); observed: 30 rows x3, 31 rows x13, 32 rows x29, 33 rows x3, 34 rows x2, 35 rows x11, 40 rows x134

== phase 11: reset while a pool is open on the file (does it have to be closed first?) ==
  copy over the file in place    before reset via the open pool = 530; after reset: open pool = 30, fresh connection = 30
  copy to .reset then rename     before reset via the open pool = 530; after reset: open pool = 530, fresh connection = 30

== phase 14: two writers, one file, busy_timeout(2000) (deterministic) ==
  holder holds for 300ms -> second writer succeeded after waiting 335.5ms
  holder holds for 2.6s -> second writer FAILED after 2.00s: database is locked (5) (SQLITE_BUSY)
```

- **One writer per file does not need retries, it needs the timeout.** With
  `busy_timeout(2000)` a second writer that overlaps a 300 ms holder waits 335 ms and
  succeeds; a holder that runs past the timeout makes the loser fail after exactly 2.00 s with
  `SQLITE_BUSY`. With `busy_timeout(0)` the loser fails immediately. That is why the pragma
  lives in the DSN rather than in a lesson: without it, a two-tab learner meets a lock error
  that says nothing about what happened.
- **Two accounts do not contend at all.** Two files, 400 autocommit INSERTs, both complete:
  1.8–3.3 s across three runs (the spread is the shared 8-vCPU host, not the lock — the
  per-statement cost in the same runs was a stable 4.8–4.9 ms).
- **The shipped pool of 4 connections *inside* one account is the one case that can fail.**
  Four concurrent writers finished green in the two quiet runs (2.09 s, 1.30 s) and produced
  two `SQLITE_BUSY` failures in the run taken under compile load. `envdb` opens 1–2
  connections per environment; a learner's SQLite environment should follow that, and a
  single write connection per account is the shape that matches `busy_timeout` — anything
  more is contention the account created with itself.
- **Replacement is atomic for a fresh reader, and only for a fresh reader.** In 195 reads
  across 20 renames a reader never once saw an unreadable file; a copy-in-place run was also
  clean over 59 reads (the window is ~2 ms and the truncated file is small), so the race
  probe proves *nothing bad was observed*, not *nothing can happen*. What the probe does show
  deterministically is phase 11: after a rename, a pool that was open before the reset still
  reads the **pre-reset** 530 rows while a fresh connection reads 30. Closing the pool first
  (ADR 0003's "reset invalidates the executor") is not an optimization.
- **Anonymous refusal is the engine's**, not the app's: `DELETE` on the shared fixture returns
  `QUERY_RUNTIME_ERROR / "attempt to write a readonly database"`, from `mode=ro` +
  `query_only(1)`. There is no second check in Go to drift out of sync.

### The learner-visible shape, over HTTP

Booted gateway, no PostgreSQL at all, on `:8123`:

```
$ curl -s http://127.0.0.1:8123/health
{"dialects":["postgresql","sqlite"],"engine":"postgresql","status":"ok","version":"dev"}

$ anonymous: POST /query dialect=sqlite (committed fixture)
{"status":"succeeded","columns":[{"name":"n","type":"expression"}],"rows":[[30]],"row_count":1,"truncated":false,"duration_ms":0.1,"error":null,"request_id":"rq_18tywt","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ anonymous: POST /query dialect=sqlite DELETE (must be refused)
{"status":"failed","columns":[],"rows":[],"row_count":0,"truncated":false,"duration_ms":0.1,"error":{"code":"QUERY_RUNTIME_ERROR","message":"attempt to write a readonly database (8)"},"request_id":"rq_vu1gqj","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ alice: sqlite INSERT into her own file
{"status":"succeeded","columns":[],"rows":[],"row_count":0,"truncated":false,"duration_ms":4.4,"error":null,"request_id":"rq_i89z7x","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ alice: count (want 31)
{"status":"succeeded","columns":[{"name":"n","type":"expression"}],"rows":[[31]],"row_count":1,"truncated":false,"duration_ms":0.2,"error":null,"request_id":"rq_r0vydv","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ bob: count (want 30: bob must not see alice's row)
{"status":"succeeded","columns":[{"name":"n","type":"expression"}],"rows":[[30]],"row_count":1,"truncated":false,"duration_ms":0.1,"error":null,"request_id":"rq_egb1og","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ bob: rows named 'Alice Only' (want 0)
{"status":"succeeded","columns":[{"name":"n","type":"expression"}],"rows":[[0]],"row_count":1,"truncated":false,"duration_ms":0.2,"error":null,"request_id":"rq_a4fl4j","engine":"SQLite 3.50.3","dialect":"sqlite"}

$ files under the SQLite data root
/tmp/qcisol/gw-sqlite/accounts/76752ecb0dabde3343fc490e503a5fc9.db (45056 B)
/tmp/qcisol/gw-sqlite/accounts/ee06ec758cc0b7394590cffb98c9e2e8.db (45056 B)
/tmp/qcisol/gw-sqlite/commerce.db (45056 B)
```

Two accounts, two files, and the row count that proves it. The same gateway with the embedded
PostgreSQL attached (`:8124`) answers `"engine":"PostgreSQL 18.3"` and serves both dialects,
and there the **reset** routes show what step 5 still owes:

```
$ alice: POST /environment/reset
{"duration_ms":526.011964,"environment":{...},"ok":true}
$ repeat the reset 3x to get a range on the manager's reset duration
reset 1: 115.452694 ms
reset 2: 124.274757 ms
reset 3: 156.554025 ms

$ alice: POST /environment/reset?dataset=sqlite (is there a SQLite reset?)
{"status":"failed","columns":[],"rows":[],"row_count":0,"truncated":false,"duration_ms":0,"error":{"code":"ENVIRONMENT_UNAVAILABLE","message":"This gateway serves the \"commerce\" and \"perf\" datasets; \"sqlite\" is not one of them."},"request_id":"rq_pl6ysg","engine":"postgresql","dialect":"postgresql"}

$ alice: sqlite count after both resets (does the row survive?)
{"status":"succeeded","columns":[{"name":"n","type":"expression"}],"rows":[[31]],"row_count":1,"truncated":false,"duration_ms":0.1,"error":null,"request_id":"rq_wp442l","engine":"SQLite 3.50.3","dialect":"sqlite"}
```

`POST /environment/reset` resets the **PostgreSQL** challenge workspace (526 ms cold, then
115–157 ms — the first call pays the drop that the ADR 0002 spike also saw take ~2.9 s once
per cluster) and leaves alice's SQLite row at 31; asking for the SQLite dialect by name is
refused as an unknown **dataset**, because no SQLite reset route exists yet.

### Side by side

| Operation | PostgreSQL (measured today) | SQLite (file, measured today) |
| --- | --- | --- |
| Build the template once | `CREATE DATABASE` + schema + seed + grants + seal = 112 ms | schema + seed into one file = 16–29 ms |
| Create one environment | copy from template = **42.6–61.4 ms** (mean 46.3 ms) | copy 45 056 B = **~2 ms** |
| Reset | drop + copy = **47–54 ms** warm, 989 ms cold | re-copy + rename = **~2 ms** |
| Drop | `DROP DATABASE` 22 ms; 169.7 ms through `Drop` (pool close) | `unlink` = **~35 µs** |
| Disk per environment | **8 165 055 B (7.8 MiB)** | **45 056 B (0.04 MiB)** |
| Cost at rest | 0 connection slots; a pool is 1–2 | 0 (a cached pool holds 1–4) |
| Contention knob | `max_connections` (97 usable) | file lock + `busy_timeout(2000)` |
| Writes at rest | WAL, checkpointed by the server | rollback journal, in the `.db` |

ADR 0004's estimate is confirmed and corrected: the copy is **~2 ms**, not ~1 ms, once the
`fsync` and the close are included — still ~20× cheaper than the database copy it was
comparing against, and with no connection budget at all.

## Alternatives rejected

| Alternative | Concrete reason it lost (measured) |
| --- | --- |
| **Hard link the template as each account's file** | 7–14 µs, 200× cheaper than the copy — and not isolation: after one `INSERT` through the link both names answered **31** rows and `os.Stat` reported the *same inode* (1046390). It is one database with two names. |
| **Copy over the file in place** | Works (`os.Create` truncates the same inode, and both the open pool and a fresh reader saw the new 30 rows), but it is not atomic: for the length of the copy a reader can open a truncated file. The rename costs the same ~2 ms (1.7–2.1 vs 1.7–2.2) and removes the window. |
| **Replace by rename without closing the pool** | Measured to fail silently: the pre-reset pool kept reading **530** rows while a fresh connection read 30 — an environment where "Reset" appears to do nothing. The pool must be closed (decision 5), which is exactly what `envdb.Reset` already does for PostgreSQL. |
| **WAL mode** | 2.5× faster per autocommit INSERT (1.9 vs 4.8 ms) and rejected anyway: committed rows live in `-wal`, so a copy-based reset needs a checkpoint and sidecar deletion, and a stale `-wal` left beside a replaced file **replayed the pre-reset data** (130 rows instead of 30). The journal default is what makes "reset = copy a file" true. |
| **One shared file with a `learner_id` column** | The same reason ADR 0002 rejected row scoping: `SELECT * FROM orders` is an ordinary first move, and `DELETE FROM customers` is one keystroke. A file, like a database, cannot be wrong about who may see it. |
| **In-memory SQLite (`:memory:`) per request** | Cheapest of all, and it throws the product away: a learner's `INSERT` would not survive their own next statement, and "reset" would mean nothing. ADR 0003's fake-write-mode row already rejected the same lie. |
| **Put the store behind `envdb`'s interface first** | The plan (step 5) said to extend `envdb`, and the store instead landed in `httpapi.SQLiteEnvironments`; the decision above does not depend on the package, but the seam does. Recorded as step 5's remainder rather than pretended away (below). |

## Consequences

- **The reset becomes affordable enough to be a first-class action.** ~2 ms means the UI can
  reset on a failed statement, not just on a learner's click, without a rate limit being the
  thing that makes it safe.
- **Nothing needs to reap sidecars.** With `delete` the environment is exactly one file; a
  reset overwrites it and a drop unlinks it. There is no `-wal`/`-shm` pair to leak.
- **Costs move from connection slots to files.** PostgreSQL bounds environments by
  `max_connections`; SQLite bounds them by disk. At 45 KiB seeded (~65 KiB after a few
  hundred writes) a thousand accounts is ~45 MiB, so the cap should be a policy (files per
  account, bytes, idle TTL) rather than a resource limit — and unlike the PostgreSQL manager,
  the SQLite store has no cap, no idle reaper and no boot-time sweep today.
- **Grading still assumes a dirty database**, for the same reason ADR 0003 records: an
  exercise expecting 80 rows can be satisfied by changing the table. The reset is the
  remedy, and it is now cheap enough to be the remedy for one exercise rather than one
  session.
- **The dialect keeps its own classroom.** A lesson can now teach `sqlite_master`, `PRAGMA`,
  `rowid` and affinity *and* let the learner write to them, which is the M6 course track's
  premise; the anonymous path stays read-only so a visitor cannot damage the shared fixture.
- **A `page_count` of 11 is not 7.8 MiB.** The size difference is the visible consequence of
  ADR 0004's choice: two courses can now be taught about the same four tables on one machine
  with a 190× difference in per-learner cost.

## What step 5 still owes (the gap between this decision and the code)

The decision above is accepted; the code implements decisions 1, 2 (from schema + seed), 7 and
8, and **not yet** 3, 4, 5 and 6's *reset* clause, because no reset exists:

1. **Build the sealed template file** once per data root and make "create" a copy of it, so
   create and reset are the same ~2 ms operation instead of a 13–16 ms re-seed.
2. **A reset route**: close the account's executor, replace `<account>.db` by
   copy-to-`.reset`-then-rename, delete any `-wal`/`-shm` beside the replaced file, and return
   the duration the way `POST /environment/reset` already does for PostgreSQL.
3. **Decide the package boundary** the plan asked for: either move the store behind
   `envdb`'s shape, or record in the plan that the SQLite dialect's store lives beside the
   HTTP surface. Today `/environment` and `/environment/reset` route to the PostgreSQL
   manager only, and `?dataset=sqlite` is refused by name (measured above).
4. **A cap and a reap**, mirroring `MaxEnvironments` + the idle reaper, so a file per account
   is a bounded promise. `pg_database_size` has `os.Stat`; `ListEnvironmentDatabases` has
   `os.ReadDir`.
5. **One write connection per account** (or an explicit serialised writer), since
   `sqlitePoolConns = 4` is the measured `SQLITE_BUSY` case.

None of these changes the isolation property, which is already measured above and covered by
`TestSQLiteAccountsCannotSeeEachOther` in `backend/internal/httpapi/dialect_test.go`.

## What is not proven here

- **No multi-tenant resource accounting.** One environment at a time was measured, plus a
  burst of copies. The growth curve for thousands of files, and the interaction with the
  host's page cache, is not measured.
- **No torn read was ever observed** in 59 in-place reads; the atomicity argument rests on
  the deterministic phase-11 result (an open pool keeps the old inode) and on POSIX rename
  semantics, not on a reproduced tear.
- **The 2 s busy timeout is a chosen bound, not a measured optimum.** It was probed at exactly
  300 ms and 2600 ms; how a learner's 5-minute essay of `INSERT`s behaves under it is not
  measured.
- **No filesystem other than this host's local disk.** NFS and container overlay filesystems
  have weaker rename and lock semantics; the decision assumes a local disk, the same
  assumption the embedded PostgreSQL makes.
- **The `SQLITE_BUSY`-on-read-only-open result is one host, one driver version.** The
  `journal_mode(DELETE)` refusal is recorded as it surfaced (`SQLITE_BUSY`), not as
  `engine/sqlite.go` predicts (`3850`); whether the difference is the live writer, the
  driver, or the platform was not isolated.
- **Nothing about the template's *introduction* is measured.** The probe shows the shipped
  seed already produces bytes identical to a template build (one `sha256`, `cmp` = 0
  differences), so the change is a latency change; but a reset that *replaces an existing*
  environment, and the interaction with a file created by the pre-template code, are not
  measured end to end.
- **The learner's own SQL can still hold a lock past the timeout** — a `BEGIN` left open in
  one tab is measured to make a second writer fail after 2.00 s. The UX for that condition
  is step 7's, not this ADR's.
