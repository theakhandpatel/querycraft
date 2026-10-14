<img src="assets/icon.svg" width="56" height="56" alt="">

# Querycraft

Learn SQL by writing it. A course where every exercise runs on a real
PostgreSQL engine and is graded by comparing query *results* against an explicit
contract, not by matching SQL text.

This repo holds the whole app: a React frontend and a Go gateway that share one
seam (`contracts/`, JSON Schema). `make build` compiles the SPA, embeds it into
the gateway, and emits **one binary** that serves both.

The app has local accounts. A fresh install seeds an administrator at
`admin@querycraft.local` with an **empty password**, so the first sign-in needs
no setup; set `QC_ADMIN_PASSWORD` before exposing the port. Public signups get
the `learner` role. Progress and events are scoped to the signed-in account.

| Track | Path | Stack | Purpose |
| --- | --- | --- | --- |
| Frontend | `web/` | React 18 + TypeScript + Vite, Effect | The whole learner UI (Linear-style shell, courses, lesson SQL cells, tables drawer, workbook, progress, challenge lab, authoring). |
| Backend | `backend/` | Go + PostgreSQL | Execution gateway (Adapter B): runs learner SQL with isolation/limits, grades results, embeds the SPA, serves OpenAPI/Swagger docs. |
| Vendored Effect source | `.worktrees/vendor-effect/vendor/effect/` | git subtree | Read the real Effect source instead of guessing. |

Both sides talk through a single `ExecutionService` seam defined as JSON Schema
in `contracts/`. On the frontend that seam is an Effect service
(`web/src/api/execution.ts`): typed errors, `Schema`-decoded results, and
`Layer`-provided adapters. The React app defaults to a real PostgreSQL engine
compiled to WebAssembly ([PGlite](https://pglite.dev)) running in a **Web
Worker**, so it runs with no server and no Docker and a runaway statement can
still be stopped by terminating the worker; one env var switches to the canned
`mock` adapter or to the HTTP gateway instead.

The two tracks have disjoint file sets, so they can be worked on in parallel.

## Layout

```
web/                            React app (frontend track)
  src/api/index.ts              Picks the adapter from VITE_API_MODE (default pglite)
  src/api/execution.ts          ExecutionService (pglite + mock + http Layers)
  src/api/pglite.ts             Real PostgreSQL (WASM) in the page: runs SQL, seeds the DB
  src/api/pglite.worker.ts      The worker that owns the engine + dataset (killable)
  src/api/idb.ts                IndexedDB store for offline progress and events
  src/api/compare.ts            Result-contract comparison mirroring the Go evaluator
  src/api/content.ts            Seeds src/data/content/*.md into `content_docs`
  src/api/dataset.ts            Imports backend/internal/dataset/*.sql with Vite ?raw
  src/api/engine.ts             Canned engine behind the mock adapter
  src/lib/exportBundle.ts       The versioned JSON export document (design 8.3)
  src/data/content/*.md         Authored chapter markdown (the content_docs seeder)
backend/                        Go module github.com/querycraft/sqlquery/backend
  cmd/api/main.go               Entrypoint
  internal/httpapi/             JSON API, OpenAPI 3.1, Swagger UI
  internal/webui/               //go:embed of the built SPA
  internal/localdb/             Downloads + starts embedded PostgreSQL (no Docker needed)
  internal/{engine,dataset,eval,store}/
docker-compose.yml              Optional PostgreSQL 16 for tests/CI only — the binary needs no Docker
contracts/                      Language-neutral JSON Schemas shared by both tracks
contracts/fixtures/             The shared evaluator fixture suite (JS and Go both run it)
docs/technical-design.md        The spec (v0.3, five milestones)
docs/content-authoring.md       How to author course markdown (DB-seeded)
docs/database-layout.md         Cluster / database / schema nesting and the learner role
docs/export-format.md           The versioned JSON export document
docs/adr/                       Decision records (see 0001, Go vs Bun)
Makefile                        build / verify / verify-mock / audit-* / serve / run / dev / docs / db-reset / clean
backend/.data/                  Embedded PostgreSQL cluster (gitignored, created on first boot)
```

## Quick start (single binary)

```sh
export PATH=$PATH:/usr/local/go/bin     # if go is not on PATH
make build        # web + embed + gateway -> backend/bin/querycraft
make serve        # SPA + API on http://localhost:8080 (boots a real PostgreSQL, no Docker)
make verify       # build + tests + smoke test (recommended before a commit)
```

The first `make serve` initialises the embedded cluster in `backend/.data/pg`
(~10 s); later boots are instant. Point it elsewhere with `QC_DATABASE_URL`.

**Verified from nothing on a clean machine** (M3 exit gate). Starting the built
binary with an empty data directory *and* an empty binary cache — no cluster, no
downloaded server, no Docker, no config — downloads PostgreSQL 18.3 once
(measured: 60 MiB, cached in `QC_PG_BINARIES`), runs `initdb`, applies the
commerce schema and seed, and serves the app. Measured 2026-10-14 on the
documented flow:

```sh
root=$(mktemp -d)
QC_ADDR=127.0.0.1:18099 QC_PG_PORT=54611 \
QC_PG_DATA=$root/pg/data QC_PG_RUNTIME=$root/pg/runtime QC_PG_BINARIES=$root/pg/binaries \
  ./backend/bin/querycraft &
# -> /health   {"engine":"postgresql","status":"ok","version":"PostgreSQL 18.3"}
# -> /query    {"status":"succeeded",…"rows":[[80]],…}   (select count(*) as n from orders)
# -> /         200 text/html   (the SPA)
# -> /docs     200             (Swagger UI)
```

`make verify` exercises the same cold-boot path on every run, on a throwaway
cluster in `backend/.data/smoke`, and asserts the responses above.

## Frontend

Node 20+, npm. No pnpm/yarn required.

```sh
cd web
npm install
npm run dev            # http://localhost:5173, Postgres (WASM) in the browser — no server, no Docker
```

`VITE_API_MODE` selects the adapter:

- `pglite` (default) — real PostgreSQL compiled to WebAssembly
  (`@electric-sql/pglite`) runs in the browser tab, in the learner's own writable
  database (ADR 0003); answers are graded by comparing result sets, and the tables
  drawer can restore the sealed dataset at any time. No server, no Docker.
- `mock` — the original canned results, kept for comparison.
- `http` — the Go gateway; also set `VITE_API_BASE` (default
  `http://localhost:8080`).

In a **production build** the SPA auto-detects the gateway: when `VITE_API_MODE`
is unset, `web/src/api/index.ts` probes its own origin's `/health` (1.5 s
timeout, expecting `{"status":"ok","engine":"postgresql"}`) and uses the `http`
adapter if it answers, otherwise it falls back to `pglite`. That is why the
single binary serves a fully working app with no configuration — its header
reads `gateway · live`.

Run against the Go gateway:

```sh
VITE_API_MODE=http VITE_API_BASE=http://localhost:8080 npm run dev
```

Build / preview / smoke-check:

```sh
npm run build          # production bundle in web/dist
npm run preview        # serve the build at http://localhost:4173
npm run typecheck      # tsc --noEmit, strict
```

The header shows `postgres 18 · in-browser` in pglite mode, `gateway · live` in
http mode, and `pg 16.3 · simulated` in mock mode.

### Chapter content

Reading content is authored as markdown in `web/src/data/content/*.md` — one file
per module id. **Three courses are complete:** `sql-analytics` (`m1`–`m8`),
`joins` (`j1`–`j3`) and `subqueries` (`q1`–`q4`). Each chapter is seeded once
into the app database table `content_docs` on first run, and the lesson page
reads it back from the database — the markdown bundle is only the seeder.
Course/module metadata and the fallback prose live in
`web/src/data/curriculum.ts`.

Markdown HTML is sanitised with DOMPurify; fenced ` ```mermaid ` blocks render to
SVG in-browser via mermaid with `securityLevel: 'strict'`, and the diagrams wait
for `document.fonts.ready` before layout so labels never clip.

Authoring workflow: `docs/content-authoring.md`.

### The dataset

The commerce schema and seed ship once, as `backend/internal/dataset/schema.sql`
and `seed.sql`, shared with the Go evaluator. The frontend imports the very same
files with Vite `?raw` (`web/src/api/dataset.ts`) to seed its in-browser Postgres
— one source of truth, no copies.

### Workbook

M2 adds the browser-side workbook. It is local-first, so it behaves the same on
either adapter — `pglite` in the tab or the Go gateway:

- **Bookmarks** on a lesson: create and remove, kept across reloads, and part of
  your export.
- **Saved queries** ("keep this query"): kept from any SQL cell, listed in the
  Workbook screen, runnable again in place.
- **Search**: one field across lesson prose, exercise prompts and your own notes,
  bookmarks and saved queries, reachable with `Cmd/Ctrl+K`.
- **Per-exercise progress history**, including a stale-version view when the
  dataset revision has moved on from the one an attempt was graded against.
- **Export and validated import**: a versioned, credential-free JSON copy of your
  progress (`docs/export-format.md`), and an import that is checked against
  `contracts/export.schema.json` and merged with what is already here rather than
  overwriting it.

### Accessibility

The app is usable keyboard-only end to end: a skip link jumps to the reading
column, the SQL editor and the rest of the controls have visible focus, the
results grid is a labelled table, and the Tables drawer is a real focus-trapped
dialog that hands focus back on close. What is verified, how, and what is *not*
verified (no automated a11y runner is installed, so a scripted keyboard-only
lesson walkthrough is the evidence) is recorded in `docs/accessibility.md`.

## Backend gateway

The gateway runs the API *and* the built SPA, and owns a real PostgreSQL. Go
1.25+. If `go` is not on `PATH`: `export PATH=$PATH:/usr/local/go/bin`.

```sh
cd backend
go build ./...
go vet ./...
go test ./...          # all green with no database
go run ./cmd/api       # listens on :8080 (override with QC_ADDR)
```

### Database modes — no Docker required

With no configuration the gateway **downloads prebuilt PostgreSQL binaries once**
(cached in `backend/.data/pg/binaries`), initialises a cluster in
`backend/.data/pg/data`, starts it on `:54329`, and applies the shared commerce
schema + seed (`backend/internal/dataset/*.sql`, the same files the browser
engine uses). Everything above this talks the ordinary PostgreSQL wire protocol,
so pointing the gateway at any other PostgreSQL is a one-variable change.

| Env var | Default | Meaning |
| --- | --- | --- |
| `QC_ADDR` | `:8080` | HTTP listen address |
| `QC_DATABASE_URL` | *(unset)* | Use this PostgreSQL instead of the embedded one |
| `QC_NO_LOCAL_DB` | `0` | `1` = never start the embedded server |
| `QC_ADMIN_DATABASE_URL` | falls back to `QC_DATABASE_URL` | Metadata store (accounts, sessions, progress, events) |
| `QC_ADMIN_EMAIL` | `admin@querycraft.local` | Address of the seeded default administrator |
| `QC_ADMIN_PASSWORD` | *(empty)* | Password for that admin. Empty = sign in with a blank password. Set it before exposing the port |
| `QC_PG_PORT` | `54329` | Embedded server port |
| `QC_PG_DATA` / `QC_PG_RUNTIME` / `QC_PG_BINARIES` | `backend/.data/pg/...` | Cluster dirs |
| `QC_PG_USER` / `QC_PG_PASSWORD` / `QC_PG_DATABASE` | `querycraft` | Credentials |

Swap the embedded server for a container or a managed database:

```sh
QC_DATABASE_URL='postgres://user:pass@host:5432/db?sslmode=disable' \
  ./backend/bin/querycraft
```

`docker compose up -d db` remains available for tests/CI that want an external
server; the default local flow never needs it.

With `QC_NO_LOCAL_DB=1` and no DSN, the gateway still serves every endpoint, but
SQL execution returns the stable code `ENVIRONMENT_UNAVAILABLE` instead of faked
rows and progress falls back to memory — the honest degraded mode, and what
`make verify-mock` checks. `make db-reset` deletes the cluster so the next boot
re-applies the dataset.

Endpoints and env vars are documented in `backend/README.md`.

### API docs (Swagger)

The gateway serves its own docs:

```sh
curl -s localhost:8080/openapi.json | head -c 200   # OpenAPI 3.1 document
# open http://localhost:8080/docs                   # Swagger UI
```

## Vendored Effect source

Per Effect's guidance for coding agents, the Effect source is vendored with
`git subtree` so agents read real code instead of guessing at APIs:

- `.worktrees/vendor-effect/vendor/effect/` — the vendored repository.
- The npm package also ships source at `web/node_modules/effect/src/`.

Effect v4 is in use; its API differs from v3 (`Context.Service`, `Effect.catch`,
`Schema.decodeUnknownEffect`). See `AGENTS.md` and `web/AGENTS.md`.

## Verify end to end

```sh
make verify        # build + typecheck + audits + go test + smoke test (real PostgreSQL)
make verify-mock   # the degraded mode (QC_NO_LOCAL_DB=1), incl. auth
make audit-courses # run every authored exercise solution through a running gateway
make audit-progress # the progress model: measured time only, v4 -> v5 upgrade
make audit-export   # the JSON export document: versioned, complete, credential-free
make audit-fixtures # the language-neutral result-contract fixtures, via the JS evaluator
make audit-adversarial # the ten design 13.3 adversarial cases, against a freshly booted gateway
make audit-load       # concurrent POST /query load, cross-checked against the gateway's own /metrics
make audit-parity     # the same lesson SQL on PGlite (Adapter A) and the Go gateway (Adapter B)
make audit-metrics    # /metrics and /diagnostics (hermetic) — audit-metrics-live for a running gateway
```

`make test` runs the type-check, every audit suite above except the load test and
parity, and `go test ./...`. The load test and parity audits boot their own
gateway, so they are separate targets (`make audit-load`, `make audit-parity`).

`make verify` boots the freshly built binary on `:18080` against a throwaway
PostgreSQL cluster: it wipes and cold-`initdb`s `backend/.data/smoke`, auto-picks
a free port so it never collides with a dev gateway, applies the dataset, then
asserts

- `/health` reports the real server version (`PostgreSQL 18.3`), not the build version;
- `POST /query` returns `{"status":"succeeded", …"rows":[[80]]…}` for
  `select count(*) as n from orders`;
- anonymous `GET /progress/{course}` is refused with `401 AUTH_REQUIRED`;
- the seeded administrator signs in with an empty password, a learner signs up,
  their progress round-trips under their own token, and a logged-out token is
  refused;
- `GET /` serves the SPA.

`make verify-mock` additionally proves the honest degraded mode: with no database
`POST /query` answers `ENVIRONMENT_UNAVAILABLE` rather than inventing rows, and
the in-memory store still enforces auth and seeds the default admin.

Manual equivalent against a running gateway:

```sh
curl -s localhost:8080/health                      # {"engine":"postgresql","version":"PostgreSQL 18.3",…}
curl -s localhost:8080/capabilities                # limits + isolation
curl -s localhost:8080/datasets/commerce/tables | head -c 200
curl -s -X POST localhost:8080/query \
  -H 'content-type: application/json' \
  -d '{"sql":"select count(*) as n from orders"}'
psql "$QC_DATABASE_URL" -c 'select count(*) from orders'   # or: PGPASSWORD=querycraft psql -h 127.0.0.1 -p 54329 -U querycraft -d querycraft
# open http://localhost:8080/docs for Swagger UI
```

## Status

`web/` is the M1 course experience: home dashboard, course/chapter navigation,
inline SQL cells with run + grade + feedback, tables drawer, workbook, progress,
challenge lab and authoring modals, all faithful to the prototype in
`docs/technical-design.md`.

Three courses are authored end to end (`sql-analytics`, `joins`, `subqueries`),
each chapter stored as markdown in the database with rendered Mermaid diagrams,
and every exercise graded against a result contract.

It has local accounts: a fresh install seeds `admin@querycraft.local` with an
empty password (`QC_ADMIN_PASSWORD` to override), public signups become learners,
and progress and events are scoped to the signed-in account — the owner always
comes from the session, never the request body. The workbook can export a
versioned, credential-free JSON copy of your data (`docs/export-format.md`).

The Go gateway is the M3 Adapter B surface. It implements the full `contracts/`
endpoint set and its evaluator; it also embeds and serves the built SPA
(`backend/internal/webui`), so one binary answers both the JSON API and the app.
It owns a real PostgreSQL (embedded by default, `QC_DATABASE_URL` to point at any
other server) and applies the shared commerce dataset on first boot.

Learner SQL is writable, but only ever inside a workspace the learner owns
(ADR `0003`). An anonymous `POST /query` runs on the shared `dataset` in a
read-only transaction and is refused any write; a **signed-in** learner is routed
to their own database, copied from the sealed template on first use, where
`INSERT`, `UPDATE`, `DELETE`, DDL and transactions all work. `GET /environment`
reports that workspace and `POST /environment/reset` drops the learner's changes
and copies the sealed template again — the same "restore the original data"
affordance the browser adapter exposes through `resetEnvironment()`. The shared
dataset itself is never writable in place, so one learner's writes cannot reach
another's, or the seed.

The frontend's default adapter is the real PGlite engine
(`web/src/api/pglite.ts`): statements run against the same schema and seed the
gateway uses, in the learner's own writable browser database, inside a Web Worker
so the host can terminate a statement that overruns the 5 s deadline and reseed
the dataset.
`engineCapabilities()` reports whether that deadline is real (`placement:
'worker'`) or whether a page that blocks workers forced the main-thread fallback
(`hard_timeout: false`), and the Appearance screen prints what it reports.
Answers are graded by comparing result sets with the same result-contract rules —
`web/src/api/compare.ts` mirrors `backend/internal/eval/compare.go`, and both run
the shared suite in `contracts/fixtures/`. Offline progress and events persist in
IndexedDB (`web/src/api/idb.ts`); with the gateway they live in PostgreSQL under
the signed-in account. `web/src/api/engine.ts` is the canned engine behind the
`mock` adapter.

Contract schemas: `contracts/README.md`. Spec: `docs/technical-design.md`.
