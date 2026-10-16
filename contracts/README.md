# Contracts

Language-neutral source of truth shared by the React client (`web/`) and the Go
gateway (`backend/`). Both sides validate against these schemas in tests.

| File | Governs |
| --- | --- |
| `query-result.schema.json` | `POST /query` output, incl. the stable error codes |
| `evaluation.schema.json` | `POST /answers/evaluate` output (`qr`, `checks`, `passed`, `duration_ms`) and the feedback-code set |
| `exercise.schema.json` | An exercise bundle and its result contract (the `exercise` object of an `/answers/evaluate` request) |
| `export.schema.json` | The client-side workbook export document (design §8.3), which is also the input the import validates before merging it |
| `ai.schema.json` | The M4 AI generation surface (design §11): the generation request and its `outcome`, the activated `challengeArtifact` and its `provenance`, the stage evidence, and the tutoring, critique and defect surfaces. Its error enum is an AI envelope, separate from the closed QueryResult enum |
| `plan.schema.json` | The M5 performance lab: `POST /explain` output (design §14 M5). `summary` and `environment` carry the deterministic, gradeable facts; `plan` is the engine's own JSON, reported verbatim; `timing_precision` names what the numbers are worth |
| `perf-contract.schema.json` | The M5 plan-based result contract attached to a performance exercise. Structural assertions only: no timing field exists, by design |
| `run.schema.json` | The agent-run audit trail: one generation run's whole transcript (`runTranscript`), the list shape (`runSummary`), and one ordered fact (`entry`). Read over `GET /runs` and `GET /runs/{id}` |

`export.schema.json` is the odd one out: it describes no endpoint. It is the
contract between the export and the import on the client, and it lives here so
there is one file to change when the document changes. `lib/importBundle.ts`
validates against it with `ajv` in the browser, and `scripts/import-audit.mjs`
does the same in Node, so the schema is executed on both sides.

Only those three responses have a contract file. Every other route is described
by the OpenAPI 3.1 document served at `GET /openapi.json` (rendered at
`GET /docs`); `backend/internal/httpapi/contract_test.go` checks each response
against its schema. The full map:

| Endpoint | Schema |
| --- | --- |
| `GET /health` | `openapi.json` → `HealthResponse` |
| `GET /capabilities` | `openapi.json` → `CapabilitiesResponse` |
| `POST /query` | `query-result.schema.json` |
| `POST /answers/evaluate` | `evaluation.schema.json` (request `exercise`: `exercise.schema.json`) |
| `GET /datasets/{id}/tables` | `openapi.json` → `TablesResponse` |
| `GET /progress/{course}` | `openapi.json` → `ProgressResponse` |
| `PUT /progress/{course}` | `openapi.json` → `OkResponse` |
| `POST /events` | `openapi.json` → `EventResponse` |
| `POST /courses/generate` | `ai.schema.json` → `outcome` |
| `POST /courses/{id}/build` | `ai.schema.json` → `outcome` (or `openapi.json` → `AIJobSubmitted` with `Prefer: respond-async`) |
| `POST /challenges/generate` | `ai.schema.json` → `outcome` |
| `GET /challenges` | `ai.schema.json` → `record[]` |
| `GET /challenges/{id}` | `ai.schema.json` → `record` |
| `POST /challenges/{id}/reset` | `openapi.json` → `OkResponse` |
| `POST /tutor` | `ai.schema.json` → `tutorResponse` |
| `POST /challenges/{id}/critique` | `ai.schema.json` → `critiqueResponse` |
| `POST /challenges/{id}/report` | `ai.schema.json` → `defectVerdict` |
| `POST /challenges/{id}/patches/{patchID}` | `ai.schema.json` → `patchDecision` → `patchResult` |
| `GET /runs` | `run.schema.json` → `runSummary[]` |
| `GET /runs/{id}` | `run.schema.json` → `runTranscript` |
| `POST /explain` | `plan.schema.json` (request `exercise.perf`: `perf-contract.schema.json`) |
| `GET /openapi.json` | The OpenAPI document itself |
| `GET /docs` | Swagger UI (HTML) |

An earlier `evaluation.schema.json` described a persisted *Evaluation record*
(`attempt_id`, `status`, `feedback_codes`, `evaluator_version`) taken from
`docs/technical-design.md` §4.4. No endpoint or storage path ever emitted that
record, so the schema was rewritten to the response `POST /answers/evaluate`
actually returns. A persisted record would need its own schema.

See `docs/technical-design.md` sections 4 and 6 for the full rules.
