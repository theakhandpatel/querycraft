# Backend decomposition

This note records the backend decomposition. It lists the packages after the
work, the four moves, and the claims two earlier drafts make that the code does
not support. Those drafts stay in place; the owner has not decided their fate.
The command `cd backend && go build ./... && go vet ./... && go test ./...`
passes.

## The packages, one job each

| Package | Job | Imports from `internal/` |
| --- | --- | --- |
| `internal/schema` | The persisted model: pipeline stages, courses, transcripts, records. | none |
| `internal/dialect` | SQL dialects and error classification. | none |
| `internal/dataset` | `schema.sql`, `seed.sql` and the data catalog. | none |
| `internal/metrics` | The registry and the Prometheus render. | none |
| `internal/engine` | The one execution path for a statement. | `dialect` |
| `internal/eval` | Grades a result. | `engine` |
| `internal/perf` | Captures a plan and grades it. | `engine` |
| `internal/store` | Persistence for users, challenges, courses, runs. | `schema` |
| `internal/envdb` | The learner environment, PostgreSQL and SQLite backends. | `dataset`, `engine`, `metrics` |
| `internal/auth` | Sessions. | `store` |
| `internal/ai` | Model-driven generation, the pipeline, the course agent, jobs, review. | `schema`, `dataset`, `engine`, `eval` |
| `internal/apidocs` | The OpenAPI document and the Swagger UI page. | standard library only |
| `internal/httpapi` | Routing and handlers. | `ai`, `apidocs`, `auth`, `dataset`, `engine`, `envdb`, `eval`, `metrics`, `perf`, `schema`, `store`, `webui` |
| `internal/localdb` | Starts and adopts the local PostgreSQL server. | `dataset` |
| `internal/pgtest` | Boots a real PostgreSQL server for tests. | `dataset`, `localdb` |
| `internal/webui` | Embeds the built SPA. | none |
| `cmd/api` | Process wiring. | `ai`, `auth`, `dataset`, `engine`, `envdb`, `httpapi`, `localdb`, `store` |

## Move 1: the model moves to `internal/schema`

`internal/store` imported `internal/ai`, so persistence depended on the AI
package for its model types. The store referenced 39 distinct `ai.*` symbols.
Moving the model into `internal/schema` broke that edge. `internal/schema`
imports nothing from `internal/`, so persistence no longer depends on
generation.

## Move 2: `engine/explain.go` moves to `internal/perf`

`internal/engine` imported `internal/perf`, so execution depended on grading.
Moving `engine/explain.go` into `internal/perf` inverted the edge. The move had
two side effects. `engine` now exports `IsBlankSQL` and `ClassifyFor`
(`engine/execute.go`) for the capture. The unused `PgxExecutor.Explain` method
is gone. `TestGradeReadsNoTiming` (`perf/grade_test.go`) skips `explain.go`,
because the capture builds the measurements on purpose.

## Move 3: the OpenAPI document moves to `internal/apidocs`

`internal/httpapi` held a 1893-line OpenAPI document (`httpapi/openapi.go`).
`internal/apidocs` now owns it (`apidocs/apidocs.go`). While moving it, the work
found that `/courses/generate` and `/challenges/generate` require a session but
declared no security and no 401. Both now declare
`"security": [{"bearerAuth": []}]` and a 401 response.
`TestDocumentedSecurityMatchesTheHandlers` (`httpapi/openapi_test.go`) checks
the document against the handlers.

## Move 4: the SQLite environment moves to `internal/envdb`

`internal/httpapi` held a 477-line SQLite environment manager
(`httpapi/sqlite.go`), which is not HTTP code. It is the second backend of the
job `internal/envdb` already owns, so `internal/envdb` now holds both
(`envdb/sqlite.go`). The move changed one line: the package clause.

## Claims that did not survive verification

The two earlier drafts in `docs/` assert things the code does not support. The
corrections follow, one per claim.

- The draft says `datasetBrief` is rebuilt on every agent turn inside the round
  loop. False. `datasetBrief` has three sites: `ai/course.go:310`,
  `ai/pipeline.go:824`, and its definition at `ai/pipeline.go:839`. The agent
  loop in `ai/course_agent.go` never calls it.
- The draft says `isBlankSQL` duplicates `tokenizeSQL`. False.
  `engine.IsBlankSQL` (`engine/execute.go`) strips whitespace and comments to
  answer "is this empty". `ai.tokenizeSQL` (`ai/validate.go`) is a lexer that
  also handles dollar-quotes and quoted identifiers. They answer different
  questions, and `internal/ai` imports `internal/engine`, so one cannot serve
  the other.
- The draft says the `envdb` to `metrics` edge is one to remove. False.
  `cmd/api/main.go:206` sets `Metrics: api.Registry()`, so the three env metrics
  (`querycraft_environment_create_seconds`,
  `querycraft_environment_reset_seconds`, `querycraft_environments_active`) are
  recorded in production.
- The draft says the store references five `ai.*` symbols. It referenced 39.

## Open

These items are true today.

- The secured and public route lists in
  `TestDocumentedSecurityMatchesTheHandlers` (`httpapi/openapi_test.go`) are
  still hand-written.
- `internal/ai` is the largest package and internally modular. No split is
  planned.
- `internal/httpapi` still resolves datasets (`httpapi/dataset.go`) and holds
  the sign-in throttle (`httpapi/login_throttle.go`).
