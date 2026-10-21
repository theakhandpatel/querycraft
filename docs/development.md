# Development

This guide covers the repository layout, the local commands, and the checks that
gate a change. For how the parts fit together, read
[`architecture.md`](architecture.md).

## The repository

```text
web/                            React app
  src/api/                      ExecutionService: the only network seam
  src/api/execution/            pglite, sqlite, and http adapters
  src/api/pglite.ts             PostgreSQL in the page, in a Web Worker
  src/api/sqlite.ts             SQLite in the page, in a Web Worker
  src/api/idb.ts                IndexedDB store for offline progress and events
  src/api/compare.ts            Result comparison, mirrors the Go evaluator
  src/components/editor/        CodeMirror editor and its lazy runtime
  src/data/courses/             One module per built-in course
  src/data/content/*.md         Chapter markdown, the content_docs seeder
web/landing/                    Landing page, served at / and copied into the build
backend/                        Go module github.com/querycraft/sqlquery/backend
  cmd/api/                      Entrypoint and the cobra CLI
  internal/config/              Settings: defaults, querycraft.json, flags
  internal/httpapi/             JSON API, OpenAPI 3.1, Swagger UI
  internal/localdb/             Downloads and starts the embedded PostgreSQL
  internal/engine/              The one execution path
  internal/dataset/             Schema and seed for both dialects
contracts/                      JSON Schemas shared by both parts, plus fixtures/
docs/                           This documentation
scripts/                        The audit suites the build targets run
```

Both parts speak one seam, defined as JSON Schema in `contracts/`. Neither
imports the other.

## Work on the frontend

You need Node 22 or later and npm. The `.nvmrc` names the line the project
tests on.

```sh
cd web
npm install
npm run dev            # http://localhost:5173 with PostgreSQL in the browser
npm run build          # full build bundle in web/dist
npm run build:simple   # browser-only bundle with a relative base
npm run typecheck      # tsrs-wasm -p ., strict
npm run typecheck:tsc  # tsc --noEmit fallback

npm run lint           # oxlint with the vendored anti-slop rules
npm run doctor         # React Doctor diagnostics for src/
```

Two adapters sit behind `ExecutionService`. `VITE_API_MODE` selects one:

- `pglite` is the default. PostgreSQL compiled to WebAssembly runs in a Web
  Worker inside the learner's own writable database. It needs no server and no
  Docker.
- `http` uses the Go gateway. Set `VITE_API_BASE` as well. It defaults to
  `http://localhost:8080`.

```sh
VITE_API_MODE=http VITE_API_BASE=http://localhost:8080 npm run dev
```

A production build with no mode set probes `/health` on its own origin and
switches to the `http` adapter when the gateway answers. That is why the single
binary works with no configuration. The header shows the active engine:
`sqlite · in-browser` or `postgres · in-browser` for the page engine, and
`gateway · live` for the gateway.

`VITE_APP_MODE` selects the product surface. Leave it unset or set `full` for
the gateway build with accounts, AI, stored courses, runs, and admin. Set
`simple` for the public browser sample. The static build sets it together with
`VITE_API_MODE=pglite`.

`npm run typecheck` runs `tsrs`, the Rust port of the TypeScript 7 checker, through `@tsrs/wasm`. The WASM package runs on the same Ubuntu image and in CI; the native `tsrs` binary needs a newer glibc. `npm run typecheck:tsc` keeps the TypeScript compiler as a fallback.

`npm run lint` runs oxlint with the anti-slop rules vendored at
`web/tools/oxlint/anti-slop/`. The rules reject TypeScript that compiles but
carries no evidence: a chain of assertions such as `as unknown as T`, an
`unknown` in a function contract, and `typeof` where a decoder belongs.
`web/oxlint.config.ts` explains every severity it sets.

`npm run doctor` runs React Doctor over `web/src`. It reports accessibility,
render behavior, and bundle findings. It is a report, not a gate. Treat each
finding as a hypothesis and check it against the file. Record the verdict beside
the change.

## Work on the gateway

You need Go 1.26 or later. If `go` is not on `PATH`, run
`export PATH=$PATH:/usr/local/go/bin`.

```sh
cd backend
go build ./...
go vet ./...
go test ./...          # passes without a database
go run ./cmd/api       # 127.0.0.1:8080, override with the addr key or --addr
# The built binary serves the landing at / and the app at /app/. make web copies
# web/landing into web/dist/landing before the embed step.
```

Settings resolve in three layers: shipped defaults, then the config file, then
command-line flags. [`../backend/README.md`](../backend/README.md) lists the
keys, the example file, and the CLI.

The dataset schema and seed have one source of truth in
`backend/internal/dataset/schema.sql` and `seed.sql`. The Go evaluator uses
them, and the frontend imports the same files with Vite `?raw` in
`web/src/api/dataset.ts`. The SQLite mirror, `schema.sqlite.sql` and
`seed.sqlite.sql`, lives beside them.

To write a lesson, read [`content-authoring.md`](content-authoring.md).

## Verify a change

```sh
make verify            # build, test, then smoke-test on real PostgreSQL
make verify-mock       # the mode with no database, including auth
make lint              # oxlint over web/src
make doctor            # React Doctor report, informational
make audit-courses     # every authored solution through a running gateway
make audit-progress    # the progress model, measured time only
make audit-export      # the export document
make audit-import      # import validation and the merge policy
make audit-fixtures    # the shared result fixtures through the JavaScript evaluator
make audit-adversarial # the ten adversarial cases and the fuzz phase
make audit-load        # concurrent POST /query, checked against the gateway metrics
make audit-parity      # the same SQL on the browser adapters and the gateway
make audit-metrics     # /metrics and /diagnostics
```

`make test` runs the type check, the linter, the cgo check, every audit above
except the load test, and `go test ./...`. It fans the checks out with a sub-make,
up to `TEST_JOBS` at once (default 8). Every audit binds free ports and stages its
own files, so the checks share no state.

The Go suite boots a PostgreSQL cluster for every test that needs one. The first
run also builds a golden cluster at `backend/.data/pg/golden`: initdb and the
dataset run once, and each test copies the result and starts a server on it. A
new PostgreSQL binary or a changed dataset rebuilds it. `make db-reset` removes
it. The load test and the parity audit start their own gateway, so they are
separate targets.

`make verify` starts the freshly built binary on a free loopback port against a
throwaway cluster in `backend/.data/smoke`. It wipes and recreates that cluster,
picks a free port, loads the dataset, and checks these facts:

- `/health` reports the real server version.
- `POST /query` returns `{"status":"succeeded", ...}` for
  `select count(*) as n from orders`.
- An anonymous `GET /progress/{course}` is refused with `401 AUTH_REQUIRED`.
- The seeded administrator signs in with an empty password.
- A learner can sign up, and their progress round-trips under their own token.
- A token from a signed-out session is refused.
- `GET /` serves the app.

To check a running gateway by hand:

```sh
curl -s localhost:8080/health
curl -s localhost:8080/capabilities
curl -s localhost:8080/datasets/commerce/tables | head -c 200
curl -s -X POST localhost:8080/query \
  -H 'content-type: application/json' \
  -d '{"sql":"select count(*) as n from orders"}'
psql "$DATABASE_URL" -c 'select count(*) from orders'
```

## Vendored Effect source

The Effect source is vendored with `git subtree` under `.worktrees/vendor-effect`.
It is not part of the published repository. Agents read the real Effect code
instead of guessing at an API. `AGENTS.md` explains when to use it.

The frontend uses Effect v4. Its API differs from v3: `Context.Service` replaces
`Tag`, `Effect.catch` replaces `catchAll`, and `Schema.decodeUnknownEffect`
replaces the v3 decoder.
