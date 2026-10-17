# Querycraft

Guidance for coding agents working in this repository.

## What this repo is

Querycraft: a learn-SQL-by-writing-it app. One repository, two tracks that share
one contract seam (`contracts/`, JSON Schema):

| Track | Path | Stack |
| --- | --- | --- |
| Frontend | `web/` | React 18 + TypeScript + Vite, Effect for the execution layer; default execution is real PostgreSQL (WASM) via PGlite, in a Web Worker, with learner data in IndexedDB |
| Backend | `backend/` | Go 1.26 + real PostgreSQL (embedded local server by default, or any `database_url` in `querycraft.json`), JSON HTTP gateway (Swagger/OpenAPI docs) |
| Vendored Effect source | `.worktrees/vendor-effect/vendor/effect/` | git subtree of Effect-TS/effect |

The tracks have disjoint file sets. Work on one does not disturb the other.
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
npm run lint           # oxlint + the vendored anti-slop rules
npm run doctor         # React Doctor diagnostics for src/ (informational; exits 1 when it finds issues)

# Backend (Go is not on PATH in this environment)
export PATH=$PATH:/usr/local/go/bin
cd backend
go build ./... && go vet ./... && go test ./...
go run ./cmd/api       # :8080, degrades gracefully with no database

# Single binary + full verification (from the repo root)
make build             # web + embed + gateway -> backend/bin/querycraft
make verify            # build + typecheck + lint + go test + smoke test (real PostgreSQL, isolated port)
make verify-mock       # smoke test the degraded mode (the no_local_db key)
make serve             # run the binary (SPA + API on :8080)
make lint              # oxlint over web/src
make doctor            # React Doctor report; informational, not a gate
make audit-fixtures    # evaluator fixture suite (JSON only; Go half runs in go test)
make db-reset          # delete the dev/smoke PostgreSQL clusters (re-seeds on next boot)
make help              # list every target
```

The tooling needs Node 20.19 or newer. `.nvmrc` names the line the project tests
on, and `web/package.json` declares the floor in `engines.node`. React Doctor
refuses to run below 20.19, so a Node upgrade is the first step on a new machine.

`VITE_API_MODE` picks the frontend adapter: `pglite` (default: real PostgreSQL
compiled to WebAssembly runs in a Web Worker) or `http` (the Go gateway, also
uses `VITE_API_BASE`, default `http://localhost:8080`).
In a **production build** with `VITE_API_MODE` unset, `web/src/api/index.ts`
probes its own origin's `/health` and switches to `http` when the gateway
answers, so the single binary works with no configuration. Docker is not
required for any of these flows.

## The database

The gateway owns a real PostgreSQL and starts one itself when no DSN is given:
binaries cached in `backend/.data/pg/binaries`, cluster at `backend/.data/pg/data`,
listening on `pg_port` (default `54329`). Config: `database_url` (use an
external server), `no_local_db` (never start one), `pg_data` /
`pg_runtime` / `pg_binaries` / `pg_port` / `pg_user` /
`pg_password` / `pg_database`, all keys in `querycraft.json`. Swapping to a container or managed server
is a one-key change. Details: `README.md`, `backend/README.md`.

Chapter reading content is authored as markdown in `web/src/data/content/*.md`,
seeded once into the app database table `content_docs`, and read back from the
database by the lesson page. The markdown bundle is only the seeder. The
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
- Keep `npm run lint` free of errors. The vendored anti-slop rules reject
  low-evidence TypeScript: a chain of type assertions that discards the
  compiler's evidence, an `unknown` in a function contract, and `typeof` where a
  decoder belongs. `web/oxlint.config.ts` says which rules are errors, which are
  warnings, and why the rest are off.
- All frontend data access goes through the `ExecutionService` seam
  (`web/src/api/`). Never call `fetch` from a screen, and never fabricate
  query results in the UI.
- The Go gateway must stay honest: with no database it reports
  `ENVIRONMENT_UNAVAILABLE` rather than inventing rows.
- Preserve the existing visual design and copy; this is a port, not a redesign.
- Do not commit unless the user asks.

## Frontend tooling

Two third-party diagnostics run against `web/src`. Neither one gates the build,
and both are pinned exactly, because a scanner that upgrades itself changes what
it reports.

| Tool | Command | What it is for |
| --- | --- | --- |
| oxlint | `npm run lint` (`make lint`) | Lint, with the anti-slop rules vendored at `web/tools/oxlint/anti-slop/`. Errors gate `make test`. |
| React Doctor | `npm run doctor` (`make doctor`) | React-specific diagnostics: accessibility, render behaviour, bundle size. Informational. |

The anti-slop rules are vendored rather than installed, per the upstream
project's own instruction. `web/tools/oxlint/anti-slop/PROVENANCE.md` records the
upstream revision. Read them before you change a severity.

React Doctor's findings are hypotheses, not verdicts. Check each one against the
file before you act on it. `progress.md` records the verdicts for the run on
2026-10-16, including the four that were false positives or deliberate.

## Writing

Prose in this repository follows three published standards, applied together.
They cover the documentation, the commit messages, the pull-request descriptions,
the code comments and the app copy.

- The Diátaxis structure and the plain-sentence rules of the `technical-writing`
  skill.
- The Vercel writing guidelines, `vercel-labs/writing-guidelines`.
- The AI-tell catalogue of the `unslop` skill.

The skills live in `.agents/skills` when installed; the rules below are the
operative subset and stand on their own.

The rules that decide most edits:

- Keep a sentence under 25 words. Put one idea in each sentence.
- Use the active voice and the present tense. Append "by monkeys" to the
  sentence; if it still parses, it is passive, so rewrite it.
- Address the reader as `you`. Use the imperative for a step.
- Use the short, everyday word: use, help, do, not utilize, facilitate, perform.
- Cut every word that does no work. "In order to" is "to".
- Name the real symbol, file, flag or command. The codebase is the word list.
- Do not use an em dash. Use a comma, a colon, or a new sentence.
- Do not start a list item with a bold label and a dash.
- Do not use AI vocabulary: additionally, crucial, delve, leverage, robust,
  seamless, showcase, underscore, vibrant, and the rest of the `unslop` list.
- Do not say "is" the long way. "Serves as", "stands as" and "boasts" are "is".
- Do not write "not just X, but Y". State the point.
- Do not force ideas into groups of three, and do not cycle synonyms for one
  thing. Pick one term and repeat it.
- Do not open a section with a slogan, and do not restate its heading.
- State what a thing does. Then point to the file that proves it.
- Prefer a measured number, or a test, over an adjective.
- Use a table only when the reader compares values across rows.
- No emoji. No title case in headings. No rhetorical questions.
- Say what is unfinished in the same document, in its own section.

Run `make` targets rather than quoting their output from memory. Report a check
that failed. Do not describe a check you did not run.
