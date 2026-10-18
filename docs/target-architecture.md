# Target architecture

Date: 2026-10-17. This is the shape the codebase is being moved toward, the
reasoning behind each move, and the reasoning behind what is deliberately kept.
It follows the review in `docs/quality-review.md`; the evidence lives in
`docs/reviews/2026-10-17/`.

The reading test: a new engineer opens any file and can say what it owns. If
two files describe the same fact, one of them is wrong eventually. If a package
needs a paragraph to justify itself, it is probably two packages or none.

## The backend tells five stories

The package layout is already close to right. The boldness is inside the
packages: deleting second descriptions, splitting files that carry several
responsibilities, and finishing the seams that stop halfway.

| Story | Packages | State |
| --- | --- | --- |
| SQL runs and answers are graded | `engine`, `eval`, `dataset` | Right size. The hand-rolled SQLite driver is earned; leave it. |
| State is stored | `store`, `envdb`, `localdb` | One SQL implementation is written twice. Collapse. |
| Courses are generated | `ai` (`generation`) | One provider is described three times. Collapse. |
| The API is served | `httpapi`, `apidocs`, `webui` | Routing knowledge in four places, five error envelopes. Collapse. |
| The gateway boots | `cmd/api` | One 328-line `run()`. Split into phases. |

## One truth per fact

The collapses, with what they delete:

| Fact | Described how many times today | Target |
| --- | --- | --- |
| The store's SQL dialect | Two hand-written copies (916 lines) beside a shared dialect shim | One set of statements (~508 lines), engine chosen by the shim |
| The metadata store | Postgres, SQLite, and a 609-line memory map | One SQL implementation plus SQLite `:memory:` |
| The provider configuration | `Provider`, `RawConfig`, `StreamFn`, `resolveCourseModel` | One capability on the existing provider seam |
| The pre-pi course wire | `ProviderRequest.Messages/Tools/ToolChoice`, `FinishReason`, unreachable transport branches | Deleted |
| The outline prompt contract | Two string literals that agree by luck | One symbol |
| `OutlineChapter` to `CourseChapter` | Three copies of the same field list | One constructor |
| Stage to error code | Two switches | One helper |
| The error envelope | Five shapes, recovered by a type switch in metrics | One typed envelope, code passed explicitly |
| Routing knowledge | Route table, `operations` map, `isStatementRoute`, a URL-prefix switch | The route table carries operation and body vocabulary |

## One responsibility per file

The splits, all behaviour-preserving:

- `course_agent.go` (1,513 lines) becomes `course_agent_outline.go` (outline
  types, validation, schema), `course_build.go` (the build state and its
  derivations), `course_prompt.go` (the prompts and schemas), leaving the run,
  tools and storage in `course_agent.go`.
- `pipeline.go` (1,019) separates stage tracking from request rendering.
- `cmd/api`'s `run()` becomes `openDatabases`, `wireAI`, `serveAndDrain`.
- `httpapi` gains `requireAI`, `requireCourseDepot`, `submitJob`, `list`; the
  seventeen copy-pasted refusals become one each.
- `envdb` carries the fixture loader on the kind, deleting `loaderFor`, and
  shares one in-flight helper between `ensure` and `reset`.
- The web store (2,038 lines) becomes memoised stores (progress, catalog, ui,
  session, build) behind the existing `useStore()` facade. One
  `commitProgress` replaces fourteen three-line rituals.
- `BuildingScreen`'s 537-line effect becomes a `useGenerationRun()` reducer
  plus a pure `poll()`; the log becomes structured JSX and the hand-rolled
  `esc` and `dangerouslySetInnerHTML` path dies.
- `api/ai.ts`'s 26 hand-rolled `asX` decoders become `effect/Schema` structs,
  one decoding vocabulary with `contract.ts`.

## What gets finished

These are undercooked, not wrong:

- The error envelope becomes one type on both sides of the wire.
- The OpenAPI document moves to `apidocs/spec.json` behind `go:embed`, and a
  test checks the route table against it, so drift is a failing test rather
  than a review finding.
- The web's generation state machine mirrors the job view's own shape. The
  backend already models the run (stages, chapters planned and verified,
  current chapter, terminal status); the screen should hold the same states as
  one union, not as branch guards.
- Screens stop re-deriving what `lib/model.ts` owns.

## What is deliberately kept

A mercenary knows what not to touch:

- `engine/sqlite_raw.go` and `sqlite.go`: the custom driver is the honest
  workaround for a dependency that coerces declared types. The review verified
  this against the dependency's own source.
- `eval`, `metrics`, the audit scripts, `contracts/` and the fixture suites.
  These are the testing story, and they are better than a unit-test stack.
- The pi agent loop, with the tools as the deterministic policy. One
  deterministic turn policy (`FinishTurn`), one transport repair
  (`recoverToolCallsFromText`), no hand-rolled loop.
- The `Executor` seam, the `Depot` interface, the `config.applyKey` switch,
  the jobs registry, the transcript recorder.
- The dependency set. Across four independent reviews, no third-party package
  earned its place. The stdlib, `pgx`, `pi`, `effect`, and `go:embed` cover
  this system; the slop found is duplicated facts, and libraries do not delete
  a second description of anything.

## Phases

Every step is independently shippable, and every phase ends green on
`make verify` plus `make test`.

1. **Shred.** Pure deletions: dead symbols, dead fields, dead parsers, the
   hand-rolled `strconv`, unlinking the outline literals. No behaviour change.
2. **One truth per fact.** The store collapse, the provider collapse, the
   dead wire, the HTTP envelope and route table.
3. **Splits.** The reader-facing file and function decompositions, backend and
   web.
4. **Finish.** One decoder vocabulary, the embedded spec with its drift test,
   the run reducer, and the optional movers (`login_throttle` to `auth`, the
   `ai` package renamed to `generation` if the churn still looks worth it).
