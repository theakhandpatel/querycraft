# ADR 0001: Keep Go for the backend; Bun is an optional frontend accelerator

- **Status:** accepted
- **Date:** 2026-10-14
- **Deciders:** project owner + agent session
- **Question:** should the project move to Bun, or keep Go for the backend?

## Decision

Keep **Go** as the backend runtime. Treat **Bun** as an *optional, additive*
frontend toolchain (package manager, test runner, TS script runner): **npm stays
the default** so `make build` / `make verify` stay reproducible.

## Why the two languages are not actually competing

Bun and Go occupy different layers, so this is not either/or:

| Layer | Who owns it | Bun can replace it? |
| --- | --- | --- |
| Learner UI (React 18 + TS + Vite + Effect) | `web/`, Node/npm today | **Yes**: install/test/scripts |
| SPA bundling | Vite (and TanStack Start, in a worktree) | No, the bundler is already JS-native |
| HTTP gateway + SQL execution + grading | `backend/`, Go | **No** |
| Single-binary distribution (`go:embed`) | `backend/`, Go | **No** |
| Embedded PostgreSQL supervision | `backend/internal/localdb` | **No** |
| OpenAPI/Swagger serving | `backend/internal/httpapi` | **No** |

## Reasons to keep Go on the backend

1. **One artifact.** `make build` compiles the SPA, embeds it, and emits a single
   ~20 MB binary that serves the API and the UI and boots PostgreSQL itself.
   Bun can produce a compiled binary too, but it would still need a PostgreSQL
   server *and* a rewrite of the hardened executor: a new risk surface for no
   functional gain.
2. **Untrusted input is the product.** The gateway runs learner-authored SQL.
   Go's `pgx` path already enforces read-only transactions, `statement_timeout`,
   `lock_timeout`, `temp_file_limit`, row/byte caps and a bounded pool
   (`backend/internal/engine`). This is the app's security boundary; it should
   not be re-implemented casually.
3. **`go:embed` + `pgx` cannot be substituted** by any JS runtime. There is a
   real PostgreSQL wire-protocol client in both ecosystems, but the binary
   distribution story is Go's alone without adding a second runtime.
4. **Reproducibility.** npm + the committed `package-lock.json` is the lowest
   common denominator. Making Bun the default would add a prerequisite and a
   second lockfile to keep honest for a marginal gain.

## Where Bun is genuinely attractive (and how we will use it)

- `bun install` vs `npm ci` wall-clock time.
- `bun test` for the small pure-TS units (`web/src/api/compare.ts`).
- Running TS directly (`bun run scripts/foo.ts`) for content linting and seeds
  without a `tsx`/build step.

Benched in the `chore/bun-toolchain` worktree. If it wins clearly, it is adopted
**only** for those additive tasks; `make` targets stay npm-based.

## Reconsider if

The frontend grows a server-rendered surface (e.g. TanStack Start deployed as a
single JS process), then Bun becomes the natural runtime *for that surface*.
The SQL execution/grading service should still stay Go for the isolation reasons
above, regardless.

## Consequences

- Docs mention two package managers (npm = default, bun = optional); the cost is
  documentation, the benefit is optional speed.
- `.worktrees/bun-toolchain` may be deleted without affecting `main`.
- Swapping the *database* stays a one-line change (`QC_DATABASE_URL`), which is
  the real portability win we cared about.
