# apidocs — wave 2 verification

## Verdict

Most of wave 1 survives. Seven items are confirmed exactly as stated: the four
dead schemas/list collapse to three confirmed deletions plus one amended, the
`/runs/{id}` mismatch, the missing `bytes` in two `required` lists, the
`ErrorEnvelope.code` enum omissions, and the two statement-route 400 shapes. I
re-derived every `$ref` reachability result with my own pass over the `Spec`
literal, and it returned the same four orphans. The `operationId` item does not
survive as a "written twice" claim: `operation` is the access-log label and
`operationId` is the OpenAPI operation id, so they are two names for two
consumers, not one fact stated twice; it stays an owner call. The out-of-domain
stale-doc item is dropped from the actionable list. One missed item is added:
the GET `/progress/{course}` schema rejects the null snapshot the handler
actually writes.

## Confirmed actions

Ordered by value. Wave-1 item numbers in parentheses.

- [ ] Delete `components.schemas.CourseGenerateResponse` and the unused test mirror `courseGenerateSchema` (Delete 1).
  Files: `backend/internal/apidocs/apidocs.go:1954-1962`, `backend/internal/httpapi/contract_test.go:599-608`.
  Evidence: my `$ref` reachability pass over the `Spec` literal reports
  `unreachable: ['AIPatchProposal', 'ChallengeGenerateRequest', 'CourseGenerateResponse', 'RunResponse']`.
  `rg -n "CourseGenerateResponse"` matches only the schema and
  `contract_test.go:599`, and `rg -n "courseGenerateSchema"` matches only its own
  definition at `:600`. `POST /courses/generate` answers `AIOutcome`
  (`apidocs.go:378,382,390,394`), never this shape. Safe now: nothing references
  it, and greenfield says delete rather than keep an unused definition. Delete
  the object member including its trailing comma so the JSON stays valid.

- [ ] Delete `components.schemas.ChallengeGenerateRequest` (Delete 2).
  Files: `backend/internal/apidocs/apidocs.go:1963-1966`.
  Evidence: the same reachability pass marks it unreachable. Its description
  ("the body is accepted and discarded") is false: `POST /challenges/generate`
  refs `AIGenerationRequest` (`apidocs.go:703`), and `runGeneration` decodes
  `schema.GenerationRequest` then sets the kind (`backend/internal/httpapi/ai.go:182-188`),
  so the body is read, not discarded. Safe now: no path refs it.

- [ ] Point the `/runs/{id}` 200 body at `RunResponse` instead of `RunTranscript` (Collapse 1).
  Files: `backend/internal/apidocs/apidocs.go:944`.
  Evidence: `handleGetRun` writes `{"ok": true, "run": runView(rec)}`
  (`backend/internal/httpapi/runs.go:110`). `RunResponse` is exactly
  `{ok, run}` and its `run` is a `RunTranscript` (`apidocs.go:1596-1601`), so it
  stays reachable after the change. The conformance test reads the wrapper:
  `decoded["run"]` (`backend/internal/httpapi/runs_test.go:289`). Safe now: the
  handler and test both prove the wrapper, and this makes the last orphan the
  live shape. Change the single `$ref` at `:944`.

- [ ] Add `bytes` to both `required` lists (Collapse 2).
  Files: `backend/internal/apidocs/apidocs.go:1556` (`RunSummary.required`), `:1573` (`RunTranscript.required`).
  Evidence: `Bytes int` carries no `omitempty` in both Go structs
  (`backend/internal/schema/transcript.go:72,87`), and `contracts/run.schema.json`
  lists `bytes` in both `$defs.runSummary.required` and
  `$defs.runTranscript.required` (read with a JSON dump). The spec omits it in
  both. Safe now: add the one word `"bytes"` to each list.

- [ ] Extend the `ErrorEnvelope.code` enum with the codes the handlers emit (Collapse 3).
  Files: `backend/internal/apidocs/apidocs.go:2049`.
  Evidence: `rg -no 'errorEnvelope\(w, [^,]+, "[A-Z_]+"' backend/internal/httpapi`
  returns `AI_INVALID_REQUEST` (`admin_ai.go:142,149,154,160,165,187,191`),
  `ADMIN_REQUIRED` (`auth.go:111`), and `PREFERENCES_UNAVAILABLE`
  (`preferences.go:32,62`), none in the enum. `writeAIError` writes
  `aiErr.Code` (`backend/internal/httpapi/ai.go:791`) from the closed set at
  `backend/internal/ai/api.go:31-40`: `AI_UNAVAILABLE`, `AI_INVALID_REQUEST`,
  `AI_INVALID_ARTIFACT`, `AI_REFERENCE_FAILED`, `AI_CROSS_CHECK_FAILED`,
  `AI_REPAIR_EXHAUSTED`, `AI_NOT_FOUND`, `AI_PATCH_UNVERIFIED`,
  `AI_PATCH_UNAPPROVED` are all missing (`ENVIRONMENT_UNAVAILABLE` is already
  present). The spec also names `ADMIN_REQUIRED` in four 403 descriptions
  (`:439,510,539,562`) the enum omits. The existing guard only checks that the
  enum is a superset of the QueryResult set (`backend/internal/httpapi/contract_test.go:1230-1239`),
  so these omissions are untested. Safe now: add the 11 codes, or derive the enum
  from `ai.Code*` and the literal call sites.

- [ ] Document the second 400 shape on `/query`, `/explain`, `/courses/generate`, `/challenges/generate` (Collapse 4).
  Files: `backend/internal/apidocs/apidocs.go:106,128` (statement routes), `:382,716` (AI routes).
  Evidence: `handleQuery` calls `readJSON` first (`backend/internal/httpapi/handlers.go:117`),
  `handleExplain` does the same (`backend/internal/httpapi/plan.go:51`), and both
  generation routes call `readJSON` through `runGeneration`
  (`backend/internal/httpapi/ai.go:183`). `readJSON` answers `errorEnvelope` with
  `requestErrorCode` (`backend/internal/httpapi/server.go:294,266-284`):
  `vocabStatement` gives `EMPTY_QUERY`/`QUERY_SYNTAX_ERROR`, `vocabAI` gives
  `AI_INVALID_REQUEST` (routes declared at `routes.go:91-92,106,107,123`). So a
  malformed body yields `ErrorEnvelope`, while a bad statement yields
  `QueryResult` (`handlers.go:145,156`) or `PlanBundle` (`plan.go:126`). Safe now:
  express the 400 content as `{"oneOf": [QueryResult/PlanBundle, ErrorEnvelope]}`
  or state the split in the description; the handlers do not change.

- [ ] Move the `Spec` string to `apidocs/spec.json` behind `go:embed` (Move 1).
  Files: `backend/internal/apidocs/apidocs.go:12-2059`, `:2062`.
  Evidence: `const Spec` is a hand-authored JSON literal and
  `var Document = json.RawMessage(Spec)`; the end state is named in
  `docs/target-architecture.md:70-72` and `docs/quality-review.md:122-123`. This
  is the owner-approved direction, so it is an action, not a finding. The
  reachability pass is ready to become the drift test step it calls for.

## Amended

- `AIPatchProposal`: the orphan is real, the remedy should be `$ref`, not a bare
  delete (Delete 3, wave-1 leaves this to an owner).
  Files: `backend/internal/apidocs/apidocs.go:1867-1875`; targets at `:1882`
  (`AIDefectVerdict.patch`) and `:1895` (`AIPatchResult.patch`).
  Evidence: reachability marks it unreachable; `rg -n "AIPatchProposal"` matches
  only `apidocs.go:1867`. But the shape is live: `contracts/ai.schema.json:241`
  refs `$defs.patchProposal` (`:246-264`) and the Go types use it
  (`ai.DefectVerdict.Patch`, `ai.PatchResult.Patch`). The spec instead declares
  both `patch` properties as bare `{"type": ["object", "null"]}`. Deleting the
  schema removes the only in-spec description and leaves the loose objects as the
  spec's truth, which contradicts the contract. Amendment: ref the two `patch`
  properties at `AIPatchProposal` (or generate them from `contracts/`), then the
  schema is reachable and no longer an orphan. Delete only if the loose objects
  are the accepted published shape, and then say so.

## Dropped

- Stale doc paths (`docs/backend-decomposition-gemini4-argon.md:36,188,190`);
  dropped as an actionable item. The cited lines do name the old
  `httpapi/openapi.go` path, but the file is a decomposition plan that proposed
  the very move, and its line 196 argues to delete the string. No live consumer
  reads the path. Out of the bounded domain (`backend/internal/apidocs/**`,
  `routes.go`, `contracts/`), so no edit is owed here.
- `operationId` vs `operation` as "route identity written twice" (Collapse 5);
  dropped as framed, kept as an owner call below. `operation` is documented as
  the access-log label (`backend/internal/httpapi/routes.go:55-59`) and
  `operationId` is the OpenAPI operation id; they are two names for two
  consumers, not one fact duplicated. `rg -n "operationId|operation_id"` outside
  `apidocs/` returns nothing, and `openapi_test.go` checks neither. The mismatch
  (`generate_course` vs `postCourseGenerate`) is cosmetic unless a generator
  consumes the document.

## New

- `ProgressResponse.snapshot` rejects the null the handler writes (missed by wave 1).
  Files: `backend/internal/apidocs/apidocs.go:1923`; `ProgressSnapshot` at
  `:1926-1936`; handler `backend/internal/httpapi/handlers.go:391-400`.
  Evidence: the spec declares
  `"snapshot": {"oneOf": [{}, {"type": "null"}]}`. An empty schema matches every
  instance, and null matches both branches, so `oneOf` fails for null (JSON
  Schema requires exactly one match). `handleGetProgress` returns
  `{"ok": true, "snapshot": nil}` when nothing is stored (`handlers.go:391`), and
  the 200 description even says "null when nothing is stored yet"
  (`apidocs.go:277`). The correct schema already exists one line below:
  `ProgressSnapshot` (`:1926`) is the explicit any-JSON union that includes null.
  Safe now: change `snapshot` to
  `{"$ref": "#/components/schemas/ProgressSnapshot"}`, the same ref the PUT
  request body uses (`:307`).

## Still uncertain

- `operationId` vs `operation`: no code proves whether the document has an
  external codegen consumer. A generator, a published SDK, or a stated decision
  that `/openapi.json` is a human page would settle it. If none exists, delete
  `operationId`; if one exists, keep both and add a drift test.
- `Exercise` accepted-field set (`apidocs.go:1207-1235`): needs a trace through
  `eval.Request` and `evaluateRequest.UnmarshalJSON` to prove which of `result`,
  `expected_result`, `expected_rows`, `checks`, `reference_solution`, `solution`
  the evaluator reads.
- `contracts/ai.schema.json $defs.patchDecision` requires `patch_id`
  (`contracts/ai.schema.json:269`) but `handlePatch` takes it from the path and
  overwrites it (`backend/internal/httpapi/ai.go:742`); the spec omits it
  (`apidocs.go:1887-1890`). Confirm the contract is stale before editing.
- `AdminAIUpdate` accepts aliases `none`, `disabled`, `deterministic`, `openai`
  (`backend/internal/httpapi/admin_ai.go:131-139`) that the spec enum omits
  (`apidocs.go:1740`). Greenfield says delete the aliases; owner confirms.
- `contracts/ai.schema.json` bounds `exercise_count` to 1..40
  (`contracts/ai.schema.json:176`), the spec documents a bare integer
  (`apidocs.go:1438`), and the Go struct has no bound
  (`backend/internal/schema/model.go:71`). Which layer owns the bound needs an
  owner call.
- `backend/internal/httpapi/contract_test.go:791` still sends the deleted
  `"steps"` field in the AI-off body. It is inert there (the AI-off path returns
  before validation) and should be dropped, but it is outside this domain.
