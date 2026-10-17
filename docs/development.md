# Development

How to work on the product. For how the parts fit together, see
[`architecture.md`](architecture.md).

## The repository

```
web/                            React app (src/api/ is the only network seam)
  src/api/execution/            ExecutionService: pglite and http layers
  src/api/pglite.ts             PostgreSQL in the page, in a Web Worker
  src/api/idb.ts                IndexedDB store for offline progress and events
  src/api/compare.ts            Result comparison, mirrors the Go evaluator
  src/data/content/*.md         Chapter markdown, the content_docs seeder
backend/                        Go module github.com/querycraft/sqlquery/backend
  cmd/api/                      Entrypoint and the cobra CLI
  internal/config/              Settings: defaults, querycraft.json, flags
  internal/httpapi/             JSON API, OpenAPI 3.1, Swagger UI
  internal/localdb/             Downloads and starts the embedded PostgreSQL
  internal/{engine,dataset,eval,store}/
contracts/                      JSON Schemas shared by both parts, plus fixtures/
docs/                           This documentation
scripts/                        The audit suites the build targets run
```

Both parts speak one seam, defined as JSON Schema in `contracts/`. Neither
imports the other.

## Working on the frontend

You need Node 20.19 or later and npm. The `.nvmrc` names the line the project
tests on.

```sh
cd web
npm install
npm run dev            # http://localhost:5173, PostgreSQL in the browser
npm run build          # production bundle in web/dist
npm run typecheck      # tsc --noEmit, strict
npm run lint           # oxlint, with the vendored anti-slop rules
npm run doctor         # React Doctor diagnostics for src/
```

Two adapters sit behind `ExecutionService`, selected by `VITE_API_MODE`:

- `pglite` (default). PostgreSQL compiled to WebAssembly runs in a Web Worker
  inside the learner's own writable database (ADR 0003). No server and no
  Docker.
- `http`. The Go gateway. Also set `VITE_API_BASE`, which defaults to
  `http://localhost:8080`.

```sh
VITE_API_MODE=http VITE_API_BASE=http://localhost:8080 npm run dev
```

A production build detects the gateway by itself: it probes `/health` on its
own origin with a 1.5 second timeout and switches to the `http` adapter when
the gateway answers. This is why the single binary works with no
configuration. The header shows the mode: `postgres 18 · in-browser` for
pglite, and `gateway · live` for http.

`npm run lint` is the only linter. It runs oxlint with the anti-slop rules
vendored at `web/tools/oxlint/anti-slop/`, which reject TypeScript that
compiles but carries no evidence: a chain of assertions such as
`as unknown as T`, an `unknown` in a function contract, and `typeof` where a
decoder belongs. `web/oxlint.config.ts` explains every severity it sets.

`npm run doctor` runs React Doctor over `web/src`. It reports accessibility,
render behaviour and bundle findings. It is a report, not a gate. Treat each
finding as a hypothesis and check it against the file. `progress.md` records
the verdicts for the run on 2026-10-16.

## Working on the gateway

You need Go 1.26 or later. If `go` is not on `PATH`, run
`export PATH=$PATH:/usr/local/go/bin`.

```sh
cd backend
go build ./...
go vet ./...
go test ./...          # green without a database
go run ./cmd/api       # 127.0.0.1:8080, override with the addr key or --addr
```

The gateway's settings resolve through three layers: shipped defaults, then
`querycraft.json`, then command-line flags. The keys, the example file, and
the CLI are documented in [`../backend/README.md`](../backend/README.md),
section "Configuration".

The dataset schema and seed have one source of truth:
`backend/internal/dataset/schema.sql` and `seed.sql`. The Go evaluator uses
them, and the frontend imports the same files with Vite `?raw` in
`web/src/api/dataset.ts`, so there are no copies.

To write a lesson, see [`content-authoring.md`](content-authoring.md).

## Verify end to end

```sh
make verify            # build, typecheck, lint, audits, go test, smoke test on real PostgreSQL
make verify-mock       # the mode with no database, including auth
make lint              # oxlint over web/src
make doctor            # React Doctor report; informational, not a gate
make audit-courses     # run every authored solution through a running gateway
make audit-progress    # the progress model: measured time only
make audit-export      # the export document
make audit-fixtures    # the shared result fixtures, through the JavaScript evaluator
make audit-adversarial # the ten adversarial cases from design 13.3
make audit-load        # concurrent POST /query, checked against the gateway metrics
make audit-parity      # the same SQL on PGlite and on the gateway
make audit-metrics     # /metrics and /diagnostics
```

`make test` runs the type check, the linter, every audit above except the load
test and parity, and `go test ./...`. The load test and the parity audit start
their own gateway, so they are separate targets.

`make verify` starts the freshly built binary on `127.0.0.1:18080` against a
throwaway cluster in `backend/.data/smoke`. It wipes and recreates that
cluster, picks a free port, loads the dataset, and then checks these facts:

- `/health` reports the real server version, `PostgreSQL 18.3`.
- `POST /query` returns `{"status":"succeeded", …"rows":[[80]]…}` for
  `select count(*) as n from orders`.
- An anonymous `GET /progress/{course}` is refused with `401 AUTH_REQUIRED`.
- The seeded administrator signs in with an empty password.
- A learner can sign up, and their progress round-trips under their own token.
- A token from a signed-out session is refused.
- `GET /` serves the app.

To check a running gateway by hand:

```sh
curl -s localhost:8080/health                      # {"engine":"postgresql","version":"PostgreSQL 18.3",…}
curl -s localhost:8080/capabilities                # limits and isolation
curl -s localhost:8080/datasets/commerce/tables | head -c 200
curl -s -X POST localhost:8080/query \
  -H 'content-type: application/json' \
  -d '{"sql":"select count(*) as n from orders"}'
psql "$DATABASE_URL" -c 'select count(*) from orders'   # your own server
```

## A note on vendored code

The Effect source is vendored with `git subtree` under `.worktrees/`, which is
not part of the published repository: agents read real Effect code instead of
guessing at an API. `AGENTS.md` explains when to use it. This project uses
Effect v4, whose API differs from v3 (`Context.Service`, `Effect.catch`,
`Schema.decodeUnknownEffect`).