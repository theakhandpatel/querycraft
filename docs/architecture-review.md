# Architecture review

Date: 2026-10-16. Scope: `backend/`, `web/`, `contracts/` and `scripts/`. The five
decomposition slices sit uncommitted and are treated as landed.

## How this review was made

Five independent reviews ran at the highest reasoning each model offers. Every
finding below was re-read in the source before it entered this document. Six
reviewer claims did not survive that pass and are listed at the end.

The gates pass on this tree:

```sh
cd backend && go build ./... && go vet ./...
```

A scanner over 622 exported Go declarations found 22 with no production
reference: 16 that only tests call, and 6 that nothing calls.

## Correctness

### 1. The boot reaper can delete every learner database

`cmd/api/main.go:236` calls `m.ReapUnreferenced(ctx, st)`. `newStore`
(`main.go:387-397`) returns `store.NewMemory()` when the metadata database is
unreachable or stale. `Memory.UserIDs` (`store/memory.go:493`) lists only
in-memory users, in practice the seeded admin. The reaper treats that list as
the keep-set and drops every other `qc_` database (`envdb/manager.go:690-713`,
`dataset/template.go:270-289`).

So with a working learner PostgreSQL and a degraded metadata store, the gateway
deletes every learner's workspace at boot. The stale-schema path is documented
and recoverable; losing the workspaces is not.

Fix: skip the reap unless the store is persistent. `store.Store` already has
`Kind()` (`store/store.go:72`), so `if st.Kind() != "memory"` is enough.

Verify: give the reaper a store whose `UserIDs` returns one account, seed a
database for a second, and assert nothing is dropped.

### 2. Shutdown does not wait for a running generation

`ai.Jobs.Submit` runs each job on `context.WithoutCancel(ctx)` in a bare
goroutine (`ai/jobs.go:244-249`). `Jobs` has no `Close` or drain: its methods are
`Running`, `Submit`, `Job`, `DiscardCourse`, `run`, `complete` and `evictLocked`.
`cmd/api/main.go` then returns, and the deferred `m.Close()`, `sqliteEnvs.Close()`
and PostgreSQL stop run underneath the job.

The gateway promises that a client that goes away does not cancel work. The
process itself breaks that promise at restart.

Fix: add `Jobs.Drain(ctx)` and call it before the deferred closes.

Verify: submit an async course, shut down mid-run, and assert the job reaches a
terminal stored record.

### 3. Ensure can re-add an environment after Close

`ensure` checks `m.closed` under the first lock (`envdb/manager.go:347`), builds
outside the lock, then takes the lock again at `:399` and writes `m.envs[key]`
with no second check. A build in flight when `Close` runs re-adds a pool that
`Close` claimed to have released.

Fix: re-check `m.closed` at `:399`, close the executor, and return `errClosed()`.

Verify: start `Ensure` against a build that blocks, call `Close`, and assert
`len(m.envs) == 0`.

### 4. The manager lock is held across multi-second DDL

`reset` takes `m.mu` and holds it across `dataset.ResetEnvironment`
(`envdb/manager.go:445-470`), a DROP and CREATE measured at about 2.9 s in ADR
0002. `reapOnce` holds it across `DropDatabase` per environment, each bounded by
`dropTimeout = 60s` (`:748-762`). `ensure` already avoids this with an in-flight
marker (`:372-395`); `reset` and `reapOnce` do not.

One learner's reset stalls every other account's `Ensure`, `Reset` and `Facts`
for the duration.

Fix: reuse the in-flight marker pattern, and drop the lock around the DDL.

Verify: run a `Reset` beside an `Ensure` for another account and measure the
second call.

### 5. A plan reset overwrites the challenge reset time

`reset` keys the environment map as `kind.keyPrefix + digest(accountID)`
(`envdb/manager.go:451`) but writes `m.resetAt[digest(accountID)]` (`:470`), and
`Facts` reads the same unprefixed key (`:513`). A plan reset therefore rewrites
the challenge environment's reported `ResetAt`, and the plan environment's own
time is unreadable.

Fix: write `m.resetAt[key] = start`.

Verify: reset the challenge, reset the plan, then read `Facts` for the challenge
account and assert `ResetAt` is the first time.

## Test-only code in shipped packages

Go's linker drops an unreachable function, so this is not mostly about binary
bytes. It is about a shipped package advertising an API that only its own tests
hold, and about code that looks live and is not. The fix for each is to unexport
it, move it into a `_test.go` file, or delete it.

| Package | Symbol | Verdict | Action |
| --- | --- | --- | --- |
| `ai` | `ProvidedRunner` (`api.go:167`) | tests only | move to a shared `internal/aitest` |
| `ai` | `CodeAIAvailable` (`api.go:30`) | dead | delete |
| `dataset` | `ConnectAsLearner`, `LoadIntoTemplate`, `ServerConnectionFacts`, `EnvironmentDatabaseSize` (`template.go`) | tests only | unexport; the tests are in-package |
| `dataset` | `RenderSchemaSQL`, `RenderSeedSQL` | maintenance path | keep; `make dataset` drives them |
| `dialect` | `ErrorCodes`, `AffinityClasses`, `ByName` | tests only | unexport |
| `dialect` | `Names`, `Affinity`, `ClassifyError`, `Decode` | tests only | unexport (see the blind spot below) |
| `envdb` | `NewSQLiteEnvironments` (`sqlite.go:133`) | one test caller | delete; production uses `WithOptions` |
| `eval` | `CompareColumns` (`compare.go:72`) | tests only | unexport |
| `eval` | `ErrorResponse` (`eval.go:161`) | dead | delete |
| `metrics` | `Histogram` (`metrics.go:195`) | tests only | keep as an accessor or move to the test |
| `perf` | `Grade` (`grade.go:75`) | dead in production | see finding 8 |
| `perf` | `WarnRowCapReached`, `WarnSettingsNonDefault`, `WarnAnalyzeSkipped` (`plan.go:31-33`) | dead | delete |
| `schema` | `StageIndex` (`model.go:196`) | tests only | unexport |
| `pgtest` | `LearnerDSN` | test package | leave; `pgtest` is test support |

Two blind spots in the scan. A name match can hide a symbol when another package
declares a method of the same name, which hides `dialect.Names` and
`dialect.ClassifyError`. An interface method declaration reads as a use, which
hides `schema.Depot.Challenges`. That method, and its only caller
`ai.Service.Challenges` (`ai/api.go:362`), are dead: the list route uses
`ChallengesFor` (`httpapi/ai.go:307`).

`ai.ProvidedRunner` cannot move into `ai`'s own `_test.go`, because
`httpapi/jobs_test.go:159` also constructs it. Both suites need a small shared
test package, or each should define its own fake `Runner`.

### 6. Fixtures and options that only tests write

`ai.Config.AdversarialFixtures` (`ai/api.go:257`) is read at `pipeline.go:674`
and set by no production caller. `schema.GenerationRequest.Steps` is validated
(`ai/validate.go:60`) and copied (`store/memory.go:209`) but never rendered into
a prompt. `ai.HTTPProviderConfig.HTTPClient` has no reference at all.

`envdb.Options.TemplateName`, `PerfTemplateName` and `MaxConnsPerEnv` are
defaulted in `withDefaults` (`envdb/manager.go:92-112`) and set by no production
caller. `MaxEnvironments` and `IdleTTL` are the same, but they should become real
knobs (`QC_MAX_ENVIRONMENTS`, `QC_IDLE_TTL`) instead of being deleted.

### 7. The fake provider is the default

`aiProviderFromEnv` reads `QC_AI_PROVIDER` with the default `fake`
(`cmd/api/ai.go:45`), so a build with no key answers generation with canned
content. This looks deliberate: the hint at `:47` describes it. It is still worth
a decision, because "no key configured" and "a model produced this" are
different sentences and the default blur them. `FakeProvider.courseBundle`
(`fake.go:499`) has no production caller and can go either way.

## Contracts

### 8. The plan grader exists twice, and the Go copy is dead

`perf.Grade` (`perf/grade.go:75`) has three callers, all tests. The reachable
grader is `gradePlan` in `web/src/api/planContract.ts`. Two implementations of
one rule, one of them shipping.

### 9. `evaluation.schema.json` copies the query result

`contracts/evaluation.schema.json:9-39` restates the `qr` object instead of
`$ref`-ing `contracts/query-result.schema.json:7-37`. Every grading change must
edit both and keep them in step.

### 10. The error enum is stated five times and guarded once

The closed code set lives in `contracts/query-result.schema.json`,
`contracts/dialect.schema.json`, `contracts/plan.schema.json`, the Go `Code*`
constants (`engine/engine.go:33-45`) and the TypeScript union
(`web/src/types.ts:229-240`, widened away at `:264`). Only the JSON copies have a
drift test (`httpapi/contract_test.go:972`). `engine.CodePersistenceFailed` has
no reference anywhere.

### 11. The feedback codes and the plan contract are copied too

`eval/compare.go:28` holds `validFeedbackCodes` and
`web/src/api/compare.ts:210-234` holds the same set. `perf/grade.go:41` and
`web/src/api/planContract.ts` both describe `PerfContract`.

## The HTTP seam

### 12. Auth is opt-in, and the guard test cannot fail on a public route

`withAuth` passes an absent or unknown token through (`httpapi/auth.go:41-59`).
Each protected handler must remember `requireUser`. The route table
(`httpapi/server.go:182-237`) carries no auth marker, and
`TestDocumentedSecurityMatchesTheHandlers` pins the secured set with a
hand-written list (`openapi_test.go:23`).

The test also has a hole: the public loop `continue`s when a path is missing
from the document (`:75-78`), so a dropped public path passes.

Fix: declare each route's auth in one table, enforce it in one wrapper, and
derive both the OpenAPI check and the test from that table.

Verify: add a route with no auth entry and show the test fails.

### 13. The docs and the handlers disagree about the generators

`backend/README.md:95-97` lists the generators as anonymous. `runGeneration`
calls `requireUser` (`httpapi/ai.go:125`), and both `/courses/generate` and
`/challenges/generate` declare bearer security. The code and the spec agree; the
README is stale.

### 14. The metrics registry is owned by the wrong package

`httpapi.New` creates the registry (`server.go:130`) and `cmd/api/main.go:206`
pulls it back out with `api.Registry()` to hand it to `envdb`. The comment at
`server.go:171-173` still says the method exists so a test can read a counter,
which is no longer true.

Fix: build the registry in `cmd/api` and pass it in `Config`.

### 15. `requestErrorCode` invents codes

`httpapi/server.go:293-307` mints `AI_INVALID_REQUEST` and `INVALID_REQUEST`
outside the documented gateway code set. Either add them to the contract or stop
inventing them.

### 16. The route facts are stated four ways

The routes live in the mux, in `requestErrorCode`, in the operations map and in
`apidocs`. Only the document has a drift test, and that test is paper over a
hand-written list.

## The frontend

### 17. The decoders discard the evidence the Schema just built

`web/src/api/contract.ts:47,50,78,82` decode with Effect `Schema` and then assert
`as unknown as QueryResult`, `as unknown as Evaluation` and `as User`.
`web/src/api/ai.ts` validates about thirty shapes with hand-rolled `typeof`
guards and `as` casts, seven of them `as unknown as`. The repo's own anti-slop
rule targets this shape, and oxlint records it as a warning
(`web/oxlint.config.ts:56`).

Fix: derive the TypeScript types with `Schema.Type<typeof X>` and return the
decoded value. Give `ai.ts` real `Schema` decoders.

### 18. The bundle, measured

The claim that "every adapter ships, so a gateway session downloads PGlite" is
wrong. The engines are lazily imported: `await import('@electric-sql/pglite')`
and `.../worker` (`web/src/api/pglite.ts:283,312`) and
`await import('./sqliteEngine')` (`sqlite.ts:139`). A gateway session never
fetches the WASM.

The real numbers: the mock adapter is about 1 kB minified, so removing it is
noise. The `dist` directory is 24 MB, of which about 18 MB is PGlite and SQLite
WASM and data, and `make build` embeds all of it in the Go binary. That embed is
a deliberate fallback: a gateway with no PostgreSQL still serves a working page
through PGlite. Splitting the binary build from the static build would save the
18 MB, and would break that fallback. Treat it as a decision, not a bug.

One real item remains: the main chunk statically imports all three adapter
wrappers (`web/src/api/execution.ts:8-42`), and `createExecutionApi` branches at
runtime (`:1396-1401`), so none can be dropped. Moving each wrapper behind
`await import()` is cheap and makes the intent legible.

## The data layer

### 19. Metadata is written twice

`store/postgres.go` and `store/sqlite.go` each declare the same tables, with 34
matching method names and no shared statement. A column added to one compiles,
passes the per-dialect tests, and fails only on the other backend in production.
Either generate both from one description or drop one backend.

### 20. The dataset schema is restated in the store

`dataset/*.sql`, the web `TABLES` table and the store DDL describe the same
tables. The generated `schema.sql` and `seed.sql` already prove the mirror
pattern inside `dataset`; the store does not use it.

## What holds up

The import graph is sound after the five slices. The leaves `schema`, `dialect`,
`dataset`, `metrics` and `webui` import nothing internal. `store` imports only
`schema`. `engine` imports only `dialect`. `internal/pgtest` is not in
`go list -deps ./cmd/api`. No cycle exists.

The hand-written SQLite driver (`engine/sqlite_raw.go`) is justified, not NIH:
the stock `modernc.org/sqlite` driver coerces declared `DATE`/`DATETIME`/`TEXT`
columns to `time.Time`, which the dialect contract forbids. `database/sql` still
owns the pool. Keep it, and keep its frame tests.

The SQLite dialect profile has one source and a guarded mirror:
`contracts/dialects/sqlite.json` is byte-identical to the Go embed and a test
enforces that. This is the pattern the error enum should copy.

Owner scoping is consistent: routes resolve the owner from the token, never the
body, and answer 404 rather than 403 for another account's id. The no-database
path reports `ENVIRONMENT_UNAVAILABLE` instead of inventing rows. Job panics are
recovered per run. The login throttle keys on the socket address, not on a
caller-supplied header.

## Where the five reviews disagreed

| Claim | Verdict | Evidence |
| --- | --- | --- |
| `/challenges/generate` is ungated | false | both generators call `runGeneration`, which calls `requireUser` (`ai.go:115-125`) |
| the four `engine` capability interfaces exist for tests | false | production probes them (`execute.go:252`, `engine.go:100,119`, `metrics.go:348`); `PgxExecutor` and `SQLiteExecutor` both implement them |
| `Registry()` exists for tests | partly | the comment says so, but production uses it (`main.go:206`); the ownership is still wrong |
| a gateway session downloads PGlite | false | `pglite.ts:283,312` and `sqlite.ts:139` import lazily |
| `envdb.Options.TemplateName` and friends have no setter, so delete | partly | `withDefaults` supplies them (`manager.go:92-112`); keep, and wire `MaxEnvironments` and `IdleTTL` to env |
| `handlers.go:214 UnmarshalJSON` is dead | false | `encoding/json` calls it for `evaluateRequest` |

One more stale path: `TestAnswerEvaluateAgainstEvaluationContract` tolerates four
missing properties unless `QC_CONTRACT_STRICT` is set
(`httpapi/contract_test.go:722-762`). The response now conforms, so the tolerant
branch and the allowlist are dead. Make the test strict and delete them.

## The plan, with decisions taken

The owner answered the open questions on 2026-10-16.

1. Gate the boot reaper on a persistent store (finding 1). In progress.
2. Add `Jobs.Drain` and call it during shutdown (finding 2). In progress.
3. Re-check `m.closed` after the unlocked build (finding 3). In progress.
4. Release the manager lock around the DDL, in `reset` and `reapOnce`
   (finding 4). In progress.
5. Key `resetAt` by the full key (finding 5). In progress.
6. Invert auth to a route table and derive the OpenAPI test from it
   (finding 12). In progress.
7. Render both metadata DDLs from one description and add a parity test
   (finding 19). In progress.
8. Grade the plan in Go: return the verdict from `POST /explain` and delete
   `gradePlan` in `web/src/api/planContract.ts` (finding 8).
9. Make `QC_AI_PROVIDER` default to off, so no key means `AI_UNAVAILABLE`
   (finding 7). Then move `FakeProvider` behind a test-only path.
10. Sweep the test-only symbols (the table above), one commit per row.
11. Make `contracts/` the source: `$ref` the `qr` object (finding 9) and extend
    the drift test to the Go and TypeScript enums (finding 10).
12. Generate the error codes from the schema into Go and TypeScript
    (finding 10), and delete the copies. This is the opencode
    `httpapi-codegen` pattern.
13. Give `ai.ts` real decoders and drop the `as unknown as` bridges
    (finding 17).
14. Move each frontend adapter behind `await import()` (finding 18).

## Decisions taken

- Auth is closed by default. One route table declares each route's requirement.
- No AI key means `AI_UNAVAILABLE`. The fake provider is not the default.
- One grader, in Go. The TypeScript twin goes.
- Both metadata backends stay, rendered from one description.
- The 24 MB embed stays. The no-database fallback is worth the bytes.

## Still unfinished

The markdown em dash pass. The first run was interrupted, so 1041 em dashes
remain across the markdown.

## Borrow from opencode

A reviewer read `packages/httpapi-codegen/README.md` on the opencode `dev`
branch. It states one rule that fits findings 10 and 16: reflect one
authoritative `HttpApi` into a shared contract with `compile(Api)`, commit the
generated source, and let CI regenerate and fail when the worktree changes. The
repository also carries `packages/protocol/openapi.json` and a generator script.
Querycraft already proves the pattern inside `dataset` with `schema.sql`; the
route table and the error enum are the next two places to use it.
