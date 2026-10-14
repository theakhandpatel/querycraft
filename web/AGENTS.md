# web/ — frontend

React 18 + TypeScript + Vite. Strict `tsconfig.json`.

## Layout

- `src/types.ts` — shared domain types (course content, progress, the
  ExecutionService contract). Import from here instead of re-declaring shapes.
- `src/api/` — the single execution seam:
  - `contract.ts` — Effect `Schema` decoders mirroring `contracts/*.schema.json`.
  - `pglite.ts` — the default engine: real PostgreSQL compiled to WebAssembly
    (`@electric-sql/pglite`) in the page, seeded from the Go dataset SQL.
  - `dataset.ts` — imports `backend/internal/dataset/*.sql` with Vite `?raw`
    (one schema/seed shared with the Go evaluator).
  - `content.ts` — splits `src/data/content/*.md` into sections and seeds them
    into the `content_docs` table on first run (the bundle is only the seeder).
  - `compare.ts` — result-contract comparison, mirroring the Go evaluator
    (`backend/internal/eval/compare.go`).
  - `engine.ts` — pure in-browser SQL engine behind the mock adapter.
  - `execution.ts` — `ExecutionService` (Effect `Context.Service`) with
    `pgliteLayer` / `mockLayer` / `httpLayer`, plus `createExecutionApi()` which
    exposes the promise-shaped `ExecutionApi` the screens use.
  - `index.ts` — `createApi()` picks pglite (default), mock or http from
    `VITE_API_MODE`; in a production build with no explicit mode it probes its
    own origin `/health` and uses `http` when the gateway answers.
- `src/data/curriculum.ts` — courses, chapters and the prose blocks used when a
  module has no authored markdown.
- `src/data/content/*.md` — authored chapter markdown, one file per module id.
  Three courses are complete: `sql-analytics` (`m1`–`m8`), `joins` (`j1`–`j3`),
  `subqueries` (`q1`–`q4`). See `docs/content-authoring.md`.
- `src/state/store.tsx` — the single provider; `useStore()` returns the
  exported `StoreValue`.
- `src/lib/markdown.ts` — markdown to sanitised HTML (DOMPurify); fenced
  ```mermaid blocks render to SVG in-browser via mermaid `securityLevel: 'strict'`.
- `src/lib/` — pure derivations (`model.ts`, `progress.ts`, `pipeline.ts`).
- `src/components/` — screens and UI. Presentational; no direct `fetch`.

`VITE_API_MODE` accepts `pglite` (default), `mock`, `http`; `http` also reads
`VITE_API_BASE` (default `http://localhost:8080`) for the Go gateway. Docker is
not required for the default flow — PGlite runs in the browser tab.

## Effect

Effect v4. When unsure of an API, read the vendored source at
`../.worktrees/vendor-effect/vendor/effect/packages/effect/src/` (or
`node_modules/effect/src/`). Do not use v3 spellings.

## Checks

```sh
npm run typecheck   # tsc --noEmit, strict
npm run build
```
