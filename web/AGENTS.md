# web/ (frontend)

React 18 + TypeScript + Vite. Strict `tsconfig.json`.

## Layout

- `src/types.ts`: shared domain types (course content, progress, the
  ExecutionService contract). Import from here instead of re-declaring shapes.
- `src/api/`: the single execution seam:
  - `contract.ts`: Effect `Schema` decoders mirroring `contracts/*.schema.json`.
  - `pglite.ts`: the default engine: real PostgreSQL compiled to WebAssembly
    (`@electric-sql/pglite`) in the page, seeded from the Go dataset SQL.
  - `dataset.ts`: imports `backend/internal/dataset/*.sql` with Vite `?raw`
    (one schema/seed shared with the Go evaluator).
  - `content.ts`: splits `src/data/content/*.md` into sections and seeds them
    into the `content_docs` table when a PostgreSQL workspace boots. Lessons
    read the bundled sections directly. The table is for SQL access, not the
    reading path.
  - `compare.ts`: result-contract comparison, mirroring the Go evaluator
    (`backend/internal/eval/compare.go`).
  - `execution/`: `ExecutionService` (Effect `Context.Service`) with
    `pgliteLayer` / `httpLayer`, plus `createExecutionApi()` which
    exposes the promise-shaped `ExecutionApi` the screens use.
  - `index.ts`: `createApi()` picks pglite (default) or http from
    `VITE_API_MODE`; in a production build with no explicit mode it probes its
    own origin `/health` and uses `http` when the gateway answers.
- `src/components/editor/`: the CodeMirror SQL editor. `SqlEditor.tsx` is the
  React boundary and imports `sqlEditorRuntime.ts` dynamically; `lib/sqlSchema.ts`
  caches the `getTables` completion source.
- `src/data/courses/`: one module per built-in course. `curriculum.ts` assembles
  the catalog and keeps the public `COURSES` and `MODULES` names.
- `src/data/content/*.md`: authored chapter markdown, one file per module id.
  **Eight courses, 52 chapters** ship complete: `sql-analytics` (`m1`–`m9`),
  `joins` (`j1`–`j3`), `subqueries` (`q1`–`q4`), `plans` (`p1`–`p8`),
  `foundations` (`f1`–`f7`), `sqlite-essentials` (`s1`–`s7`),
  `sqlite-in-production` (`r1`–`r7`), `postgres-to-sqlite` (`x1`–`x7`). The list
  of record is `web/src/data/courses/index.ts` (`COURSES`), re-exported by
  `web/src/data/curriculum.ts`. See `docs/content-authoring.md`.
- `src/state/store.tsx`: the single provider; `useStore()` returns the
  exported `StoreValue`.
- `src/lib/markdown.ts`: markdown to sanitised HTML (DOMPurify); fenced
  ```mermaid blocks render to SVG in-browser via mermaid `securityLevel: 'strict'`.
- `src/lib/`: pure derivations (`model.ts`, `progress.ts`, `pipeline.ts`).
- `src/components/`: screens and UI. Presentational; no direct `fetch`.
- `oxlint.config.ts`: the rule severities for `npm run lint`, each with its
  reason. Every rule that is off says why.
- `tools/oxlint/anti-slop/`: the vendored anti-slop rules. `PROVENANCE.md`
  records the upstream revision.

`VITE_API_MODE` accepts `pglite` (default) or `http`; `http` also reads
`VITE_API_BASE` (default `http://localhost:8080`) for the Go gateway. Docker is
not required for the default flow, because PGlite runs in the browser tab.
`VITE_APP_MODE` is `simple` for the public browser sample and unset or `full` for
the gateway build.

## Effect

Effect v4. When unsure of an API, read the vendored source at
`../.worktrees/vendor-effect/vendor/effect/packages/effect/src/` (or
`node_modules/effect/src/`). Do not use v3 spellings.

## Checks

```sh
npm run typecheck   # tsrs-wasm -p ., strict
npm run typecheck:tsc # tsc --noEmit fallback
npm run lint        # oxlint + the vendored anti-slop rules
npm run build
npm run doctor      # React Doctor diagnostics; informational, not a gate
```

`oxlint.config.ts` decides each rule's severity and says why. The rules
themselves are vendored at `tools/oxlint/anti-slop/`, so a rule can be read and
changed here instead of tracked upstream.
