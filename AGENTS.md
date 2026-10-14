# AGENTS.md — Querycraft

Guidance for coding agents working in this repository.

## What this repo is

Querycraft: a learn-SQL-by-writing-it app. One repository, two tracks that share
one contract seam (`contracts/`, JSON Schema):

| Track | Path | Stack |
| --- | --- | --- |
| Frontend | `web/` | React 18 + TypeScript + Vite, Effect for the execution layer; default execution is real PostgreSQL (WASM) via PGlite, in a Web Worker, with learner data in IndexedDB |
| Backend | `backend/` | Go 1.25 + real PostgreSQL (embedded local server by default, or any `QC_DATABASE_URL`), JSON HTTP gateway (Swagger/OpenAPI docs) |
| Vendored Effect source | `.worktrees/vendor-effect/vendor/effect/` | git subtree of Effect-TS/effect |

The tracks have disjoint file sets — work on one does not disturb the other.
`make build` compiles the SPA, embeds it into the gateway, and emits a single
binary at `backend/bin/querycraft` that serves both on one port. That binary
also boots its own PostgreSQL, so the built app needs no Docker and no config.

## Commands

```sh
# Frontend
cd web && npm install
npm run dev            # http://localhost:5173 (Postgres/WASM in the browser; no server, no Docker)
VITE_API_MODE=http VITE_API_BASE=http://localhost:8080 npm run dev   # against the Go gateway
npm run build          # production bundle
npm run typecheck      # tsc --noEmit (strict)

# Backend (Go is not on PATH in this environment)
export PATH=$PATH:/usr/local/go/bin
cd backend
go build ./... && go vet ./... && go test ./...
go run ./cmd/api       # :8080, degrades gracefully with no database

# Single binary + full verification (from the repo root)
make build             # web + embed + gateway -> backend/bin/querycraft
make verify            # build + typecheck + go test + smoke test (real PostgreSQL, isolated port)
make verify-mock       # smoke test the degraded mode (QC_NO_LOCAL_DB=1)
make serve             # run the binary (SPA + API on :8080)
make audit-fixtures    # evaluator fixture suite (JSON only; Go half runs in go test)
make db-reset          # delete the dev/smoke PostgreSQL clusters (re-seeds on next boot)
make help              # list every target
```

`VITE_API_MODE` picks the frontend adapter: `pglite` (default — real PostgreSQL
compiled to WebAssembly runs in a Web Worker), `mock` (canned results), or `http`
(the Go gateway, also uses `VITE_API_BASE`, default `http://localhost:8080`).
In a **production build** with `VITE_API_MODE` unset, `web/src/api/index.ts`
probes its own origin's `/health` and switches to `http` when the gateway
answers, so the single binary works with no configuration. Docker is not
required for any of these flows.

## The database

The gateway owns a real PostgreSQL and starts one itself when no DSN is given:
binaries cached in `backend/.data/pg/binaries`, cluster at `backend/.data/pg/data`,
listening on `QC_PG_PORT` (default `54329`). Env: `QC_DATABASE_URL` (use an
external server), `QC_NO_LOCAL_DB=1` (never start one), `QC_PG_DATA` /
`QC_PG_RUNTIME` / `QC_PG_BINARIES` / `QC_PG_PORT` / `QC_PG_USER` /
`QC_PG_PASSWORD` / `QC_PG_DATABASE`. Swapping to a container or managed server
is a one-variable change. Details: `README.md`, `backend/README.md`.

Chapter reading content is authored as markdown in `web/src/data/content/*.md`,
seeded once into the app database table `content_docs`, and read back from the
database by the lesson page — the markdown bundle is only the seeder. The
dataset schema and seed have one source of truth shared with the Go evaluator:
`backend/internal/dataset/schema.sql` and `seed.sql`, imported into the web app
with Vite `?raw` (`web/src/api/dataset.ts`).

The result-contract rules have one test suite for both languages:
`contracts/fixtures/*.json`, each `{ contract, actual_result, expected_outcome }`.
`backend/internal/eval/fixtures_test.go` and `scripts/fixtures-audit.mjs` read
the same files, so neither language keeps its own expectations. A new grading
rule is added as fixtures first (`make audit-fixtures`, `make test`).

## Working with Effect (frontend)

The frontend execution layer is written with [Effect](https://effect.website) v4
(typed errors, `Schema` decoding, `Layer`-provided services, retries/timeouts).

Before guessing at Effect APIs, **read the source**. It is available two ways:

- Vendored: `.worktrees/vendor-effect/vendor/effect/packages/effect/src/`
  (the `git subtree` of the Effect repo).
- Package source: `web/node_modules/effect/src/` and its types in
  `web/node_modules/effect/dist/*.d.ts`.

Prefer `rg <Symbol> .worktrees/vendor-effect/vendor/effect/packages/effect/src`
over recalling docs. Effect v4 differs from v3: `Context.Service` (not `Tag`),
`Effect.catch` (not `catchAll`), `Schema.Literals`/`Schema.Union([...])`,
`Schema.decodeUnknownEffect`.

The vendored copy is updated with:

```sh
cd .worktrees/vendor-effect && git subtree pull --prefix=vendor/effect https://github.com/Effect-TS/effect.git main --squash
```

## Conventions

- TypeScript is `strict`; keep `npm run typecheck` clean.
- All frontend data access goes through the `ExecutionService` seam
  (`web/src/api/`) — never call `fetch` from a screen, and never fabricate
  query results in the UI.
- The Go gateway must stay honest: with no database it reports
  `ENVIRONMENT_UNAVAILABLE` rather than inventing rows.
- Preserve the existing visual design and copy; this is a port, not a redesign.
- Do not commit unless the user asks.
