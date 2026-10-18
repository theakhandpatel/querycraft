# apidocs — slop audit (wave 1)

## Verdict

The real slop in `backend/internal/apidocs` is a hand-authored OpenAPI string that
has quietly stopped describing what the gateway answers. Four component schemas
are unreachable from any path (`AIPatchProposal`, `ChallengeGenerateRequest`,
`CourseGenerateResponse`, `RunResponse`), and one of them is the schema the
`/runs/{id}` path should have referenced. Two `required` lists lost `bytes`, the
`ErrorEnvelope.code` enum omits codes the handlers emit, and the `/query` and
`/explain` 400 bodies are documented as `QueryResult`/`PlanBundle` while the
transport answers an `ErrorEnvelope` for an unreadable body. Most of the file is
earned: every route in `routes.go` is documented, `QueryResult` matches its
contract, and the AI job, outcome and course enums match the Go constants. The
code that decides verdicts here is the reference graph and the drift tests, not
line count. `openAPIOperations` in the test is a second route list that the
`httpapi` drift test already covers, so it is the clearest test-side deletion.

Method: I parsed the `Spec` const (lines 12-2059), walked `$ref`s from `paths`,
and compared each component against `routes.go`, the handler structs, and
`contracts/*.schema.json`. Commands: a Python `$ref` reachability pass, a Python
route-vs-path diff, and `rg` over `backend/`, `contracts/` and `docs/`.

## Delete

- `components.schemas.CourseGenerateResponse` — `backend/internal/apidocs/apidocs.go:1954-1962`.
  Why it exists: a legacy `POST /courses/generate` response (title, chapters,
  exercises, datasetVersion). Evidence nothing hits it: the reachability pass
  reports it unreachable from every path, and `rg -n CourseGenerateResponse`
  finds only this definition plus an unused test mirror
  (`backend/internal/httpapi/contract_test.go:599-608`, `courseGenerateSchema`,
  whose only match is its own definition). The sibling `CourseGenerateRequest`
  was already deleted on 2026-10-18. Remedy: delete the schema and the dead
  `courseGenerateSchema` var.

- `components.schemas.ChallengeGenerateRequest` — `backend/internal/apidocs/apidocs.go:1963-1966`.
  Why it exists: described as "Free-form challenge generation request; the body
  is accepted and discarded." Evidence nothing hits it: unreachable from every
  path; `POST /challenges/generate` now references `AIGenerationRequest`
  (`apidocs.go:703`). The description is stale because `runGeneration` decodes
  `schema.GenerationRequest` (`backend/internal/httpapi/ai.go:182`). Remedy:
  delete the schema.

- `components.schemas.AIPatchProposal` — `backend/internal/apidocs/apidocs.go:1867-1875`.
  Why it exists: a typed patch proposal. Evidence nothing hits it: unreachable
  from every path; `AIDefectVerdict.patch` (`:1882`) and `AIPatchResult.patch`
  (`:1895`) are loose `{type:[object,null]}` objects instead. `rg -n
  AIPatchProposal` over `backend contracts docs web` returns only the definition.
  Caveat: the Go types do use the shape (`ai.DefectVerdict.Patch`,
  `ai.PatchResult.Patch`, `backend/internal/ai/api.go:104,125`) and
  `contracts/ai.schema.json $defs.patchProposal` is the truth. Remedy: either
  delete this schema and leave the loose objects, or `$ref` it from the two
  `patch` properties. An owner picks one; two truths may not stay.

- `openAPIOperations` — `backend/internal/apidocs/apidocs_test.go:11-55` (loop at `:79-88`).
  Why it exists: a hand-kept list of method+path pairs the document test checks.
  Evidence it is redundant: `backend/internal/httpapi/openapi_test.go:16-60`
  iterates the live route table (`(&Server{}).routes()`) and asserts every route
  is documented, that the security block matches the route's auth, and that a
  secured route documents its 401. That is a strict superset. The list here also
  omits `GET /openapi.json` and `GET /docs`, so it cannot catch a dropped route
  anyway. Remedy: delete the list and the loop; keep the serving assertions in
  `TestOpenAPIDocument` (200 plus JSON content type).

## Collapse

- `/runs/{id}` 200 body: `RunTranscript` or `RunResponse` — `backend/internal/apidocs/apidocs.go:944` vs `:1596-1602`.
  The path points its 200 at `RunTranscript`, but the handler answers
  `{"ok": true, "run": ...}` (`backend/internal/httpapi/runs.go:110`), and
  `RunResponse` is exactly `{ok, run}`. The test reads the wrapper
  (`backend/internal/httpapi/runs_test.go:288-290`, `decoded["run"]`). So the
  orphan `RunResponse` is correct and the path is wrong. Remedy: point the 200
  at `RunResponse` (then `RunTranscript` stays referenced from it).

- `bytes` required twice, documented once — `backend/internal/apidocs/apidocs.go:1556` (`RunSummary.required`), `:1573` (`RunTranscript.required`).
  Both lists omit `bytes`, but the Go structs always emit it
  (`backend/internal/schema/transcript.go:72,87`, no `omitempty`) and
  `contracts/run.schema.json` lists `bytes` in `runSummary.required` and
  `runTranscript.required`. Remedy: add `bytes` to both required lists, or make
  the contract the single source and generate the spec from it.

- `ErrorEnvelope.code` enum vs emitted codes — `backend/internal/apidocs/apidocs.go:2049`.
  `rg -no 'errorEnvelope\(w, [^,]+, "[A-Z_]+"' backend/internal/httpapi` returns
  `ADMIN_REQUIRED`, `AI_INVALID_REQUEST`, `PREFERENCES_UNAVAILABLE`,
  `AUTH_*`, `NOT_FOUND`, `PERSISTENCE_FAILED`. The enum lacks the first three,
  and lacks the `AI_*` codes `writeAIError` writes (`backend/internal/ai/api.go:31-40`).
  The spec even names 403 `ADMIN_REQUIRED` in four descriptions
  (`:439,510,539,562`) while the enum omits it. The sibling review already
  flagged this as an owner decision (`docs/reviews/2026-10-18/wave1/httpapi.md:274`).
  Remedy: derive the enum from the code constants, or list the emitted set.

- 400 response shape, statement routes — `backend/internal/apidocs/apidocs.go:106` (`/query`), `:128` (`/explain`).
  The spec says the 400 body is a `QueryResult` (`/query`) or a `PlanBundle`
  (`/explain`). But an unreadable body never reaches those writers:
  `readJSON` calls `errorEnvelope` (`backend/internal/httpapi/server.go:294`)
  with the route's vocabulary (`:266-284`), so `/query` with an empty body
  answers `{ok:false,error:{code:"EMPTY_QUERY"}}`, an `ErrorEnvelope`. The test
  asserts that shape (`backend/internal/httpapi/server_test.go:611-614,625-632`).
  Remedy: document both 400 shapes, or document only the reachable one per case.
  Same split for `/courses/generate` and `/challenges/generate`
  (`:380-383`, `:714-717` document `AIOutcome`, but a malformed body yields
  `ErrorEnvelope` with `AI_INVALID_REQUEST`, `ai.go:183` plus `server.go:277-278`).

- Route identity written twice — `backend/internal/httpapi/routes.go:70-126` (`operation`) vs `backend/internal/apidocs/apidocs.go:34-1069` (`operationId`).
  The same routes carry two vocabularies: `generate_course` vs
  `postCourseGenerate` (`routes.go:106`, `apidocs.go:361`), `apply_patch` vs
  `decidePatch` (`routes.go:122`, `apidocs.go:852`), `reset_environment` vs
  `postEnvironmentReset`. Nothing links them: `rg -rn "operationId|operation_id"`
  outside `apidocs/` returns nothing, and `openapi_test.go` checks neither.
  Remedy: a drift test asserting each route's `operationId`, or delete
  `operationId` if no generator consumes the document.


## Move / split

- `Spec` string to `spec.json` behind `go:embed` — `backend/internal/apidocs/apidocs.go:12-2059` (2048 lines).
  This is the intended end state (`docs/target-architecture.md:70-72`,
  `docs/quality-review.md:122-123`). Current shape is a Go string literal plus
  `var Document = json.RawMessage(Spec)` (`:2062`). What is missing to get there:
  (1) the file split with `//go:embed spec.json`; (2) a drift test that goes
  beyond path+security, namely reachability (the four orphans above would fail),
  `required`/enum equality against `contracts/*.schema.json`, and
  `operationId` vs `operation`; (3) building `components.schemas` from
  `contracts/` instead of restating them by hand, which the `info.description`
  already half-promises (`apidocs.go:17`).

- Stale doc paths, no second live copy — `docs/backend-decomposition-gemini4-argon.md:36,188,190`.
  `rg -n "openapi\.go|apidocs" docs backend/README.md README.md` shows this doc
  still cites `httpapi/openapi.go:1748-1759` and `:12-1855`; that file moved to
  `internal/apidocs/apidocs.go`. No second live copy of the API description
  exists under `docs/`; the matches are plans and reviews. Remedy: none required
  for `apidocs`, but the historical path is now wrong. `docs.go` references
  `swagger-ui-dist@5` (floating major, `backend/internal/apidocs/docs.go:13,18`)
  while the repo pins scanners exactly.

## Earned (checked, keep)

- Route coverage: a Python diff of `routes.go` against `Spec.paths` finds no
  route missing and no extra path; `openapi_test.go:16-60` enforces it, and both
  `/docs` and `/docs/` are intentionally documented once (`openapi_test.go:30-32`).
- `QueryResult` matches `contracts/query-result.schema.json` exactly (same nine
  `required` keys, same properties), and the error enum is guarded
  (`backend/internal/httpapi/contract_test.go:1216-1239`).
- `AIGenerationRequest` (`apidocs.go:1429-1441`) matches `schema.GenerationRequest`
  field for field (`backend/internal/schema/model.go:64-79`); `rg '"steps"|Steps'`
  over `apidocs.go` returns nothing, so the deleted field is gone from the spec.
- `AIJob`, `AIJobStage`, `AIJobHandle` match `jobs.JobView` and the stage/status
  constants (`backend/internal/ai/jobs/jobs.go:60-88`,
  `backend/internal/schema/status.go:11-15`); `AIJobStage.queued/running` is real
  (`backend/internal/ai/pipeline.go:222,229`).
- `AIOutcome`, `AICourseRecord.status`, `AIRecord.status` match the status
  constants (`backend/internal/schema/status.go:20-51`), including `discarded`
  as job-only.
- `EnvironmentResponse` matches `backend/internal/httpapi/environment.go:56-63`.
- `CapabilitiesResponse` and `HealthResponse` match the literal maps at
  `backend/internal/httpapi/handlers.go:28-79` and `plan.go:219-222`.
- `AdminAIConfig`, `AdminAIUpdate`, `AdminAIModelList` match
  `backend/internal/httpapi/admin_ai.go:32-111`.
- `EventRequest.datasetVersion`/`exerciseVersion` are live: the client sends them
  (`web/src/state/store.tsx:1248-1249`).
- `SwaggerUI`/`DocsHandler` are a single self-contained page that loads
  `/openapi.json`; no second renderer (`docs.go:7-36`).

## UNCERTAIN

- `Exercise` accepted-field set (`apidocs.go:1207-1235`): I did not prove which
  of `result`, `expected_result`, `expected_rows`, `checks`, `reference_solution`,
  `solution` the evaluator still reads. Needs a trace through `eval.Request` and
  `evaluateRequest.UnmarshalJSON` before any deletion.
- `operationId` vs `operation`: owner decides whether the document is a codegen
  contract or a human page. If codegen, keep both and add the drift test; if not,
  delete `operationId`.
- `ErrorEnvelope` enum membership for `AI_*`, `ADMIN_REQUIRED`,
  `PREFERENCES_UNAVAILABLE`: already an owner decision (sibling review
  `httpapi.md:274`).
- `AIPatchProposal`: delete or `$ref` from `AIDefectVerdict.patch` and
  `AIPatchResult.patch` (`apidocs.go:1882,1895`). Low conviction either way.
- `contracts/ai.schema.json $defs.patchDecision` requires `patch_id` in the body,
  but the handler takes it from the path and overwrites it
  (`backend/internal/httpapi/ai.go:742`), and the spec omits it
  (`apidocs.go:1887-1890`). The contract looks stale; confirm before editing it.
- `AdminAIUpdate` accepts aliases `none`, `disabled`, `deterministic`, `openai`
  (`backend/internal/httpapi/admin_ai.go:131-138`) that the spec enum does not
  list (`apidocs.go:1740`). Greenfield says delete the aliases; owner confirms.
- `contracts/ai.schema.json $defs.generationRequest.exercise_count` bounds it to
  1..40; the spec documents a bare integer (`apidocs.go:1438`) and the Go struct
  has no bound (`model.go:71`). Which layer owns the bound needs an owner call.
- Out of domain but adjacent: `contract_test.go:791` still sends the deleted
  `"steps"` field in the AI-off case body. It is inert there (the AI-off path
  returns before validation) and should be dropped.
