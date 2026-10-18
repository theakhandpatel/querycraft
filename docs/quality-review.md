# Quality review

Date: 2026-10-17. Scope: the whole repository, reviewed with the two vendored
skills in `.agents/skills/`: `code-simplification` and
`thermo-nuclear-code-quality-review`. Four reviewers ran in parallel, one per
layer. Their full reports, with file:line evidence, measurements and worked
remedies, are the four files under `docs/reviews/2026-10-17/`. This document is
the consolidated verdict and the plan of attack.

The question the review was asked to test: is the plumbing over-abstracted,
is the HTTP layer a hand-rolled mess, and do third-party packages already
solve what is hand-rolled here?

## Verdict per layer

**HTTP layer** (`01_httpapi.md`). Not a hand-rolled mess, and the standard is
already the right one: Go 1.22 `ServeMux` method-plus-wildcard patterns and a
six-function middleware chain. The honest third-party answer is none: chi would
not delete a layer, huma and oapi-codegen would invert the `contracts/` source
of truth, and the validator packages cannot express rules the `auth` and `ai`
packages already own. The real problems are narrow: a metrics helper that
silently loses error codes for whole route families, routing knowledge declared
in four places, and one refusal copy-pasted seventeen times.

**Generation layer** (`02_ai_pipeline.md`). The pipeline (stages, evidence,
repair budget, transcript, scratch environment) earns its keep. The defect is a
provider configuration described three times. `Config` carries `Provider`,
`RawConfig` and `StreamFn`, synced by hand at three wiring sites, and the course
agent re-implements `piai.New` in `resolveCourseModel`. The pi adoption left two
seams where the recorded plan said to leave one. A byte-identical
`jsonSchemaToPi` sits in two files. The pre-pi wire surface is dead but still
shipped.

**Substrate** (`03_engine_store_config.md`). Half right. The interfaces are the
right size; the implementations behind them are duplicated. The store contains
the mechanism that makes the two SQL dialects collapse into one (`sqlStore` plus
`sqlDialect` in `metadata.go`), and the `Depot` methods right beside it ignore
it, writing the same 17 queries twice. `store.Memory` is a third implementation,
outside every parity harness, and it has already drifted. The one hand-rolled
driver expected to fail the review, `sqlite_raw.go`, passed on the evidence: the
pinned `modernc.org/sqlite` coerces DATE-declared TEXT columns to `time.Time`
with no opt-out, and the dialect contract needs the text.

**Frontend** (`04_web_frontend.md`). The over-abstraction hypothesis holds
here. `state/store.tsx` is 2,038 lines in one provider whose value is not
memoised, so one HTTP log line re-renders all 32 consumers. `BuildingScreen`
encodes the whole run lifecycle as one 537-line effect with four nested
closures and a module-level `Map` to survive StrictMode. The decoding seam has
two vocabularies: `effect/Schema` in two modules and 26 hand-rolled `asX`
parsers in `ai.ts`, which is where 8 of the 29 lint warnings live.

## The five big moves, ranked by how much they delete

1. **Fold the `Depot` onto `sqlStore`, delete `sqlite_depot.go`.** About 400
   lines of duplicated SQL vanish, and the drift class (timestamp format, NULL
   handling, a dead `approved_at` scan) is removed by construction. The parity
   harness already protects it.
2. **Replace `store.Memory` with a thin wrapper over
   `NewSQLite(ctx, ":memory:")`, delete `memory.go`.** About 600 lines, and
   degraded mode joins the parity harness that already proves the SQL backends
   agree. The one kept behaviour is `Kind() == "memory"`, which
   `reapOrphanedEnvironments` reads.
3. **One provider description.** Give `provider.Provider` one optional course
   capability (pi model, key resolver, stream), implement it on `FakeProvider`
   and `piai.PIAI`, then delete `AIRawConfig`, `resolveCourseModel`,
   `CourseStream`, `Config.RawConfig`, the `StreamFn` field, and the copied
   `jsonSchemaToPi`. Three descriptions collapse to one.
4. **Split `course_agent.go` (1,513 lines)** into `course_agent_outline.go`,
   `course_build.go` and `course_prompt.go`. Pure moves, no behaviour change.
5. **Decompose the web store and the run screen.** Memoised
   progress/ui/catalog stores behind the existing `useStore()` facade, one
   `commitProgress` replacing 14 three-line rituals, and a
   `useGenerationRun()` reducer replacing the 537-line effect.

## The safe deletions, shippable immediately

- `webui.fmtInt` to `strconv.Itoa` (22 lines).
- `SQLiteConfig.DSN`, `parseSQLiteDSN` and the dead connector branch (~55
  lines).
- The 7 dead `StoreValue` members on the frontend.
- `DialectOf`/`EngineLabelOf` identity wrappers.
- The fake provider's outline markers: make `courseAgentPrompt` write the
  constants instead of two string literals that agree by luck. Without this,
  editing one prompt sentence silently breaks the shipped default provider.
- Dead generation-layer surface: `sectionCount`, `CodeAIAvailable`,
  `GenerationRequest.Steps`, `Config.AdversarialFixtures` plus its branch.
- `internal/dialect`: the vestigial keep-alive already removed in this pass.

## What the reviewers told us to leave alone

- `engine/sqlite_raw.go`, the 906-line hand-rolled driver. Earned; the
  dependency's source proves the coercion with no opt-out.
- The `config.applyKey` switch. It is the file schema and keeps the per-key
  compile-time check.
- The `Executor` seam, `engine.Execute`, the eval rules, the metrics registry,
  the localdb bootstrap, the stall proxy. Right size, comment explains
  measurements.
- Third-party packages, across every layer: none. The stdlib plus
  `encoding/json`, `effect/Schema` where it is already used, and `go:embed` for
  the OpenAPI document. sqlc emits two packages for two engines and would not
  remove the store duplication the existing `sqlDialect` removes in 120
  parity-tested lines.

## The medium moves, each independent

- HTTP: one typed error envelope written with the code explicit, deleting
  `errorCode`'s five-case type switch; `operation` and the body vocabulary on
  the `route` struct, deleting `operations`, `operationOf` and
  `isStatementRoute`; one `requireAI` helper for the 17 copy-pasted refusals;
  `submitJob` and `list` helpers for the duplicated handler shapes.
- Generation: `tryChapter` for the place-gate-restore pattern;
  `OutlineChapter.chapter()` for the mapping copied three times; link the
  fake's two prompt literals (safe list); decide the home of
  `recoverToolCallsFromText` (transport repair living in the policy package).
- Substrate: carry the fixture loader on `envdb.envKind`, delete `loaderFor`;
  optional `run()` decomposition.
- Frontend: port `api/ai.ts` to `effect/Schema` (one vocabulary, and the 8 lint
  warnings go with the hand-rolled parsers); derive
  `exercisesWithReference`/`chapterBlocks` in `lib/model.ts` and delete the
  screen-local copies; optionally collapse the 50 mechanical wrappers in
  `createExecutionApi` with one typed `toPromiseApi`.
- Docs: move the 1,893-line OpenAPI string out of `apidocs.go` into
  `apidocs/spec.json` via `go:embed`. Stdlib only, and the document becomes
  diffable and lintable.

## Proposed order of attack

Every step is independent and lands with the full gate, `make verify`.

1. The safe deletions, as one small commit each.
2. The HTTP error envelope and the route struct fields. The wire document is
   unchanged; `contract_test.go` guards it.
3. The provider seam collapse, followed by deleting the dead pre-pi wire
   surface.
4. The store collapse: Depot onto `sqlStore`, then Memory onto SQLite.
5. The `course_agent.go` split, then the local factors (`tryChapter`, the
   chapter constructor).
6. The frontend: dead members, `commitProgress`, `lib/model.ts` derivations,
   then the store split and the `useGenerationRun` reducer, then the
   `effect/Schema` port.
7. The optional moves last: `go:embed` for the spec, the login throttle's move
   to `internal/auth`, `run()` decomposition, the course-state unify (the one
   move that touches the store/job contract).

Two steps change a data shape and want a short diff of their own (the provider
seam, the store collapse). Do not bundle them with the mechanical extrasctions.
