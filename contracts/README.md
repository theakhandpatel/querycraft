# Contracts

Language-neutral source of truth shared by the React client (`web/`) and the Go
gateway (`backend/`). Both sides validate against these schemas in tests.

| File | Governs |
| --- | --- |
| `query-result.schema.json` | `POST /query` output, incl. the stable error codes |
| `evaluation.schema.json` | `POST /answers/evaluate` output (`qr`, `checks`, `passed`, `duration_ms`) and the feedback-code set |
| `exercise.schema.json` | An exercise bundle and its result contract (the `exercise` object of an `/answers/evaluate` request) |
| `export.schema.json` | The client-side workbook export document (design §8.3), which is also the input the import validates before merging it |

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
| `POST /courses/generate` | `openapi.json` → `CourseGenerateResponse` |
| `POST /challenges/generate` | `openapi.json` → `OkResponse` |
| `GET /openapi.json` | The OpenAPI document itself |
| `GET /docs` | Swagger UI (HTML) |

An earlier `evaluation.schema.json` described a persisted *Evaluation record*
(`attempt_id`, `status`, `feedback_codes`, `evaluator_version`) taken from
`docs/technical-design.md` §4.4. No endpoint or storage path ever emitted that
record, so the schema was rewritten to the response `POST /answers/evaluate`
actually returns. A persisted record would need its own schema.

See `docs/technical-design.md` sections 4 and 6 for the full rules.
