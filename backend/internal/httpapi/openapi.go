package httpapi

import (
	"encoding/json"
	"net/http"
)

// openAPISpec is the hand-authored OpenAPI 3.1 description of the gateway. It
// documents every endpoint served by Handler() (the /openapi.json and /docs
// routes included), the QueryResult shape shared with
// contracts/query-result.schema.json, and the stable error codes.
const openAPISpec = `{
  "openapi": "3.1.0",
  "info": {
    "title": "Querycraft Gateway API",
    "version": "1.0.0",
    "description": "Adapter B: the JSON HTTP surface the Querycraft client talks to. Response schemas mirror contracts/*.schema.json where a contract file exists; the rest are defined here."
  },
  "servers": [{"url": "/"}],
  "security": [],
  "tags": [
    {"name": "system", "description": "Liveness, advertised capabilities, metrics and diagnostics."},
    {"name": "auth", "description": "Local accounts and bearer-token sessions."},
    {"name": "query", "description": "SQL execution and answer evaluation."},
    {"name": "environment", "description": "The learner's writable workspace and its reset."},
    {"name": "datasets", "description": "Dataset introspection."},
    {"name": "progress", "description": "Per-course progress snapshots."},
    {"name": "events", "description": "Client telemetry events."},
    {"name": "generation", "description": "Course and challenge generation."}
  ],
  "paths": {
    "/health": {
      "get": {
        "operationId": "getHealth",
        "tags": ["system"],
        "summary": "Liveness probe that also reports the engine version.",
        "responses": {
          "200": {
            "description": "Gateway is up.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/HealthResponse"}}}
          }
        }
      }
    },
    "/capabilities": {
      "get": {
        "operationId": "getCapabilities",
        "tags": ["system"],
        "summary": "Advertised execution limits and isolation mode.",
        "responses": {
          "200": {
            "description": "Capabilities.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/CapabilitiesResponse"}}}
          }
        }
      }
    },
    "/metrics": {
      "get": {
        "operationId": "getMetrics",
        "tags": ["system"],
        "summary": "Prometheus text exposition of request, statement and pool metrics.",
        "description": "Unlabelled series arrive without braces. Labels are a bounded set (route patterns, method, status class, error code), so scraping never grows unbounded series. The pool and readiness gauges are sampled at scrape time.",
        "responses": {
          "200": {
            "description": "Prometheus text exposition (version 0.0.4).",
            "content": {"text/plain": {"schema": {"type": "string"}}}
          }
        }
      }
    },
    "/diagnostics": {
      "get": {
        "operationId": "getDiagnostics",
        "tags": ["system"],
        "summary": "Measured readiness, versions, pool state and sandbox facts.",
        "description": "Never includes a DSN, password or hash. The learner subsection is read over the restricted connection, so it reports the privileges learner SQL actually holds.",
        "responses": {
          "200": {
            "description": "Diagnostics report.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/DiagnosticsResponse"}}}
          }
        }
      }
    },
    "/query": {
      "post": {
        "operationId": "postQuery",
        "tags": ["query"],
        "summary": "Execute a SQL statement in a read-only transaction and return its rows.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/QueryRequest"}}}
        },
        "responses": {
          "200": {
            "description": "Statement succeeded.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/QueryResult"}}}
          },
          "400": {
            "description": "Statement failed; the body is still a QueryResult whose error.code is a stable code (EMPTY_QUERY, QUERY_SYNTAX_ERROR, QUERY_RUNTIME_ERROR, QUERY_TIMEOUT, RESULT_LIMIT_EXCEEDED, ENVIRONMENT_UNAVAILABLE, DIALECT_MISMATCH).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/QueryResult"}}}
          }
        }
      }
    },
    "/explain": {
      "post": {
        "operationId": "postExplain",
        "tags": ["query"],
        "summary": "Capture an EXPLAIN plan, optionally executing the statement with ANALYZE.",
        "description": "The response is the PlanBundle described by contracts/plan.schema.json: 'summary' and 'environment' carry the deterministic, gradeable facts (node types, relations, indexes, the planner's own row estimates, engine version, row counts and planner settings) and 'plan' is the engine's own JSON plan, reported verbatim and never graded. 'timing_precision' names what the numbers beside it are worth. A graded assertion never reads a timing. EXPLAIN ANALYZE executes the statement, so it requires a signed-in learner's writable environment and is refused with ENVIRONMENT_UNAVAILABLE for an anonymous caller rather than silently downgraded to a plain EXPLAIN; with no database the route reports ENVIRONMENT_UNAVAILABLE and never invents a plan.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PlanRequest"}}}
        },
        "responses": {
          "200": {
            "description": "The plan was captured.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PlanBundle"}}}
          },
          "400": {
            "description": "The plan could not be captured; the body is still a PlanBundle whose error.code is a stable code (EMPTY_QUERY, QUERY_SYNTAX_ERROR, QUERY_RUNTIME_ERROR, QUERY_TIMEOUT, RESULT_LIMIT_EXCEEDED, ENVIRONMENT_UNAVAILABLE, EVALUATION_ERROR).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PlanBundle"}}}
          }
        }
      }
    },
    "/answers/evaluate": {
      "post": {
        "operationId": "postAnswersEvaluate",
        "tags": ["query"],
        "summary": "Run the learner's SQL and grade it against an exercise contract.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EvaluateRequest"}}}
        },
        "responses": {
          "200": {
            "description": "Evaluation result. The embedded qr carries any execution error.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EvaluateResponse"}}}
          },
          "400": {
            "description": "The request body was empty or malformed JSON; the body is the ErrorEnvelope (EMPTY_QUERY or QUERY_SYNTAX_ERROR).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/environment": {
      "get": {
        "operationId": "getEnvironment",
        "tags": ["environment"],
        "summary": "Describe the caller's writable learner workspace.",
        "description": "A signed-in learner gets their own database, copied from the sealed template and created on first use. An anonymous caller has no workspace and is told so honestly (exists=false, writable=false) rather than being shown the shared dataset as if it were theirs. A gateway with no database answers 503 ENVIRONMENT_UNAVAILABLE.",
        "responses": {
          "200": {
            "description": "The caller's workspace facts.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EnvironmentResponse"}}}
          },
          "503": {
            "description": "No workspace can be provided (no database, or the capacity cap is reached); the body is the ErrorEnvelope with code ENVIRONMENT_UNAVAILABLE.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/environment/reset": {
      "post": {
        "operationId": "postEnvironmentReset",
        "security": [{"bearerAuth": []}],
        "tags": ["environment"],
        "summary": "Restore the sealed dataset, discarding the learner's own changes.",
        "description": "Drops the caller's environment database and copies the sealed template again. Destructive to the caller's own workspace only. Requires a signed-in account; an anonymous caller is refused with AUTH_REQUIRED. When the course parameter names a stored course, that course's own setup statements are applied to the restored workspace in order, so the learner's database is the one the course's exercises were verified against; setup_statements reports how many were applied. A course is resolved owner-scoped, and a course id that is not the caller's is refused with 404 NOT_FOUND so it cannot be probed for existence.",
        "parameters": [
          {
            "name": "dataset",
            "in": "query",
            "required": false,
            "schema": {"type": "string", "enum": ["commerce", "perf", ""]},
            "description": "Which workspace to restore. Absent, \"\" and \"commerce\" all restore the caller's challenge workspace; \"perf\" restores the plan lab, which is a separate database so a re-indexed plan lesson cannot change the plan a challenge lesson is graded on. A name this gateway does not serve is refused with ENVIRONMENT_UNAVAILABLE naming the datasets it does."
          },
          {
            "name": "course",
            "in": "query",
            "required": false,
            "schema": {"type": "string"},
            "description": "A stored course whose setup should be re-applied after the template is copied, so the learner's environment equals the one its exercises were verified in. Resolved owner-scoped; an id that is not the caller's, or does not exist, is refused with 404 NOT_FOUND and applies nothing. A course cannot be combined with the \"perf\" dataset: a course runs in its own workspace, so that request is refused with 400."
          },
          {
            "name": "dialect",
            "in": "query",
            "required": false,
            "schema": {"type": "string", "enum": ["postgresql", "sqlite"]},
            "description": "Which engine's workspace to restore. Absent means postgresql, so a client that has never heard of the field keeps the behaviour it had. \"sqlite\" restores the learner's SQLite database file to the committed fixture; only a signed-in account has one. A dialect this gateway does not serve is refused with DIALECT_MISMATCH. The dialect matters because the two workspaces are different things — a database versus a file — so resetting one while the learner is looking at the other would report a restore that did not happen."
          }
        ],
        "responses": {
          "200": {
            "description": "The workspace was restored; duration_ms is the measured end-to-end reset time in milliseconds and setup_statements is how many of the named course's setup statements were applied (0 when no course was named).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EnvironmentResetResponse"}}}
          },
          "400": {
            "description": "The request named a course together with the perf dataset, a dataset this gateway does not serve, or a dialect it cannot reset (DIALECT_MISMATCH). The body is the route's own ErrorEnvelope ({ok:false,error}), not a QueryResult — a reset is not a statement.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "401": {
            "description": "No valid session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "404": {
            "description": "The request named a course that does not exist or is not the caller's; nothing was reset and nothing was applied.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "No workspace can be provided; the body is the ErrorEnvelope with code ENVIRONMENT_UNAVAILABLE.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/datasets/{id}/tables": {
      "get": {
        "operationId": "getDatasetTables",
        "tags": ["datasets"],
        "summary": "List a dataset's tables, columns and preview rows.",
        "parameters": [
          {
            "name": "id",
            "in": "path",
            "required": true,
            "description": "Dataset identifier, e.g. commerce.",
            "schema": {"type": "string"}
          },
          {
            "name": "dialect",
            "in": "query",
            "required": false,
            "schema": {"type": "string", "enum": ["postgresql", "sqlite"]},
            "description": "Which engine's workspace to describe. Absent means postgresql. \"sqlite\" introspects the SQLite workspace over the executor seam — the learner's own file when signed in, the shared fixture otherwise — so a SQLite chapter's drawer lists SQLite relations and their declared types (affinities), not PostgreSQL's."
          }
        ],
        "responses": {
          "200": {
            "description": "Dataset introspection.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/TablesResponse"}}}
          },
          "404": {
            "description": "Unknown dataset.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/progress/{course}": {
      "get": {
        "operationId": "getProgress",
        "tags": ["progress"],
        "summary": "Load the signed-in account's progress snapshot for a course.",
        "security": [{"bearerAuth": []}],
        "parameters": [
          {
            "name": "course",
            "in": "path",
            "required": true,
            "description": "Course slug, e.g. sql-analytics.",
            "schema": {"type": "string"}
          }
        ],
        "responses": {
          "200": {
            "description": "Snapshot (null when nothing is stored yet).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ProgressResponse"}}}
          },
          "401": {
            "description": "No valid session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "500": {
            "description": "Snapshot could not be loaded.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      },
      "put": {
        "operationId": "putProgress",
        "tags": ["progress"],
        "security": [{"bearerAuth": []}],
        "summary": "Replace the stored progress snapshot for a course with arbitrary JSON.",
        "parameters": [
          {
            "name": "course",
            "in": "path",
            "required": true,
            "description": "Course slug, e.g. sql-analytics.",
            "schema": {"type": "string"}
          }
        ],
        "requestBody": {
          "required": true,
          "description": "Any valid JSON snapshot value.",
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ProgressSnapshot"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "Snapshot saved.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/OkResponse"}}}
          },
          "400": {
            "description": "Body was empty or not valid JSON.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "500": {
            "description": "Snapshot could not be saved.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/events": {
      "post": {
        "operationId": "postEvent",
        "tags": ["events"],
        "security": [{"bearerAuth": []}],
        "summary": "Record a client telemetry event.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EventRequest"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "202": {
            "description": "Event accepted.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/EventResponse"}}}
          },
          "400": {
            "description": "The body was not a JSON object or carried no event id.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "500": {
            "description": "Event could not be stored.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/courses/generate": {
      "post": {
        "operationId": "postCourseGenerate",
        "tags": ["generation"],
        "summary": "Generate a course, one chapter at a time, through the chapter agent.",
        "description": "The model is an untrusted content generator; the gateway is the policy. The run asks for a small outline first, stores it as a 'building' course, and then holds one conversation in which the model hands in one chapter per save_chapter tool call. Each chapter is validated with the same gates a challenge is and run in the course's own scratch environment before it is appended to the stored plan, so GET /courses/{id} answers with the chapters that already verified while the rest are still being written. 'stages' reports what the pipeline did, and a course job's live progress is 'chapters_planned' / 'chapters_verified' / 'current_chapter'. 'status' is 'active' only for a course whose every stored exercise verified; a chapter the model cannot repair is dropped and reported; a run in which nothing verified is 'rejected' and stores nothing. Set 'require_approval' to have the run stop after the outline: the response status is then 'awaiting_approval', the stored 'building' course is the index to approve, and POST /courses/{id}/build continues it into the chapter phase. The pause is opt-in: without the flag the run is one request, outline into chapters. Requires a session. Answers 503 when AI is off or the gateway is at its concurrency limit, 400 for an invalid request. A generation takes minutes, so a client that cannot wait sends Prefer: respond-async (or async=1) and polls GET /generation/{id} instead of holding the request open; that path answers 202 with the job id.",
        "parameters": [{"name": "Prefer", "in": "header", "required": false, "schema": {"type": "string"}, "description": "Send a Prefer header of 'respond-async' (or pass async=1) to get a 202 with a job id instead of waiting for the envelope. The blocking envelope is the default."}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIGenerationRequest"}}}
        },
        "responses": {
          "202": {
            "description": "The run was accepted and continues in the background. Poll GET /generation/{id}.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIJobSubmitted"}}}
          },
          "201": {
            "description": "A verified artifact was activated.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "400": {
            "description": "The request itself was invalid.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "422": {
            "description": "Generation ran and nothing was activated; the outcome names the stage that refused.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "503": {
            "description": "AI is off, the database cannot verify, or the gateway is at capacity.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          }
        }
      }
    },
    "/courses": {
      "get": {
        "operationId": "listCourses",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "List the caller's stored generated courses, newest first.",
        "description": "Only the session's own courses are returned; the depot is scoped on write, so the filter lives in the handler. With AI off the answer is 503 and 'courses' is an empty array, never an invented row.",
        "responses": {
          "200": {
            "description": "Courses that survived activation, newest first.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AICourseList"}}}
          },
          "401": {
            "description": "No session.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "AI is off; the body is an AICourseList with ok=false and an empty 'courses'.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AICourseList"}}}
          }
        }
      }
    },
    "/courses/{id}": {
      "get": {
        "operationId": "getCourse",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Read one stored course with the evidence that admitted it.",
        "description": "A course that belongs to another learner is reported as 404, never 403, so its existence cannot be confirmed.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "The stored record.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AICourseResponse"}}}
          },
          "404": {
            "description": "No such course, or it belongs to another learner.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "AI is off.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      },
      "delete": {
        "operationId": "deleteCourse",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Forget a generated course.",
        "description": "Removes the generated plan only. Learner attempts and progress are the learner's data and are not touched. A course that belongs to another learner is reported as 404, never 403.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "Removed.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/OkResponse"}}}
          },
          "404": {
            "description": "No such course, or it belongs to another learner.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "AI is off.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/courses/{id}/build": {
      "post": {
        "operationId": "postCourseBuild",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Approve a paused course and build its chapters.",
        "description": "Continues a course that stopped at 'awaiting_approval': the stored outline is reloaded, the chapter conversation is rebuilt from the request stored beside it, and the chapter phase runs to completion, activating the course exactly as a default run would. A chapter that already verified is not re-written. The route is owner-scoped: a course that belongs to another learner is reported as 404, never 403. A course that is not still being built is refused with 409, because re-running a finished course is not what this route is for. A continuation takes minutes, so it accepts the same Prefer: respond-async (or async=1) ask as /courses/generate and answers 202 with the job id; the blocking envelope is the default.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "Prefer", "in": "header", "required": false, "schema": {"type": "string"}, "description": "Send a Prefer header of 'respond-async' (or pass async=1) to get a 202 with a job id instead of waiting for the envelope."}
        ],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "202": {
            "description": "The continuation was accepted and runs in the background. Poll GET /generation/{id}.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIJobSubmitted"}}}
          },
          "201": {
            "description": "The course was built and activated, exactly as a default run would.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "404": {
            "description": "No such course, or it belongs to another learner.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "409": {
            "description": "The course is not awaiting approval: only a course that is still being built can be continued.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "422": {
            "description": "The continuation ran and nothing was activated; the outcome names the stage that refused.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "503": {
            "description": "AI is off, the database cannot verify, or the gateway is at capacity.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          }
        }
      }
    },
    "/generation/{id}": {
      "get": {
        "operationId": "getGenerationJob",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Poll a generation that was submitted with Prefer: respond-async.",
        "description": "The body is the job's own view: a live copy of the pipeline's stages while the run is going, and the pipeline's final report once it is over. A course job also carries its chapter progress ('chapters_planned', 'chapters_verified', 'current_chapter') and 'chapters', the per-chapter report in course order. A job that belongs to another learner is reported as 404, never 403, so an id cannot be probed for existence. The stored record, not the job, is the durable thing: a course job names its course_id as soon as the outline is stored — that course reads as 'building' from GET /courses/{id} and carries the chapters that already verified — and a challenge job names its challenge_id once the challenge was stored. A refused run names neither, because it stored nothing.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {
            "description": "The job view, whatever state the run is in.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIJobResponse"}}}
          },
          "401": {
            "description": "No session.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "404": {
            "description": "No such job, or it belongs to another learner.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "AI is off; the body is an AIJobResponse with ok=false and job=null.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIJobResponse"}}}
          }
        }
      }
    },
    "/challenges/generate": {
      "post": {
        "operationId": "postChallengeGenerate",
        "tags": ["generation"],
        "summary": "Generate one challenge through the 11.1 pipeline.",
        "description": "Same contract and same statuses as /courses/generate.",
        "parameters": [{"name": "Prefer", "in": "header", "required": false, "schema": {"type": "string"}, "description": "Send a Prefer header of 'respond-async' (or pass async=1) to get a 202 with a job id instead of waiting for the envelope. The blocking envelope is the default."}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIGenerationRequest"}}}
        },
        "responses": {
          "202": {
            "description": "The run was accepted and continues in the background. Poll GET /generation/{id}.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIJobSubmitted"}}}
          },
          "201": {
            "description": "A verified artifact was activated.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "400": {
            "description": "The request itself was invalid.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "422": {
            "description": "Generation ran and nothing was activated.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          },
          "503": {
            "description": "AI is off, the database cannot verify, or the gateway is at capacity.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIOutcome"}}}
          }
        }
      }
    },
    "/challenges": {
      "get": {
        "operationId": "listChallenges",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "List stored generated challenges, newest first.",
        "responses": {
          "200": {
            "description": "Challenges that survived activation.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIChallengeList"}}}
          },
          "401": {
            "description": "No session.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/challenges/{id}": {
      "get": {
        "operationId": "getChallenge",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Read one stored challenge with the evidence that admitted it.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "The stored record, in the {ok, challenge} envelope.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIRecordResponse"}}}
          },
          "404": {
            "description": "No such challenge.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/challenges/{id}/reset": {
      "post": {
        "operationId": "resetChallenge",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Forget a generated challenge.",
        "description": "Removes the generated definition only. Learner attempts are the learner's data and are not touched.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "Removed.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/OkResponse"}}}
          },
          "404": {
            "description": "No such challenge.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/challenges/{id}/critique": {
      "post": {
        "operationId": "postCritique",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Review the learner's own query on demand.",
        "description": "The critique never contains the reference solution.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AICritiqueRequest"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "A critique.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AICritiqueResponse"}}}
          },
          "503": {
            "description": "AI is off.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/challenges/{id}/report": {
      "post": {
        "operationId": "reportDefect",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Report a defect in a challenge.",
        "description": "The claimed defect is reproduced by execution before any patch is proposed, and 'applied' stays false until the learner approves the patch.",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIDefectReport"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "A verdict, with evidence for either outcome.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIDefectVerdict"}}}
          }
        }
      }
    },
    "/challenges/{id}/patches/{patchID}": {
      "post": {
        "operationId": "decidePatch",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Approve or reject a proposed patch.",
        "description": "The approver is taken from the session, never from the body. An unapproved patch is refused with 409.",
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "patchID", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIPatchDecision"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "The decision was applied.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AIPatchResult"}}}
          },
          "409": {
            "description": "The patch was not approved.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/tutor": {
      "post": {
        "operationId": "postTutor",
        "security": [{"bearerAuth": []}],
        "tags": ["generation"],
        "summary": "Ask for a hint about the learner's own query.",
        "description": "Tutoring sees the learner's SQL, its real error or result summary, the objective and the hint level. It never sees the reference solution.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AITutorRequest"}}}
        },
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "A hint.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AITutorResponse"}}}
          },
          "503": {
            "description": "AI is off.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/runs": {
      "get": {
        "operationId": "listRuns",
        "tags": ["generation"],
        "summary": "List the caller's generation runs, newest first.",
        "description": "Each generation run leaves a transcript: every provider message, tool call and tool result, in order, attributed to the run, the kind (challenge or course), the owner and the terminal outcome. The transcript is redacted before it is stored (a credential never reaches it) and bounded (a runaway run cannot bloat the database), so a stored run is a prefix of the conversation when the bound was hit — 'truncated' says so. This route returns summaries, newest first, capped at 100; another learner's runs are never listed.",
        "security": [{"bearerAuth": []}],
        "responses": {
          "200": {
            "description": "The caller's runs.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/RunList"}}}
          },
          "401": {
            "description": "No valid session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "No generation service (and so no transcript store) is configured.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/RunList"}}}
          }
        }
      }
    },
    "/runs/{id}": {
      "get": {
        "operationId": "getRun",
        "tags": ["generation"],
        "summary": "Read one generation run's transcript.",
        "description": "The whole stored conversation for one run: every message, tool call and tool result in order, with the run's kind, owner, status and error. The owner is the session's account and is not carried in the body. A run belonging to another account is reported 404, never 403, so an id cannot be probed for existence.",
        "security": [{"bearerAuth": []}],
        "parameters": [
          {"name": "id", "in": "path", "required": true, "schema": {"type": "string"}, "description": "The run id: a job id from POST /courses/generate?async=1 or POST /challenges/generate?async=1, or the id minted for a blocking run."}
        ],
        "responses": {
          "200": {
            "description": "The run's transcript.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/RunTranscript"}}}
          },
          "401": {
            "description": "No valid session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "404": {
            "description": "No such run, or it belongs to another account.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "503": {
            "description": "No generation service (and so no transcript store) is configured.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/auth/signup": {
      "post": {
        "operationId": "signup",
        "tags": ["auth"],
        "summary": "Create a learner account and start a session.",
        "description": "Public signups always receive the learner role; admin is not self-service.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/SignupRequest"}}}
        },
        "responses": {
          "201": {
            "description": "Account created and signed in.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AuthResponse"}}}
          },
          "400": {
            "description": "Email, name or password failed validation.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "409": {
            "description": "That email is already registered (AUTH_EMAIL_TAKEN).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/auth/login": {
      "post": {
        "operationId": "login",
        "tags": ["auth"],
        "summary": "Exchange credentials for a session token.",
        "description": "An unknown email and a wrong password return the same 401 so the endpoint cannot enumerate accounts. Repeated failures from one address against one account are refused with 429 AUTH_RATE_LIMITED (bounded per (address, email); a success clears the count).",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/LoginRequest"}}}
        },
        "responses": {
          "200": {
            "description": "Signed in.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/AuthResponse"}}}
          },
          "400": {
            "description": "Malformed request body.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "401": {
            "description": "Email or password is incorrect (AUTH_INVALID_CREDENTIALS).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "429": {
            "description": "Too many failed sign-in attempts for this address and account (AUTH_RATE_LIMITED). The Retry-After header carries whole seconds until the window ends. The body does not distinguish a known from an unknown address.",
            "headers": {"Retry-After": {"description": "Whole seconds until the fixed window ends.", "schema": {"type": "integer"}}},
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/auth/logout": {
      "post": {
        "operationId": "logout",
        "tags": ["auth"],
        "summary": "Revoke the caller's session token.",
        "security": [{"bearerAuth": []}],
        "responses": {
          "401": {
            "description": "No session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          },
          "200": {
            "description": "Token revoked (an anonymous call is also a no-op 200).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/OkResponse"}}}
          }
        }
      }
    },
    "/auth/me": {
      "get": {
        "operationId": "me",
        "tags": ["auth"],
        "summary": "Return the account behind the bearer token.",
        "security": [{"bearerAuth": []}],
        "responses": {
          "200": {
            "description": "The signed-in account.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/MeResponse"}}}
          },
          "401": {
            "description": "No valid session (AUTH_REQUIRED).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ErrorEnvelope"}}}
          }
        }
      }
    },
    "/openapi.json": {
      "get": {
        "operationId": "getOpenAPI",
        "tags": ["system"],
        "summary": "This OpenAPI document.",
        "responses": {
          "200": {
            "description": "The OpenAPI 3.1 document for this gateway.",
            "content": {"application/json": {"schema": {"type": "object"}}}
          }
        }
      }
    },
    "/docs": {
      "get": {
        "operationId": "getDocs",
        "tags": ["system"],
        "summary": "Swagger UI that renders this OpenAPI document.",
        "responses": {
          "200": {
            "description": "An HTML page that loads /openapi.json.",
            "content": {"text/html": {"schema": {"type": "string"}}}
          }
        }
      }
    }
  },
  "components": {
    "securitySchemes": {
      "bearerAuth": {
        "type": "http",
        "scheme": "bearer",
        "description": "Opaque session token from /auth/login or /auth/signup, sent in the Authorization header as a Bearer token."
      }
    },
    "schemas": {
      "User": {
        "type": "object",
        "required": ["id", "email", "name", "role", "createdAt"],
        "properties": {
          "id": {"type": "string"},
          "email": {"type": "string", "format": "email"},
          "name": {"type": "string"},
          "role": {"type": "string", "enum": ["admin", "learner"]},
          "createdAt": {"type": "string", "format": "date-time"}
        }
      },
      "SignupRequest": {
        "type": "object",
        "required": ["email", "name", "password"],
        "properties": {
          "email": {"type": "string", "format": "email"},
          "name": {"type": "string"},
          "password": {"type": "string", "minLength": 8}
        }
      },
      "LoginRequest": {
        "type": "object",
        "required": ["email", "password"],
        "properties": {
          "email": {"type": "string", "format": "email"},
          "password": {"type": "string"}
        }
      },
      "AuthResponse": {
        "type": "object",
        "required": ["ok", "user", "token", "expiresAt"],
        "properties": {
          "ok": {"type": "boolean"},
          "user": {"$ref": "#/components/schemas/User"},
          "token": {"type": "string"},
          "expiresAt": {"type": "string", "format": "date-time"}
        }
      },
      "MeResponse": {
        "type": "object",
        "required": ["ok", "user"],
        "properties": {
          "ok": {"type": "boolean"},
          "user": {"$ref": "#/components/schemas/User"}
        }
      },
      "Column": {
        "type": "object",
        "required": ["name", "type"],
        "properties": {
          "name": {"type": "string"},
          "type": {"type": "string", "description": "PostgreSQL type name, e.g. int8 or text."}
        }
      },
      "QueryError": {
        "type": "object",
        "required": ["code", "message"],
        "properties": {
          "code": {
            "type": "string",
            "description": "Stable error code.",
            "enum": [
              "EMPTY_QUERY",
              "QUERY_SYNTAX_ERROR",
              "QUERY_RUNTIME_ERROR",
              "QUERY_TIMEOUT",
              "RESULT_LIMIT_EXCEEDED",
              "ENVIRONMENT_UNAVAILABLE",
              "DIALECT_MISMATCH",
              "PERSISTENCE_FAILED",
              "EVALUATION_ERROR"
            ]
          },
          "message": {"type": "string"},
          "detail": {"type": ["string", "null"]}
        }
      },
      "QueryResult": {
        "type": "object",
        "description": "Matches contracts/query-result.schema.json exactly.",
        "required": ["status", "columns", "rows", "row_count", "truncated", "duration_ms", "error", "engine", "dialect"],
        "properties": {
          "status": {"type": "string", "enum": ["succeeded", "failed"]},
          "columns": {"type": "array", "items": {"$ref": "#/components/schemas/Column"}},
          "rows": {"type": "array", "items": {"type": "array", "items": {}}},
          "row_count": {"type": "integer"},
          "truncated": {"type": "boolean"},
          "duration_ms": {"type": "number"},
          "error": {"oneOf": [{"$ref": "#/components/schemas/QueryError"}, {"type": "null"}]},
          "request_id": {"type": ["string", "null"]},
          "engine": {"type": "string"},
          "dialect": {
            "type": "string",
            "enum": ["postgresql", "sqlite"],
            "description": "The machine-readable dialect of the engine that produced this result. A chapter declares one dialect; a result from the other is refused with DIALECT_MISMATCH rather than graded."
          }
        }
      },
      "Limits": {
        "type": "object",
        "description": "Per-request caps; zero values fall back to the server defaults.",
        "properties": {
          "max_rows": {"type": "integer"},
          "max_bytes": {"type": "integer"},
          "timeout_ms": {"type": "integer"}
        }
      },
      "QueryRequest": {
        "type": "object",
        "required": ["sql"],
        "properties": {
          "sql": {"type": "string"},
          "dialect": {"type": "string", "enum": ["postgresql", "sqlite"], "description": "The dialect to run the statement on. Absent means postgresql, the default dialect (ADR 0004), so an existing client is unchanged. A dialect this gateway does not serve is refused with DIALECT_MISMATCH naming the dialects it does serve."},
          "dataset": {"type": "string", "description": "The fixture to run against: commerce (the shared read-only dataset, or the caller's own workspace when signed in) or perf (the larger plan fixture, always in the caller's own writable environment, so a CREATE INDEX cannot touch the challenge workspace). Absent, \"\", and \"commerce\" all mean commerce. A request that carries exercise.perf and names no dataset also routes to perf. The perf dataset is a PostgreSQL lab, so it is refused with DIALECT_MISMATCH for another dialect; an unknown dataset is refused with ENVIRONMENT_UNAVAILABLE naming the datasets this gateway serves."},
          "limits": {"$ref": "#/components/schemas/Limits"}
        }
      },
      "Exercise": {
        "type": "object",
        "description": "The subset of ExerciseSpec the evaluator needs. The result contract is accepted as either result_contract or the client's contract alias.",
        "properties": {
          "id": {"type": "string"},
          "version": {"type": "integer"},
          "lesson_id": {"type": "string"},
          "type": {"type": "string"},
          "prompt": {"type": "string"},
          "setup": {
            "type": "object",
            "properties": {
              "dataset": {"type": "string"},
              "schema_version": {"type": "integer"},
              "seed_version": {"type": "integer"},
              "dialect": {"type": "string", "enum": ["postgresql", "sqlite"], "description": "The dialect this exercise's contract was written for."}
            }
          },
          "result_contract": {"$ref": "#/components/schemas/ResultContract"},
          "contract": {"$ref": "#/components/schemas/ResultContract", "description": "Alias for result_contract; the web client sends this form."},
          "reference_solution": {"type": "string"},
          "solution": {"type": "string"},
          "hints": {"type": "array", "items": {"type": "string"}},
          "difficulty": {"type": "string"},
          "result": {"$ref": "#/components/schemas/ExpectedResult"},
          "expected_result": {"$ref": "#/components/schemas/ExpectedResult"},
          "expected_rows": {"type": "array", "items": {"type": "array", "items": {}}},
          "checks": {"type": "array", "items": {"type": "object"}}
        }
      },
      "ResultContract": {
        "type": "object",
        "properties": {
          "columns": {"type": "array", "items": {"type": "string"}},
          "column_order": {"type": "string", "enum": ["exact", "ignored"]},
          "row_order": {"type": "string", "enum": ["exact", "ignored"]},
          "duplicates": {"type": "string", "enum": ["preserved", "ignored"]},
          "numeric_tolerance": {"type": "number"}
        }
      },
      "ExpectedResult": {
        "type": "object",
        "properties": {
          "columns": {"type": "array", "items": {"type": "string"}},
          "rows": {"type": "array", "items": {"type": "array", "items": {}}}
        }
      },
      "EvaluateRequest": {
        "type": "object",
        "required": ["sql", "exercise"],
        "properties": {
          "sql": {"type": "string"},
          "dialect": {"type": "string", "enum": ["postgresql", "sqlite"], "description": "The dialect to run and grade the answer on. Absent means postgresql. Grading a SQLite answer against a PostgreSQL contract (or the reverse) is refused with DIALECT_MISMATCH rather than silently graded."},
          "dataset": {"type": "string", "description": "The fixture the answer is graded against: commerce (the shared read-only dataset, or the caller's own workspace when signed in) or perf (the larger plan fixture, always in the caller's own writable environment). Absent, \"\", and \"commerce\" all mean commerce; exercise.perf with no dataset also routes to perf. This is what a plans-course exercise needs: its authored result was written against perf rows, so grading it on commerce would compare the answer to the wrong table."},
          "limits": {"$ref": "#/components/schemas/Limits"},
          "exercise": {"$ref": "#/components/schemas/Exercise"}
        }
      },
      "CheckResult": {
        "type": "object",
        "properties": {
          "label": {"type": "string"},
          "code": {
            "type": "string",
            "description": "Feedback code for this check; the gateway clamps unknown codes to VALUE_MISMATCH.",
            "enum": ["VALUE_MISMATCH", "COLUMN_MISMATCH", "COLUMN_ORDER_MISMATCH", "ROW_COUNT_MISMATCH", "ROW_ORDER_MISMATCH", "FILTER_MISSING", "AGGREGATION_MISSING", "GROUPING_MISSING", "JOIN_MISSING", "JOIN_TYPE_MISMATCH", "CTE_MISSING", "WINDOW_MISSING", "PARTITION_MISSING", "WINDOW_ORDER_MISSING", "RANK_FUNCTION_MISMATCH", "GRAIN_ERROR", "EXPLANATION_MISSING", "EVALUATION_ERROR"]
          },
          "ok": {"type": "boolean"}
        }
      },
      "EvaluateResponse": {
        "type": "object",
        "properties": {
          "qr": {"$ref": "#/components/schemas/QueryResult"},
          "checks": {"type": "array", "items": {"$ref": "#/components/schemas/CheckResult"}},
          "passed": {"type": "boolean"},
          "duration_ms": {"type": "string"}
        }
      },
      "Environment": {
        "type": "object",
        "description": "The caller's learner workspace (ADR 0003). exists and writable are always present; the rest are facts read from the environment when it exists.",
        "required": ["exists", "writable"],
        "properties": {
          "account": {"type": "string"},
          "database": {"type": "string", "description": "The environment database name; derived from the account id, never interpolated from it."},
          "exists": {"type": "boolean", "description": "Whether the caller's environment database currently exists."},
          "writable": {"type": "boolean", "description": "Whether a learner statement may write. False is correct for the anonymous shared dataset."},
          "tables": {"type": "integer", "description": "Tables in the dataset schema, present only when the environment exists."},
          "created_at": {"type": "string", "format": "date-time"},
          "reset_at": {"type": "string", "format": "date-time"},
          "idle_until": {"type": "string", "format": "date-time", "description": "When an idle environment is reaped if it goes unused."}
        }
      },
      "EnvironmentResponse": {
        "type": "object",
        "required": ["ok", "environment"],
        "properties": {
          "ok": {"type": "boolean"},
          "environment": {"$ref": "#/components/schemas/Environment"}
        }
      },
      "EnvironmentResetResponse": {
        "type": "object",
        "required": ["ok", "duration_ms", "setup_statements", "environment"],
        "properties": {
          "ok": {"type": "boolean"},
          "duration_ms": {"type": "number", "description": "Measured end-to-end reset time in milliseconds."},
          "setup_statements": {"type": "integer", "description": "How many of the named course's setup statements were applied. 0 when the request named no course; the reset fails instead of reporting a partial application."},
          "environment": {"$ref": "#/components/schemas/Environment"}
        }
      },
      "HealthResponse": {
        "type": "object",
        "properties": {
          "status": {"type": "string"},
          "engine": {"type": "string", "description": "The dialect of the executor that answers /query with no dialect field: the default dialect."},
          "version": {"type": "string"},
          "dialects": {"type": "array", "items": {"type": "string", "enum": ["postgresql", "sqlite"]}, "description": "Every dialect this gateway can route a statement to, in stable order. A probe reads this rather than assuming one engine."},
          "admin_password_empty": {"type": "boolean", "description": "True only when the seeded default administrator can be signed in with an empty password (a loopback-only local-development install). The sign-in screen shows its local-admin hint for this boolean and no other reason. It is a boolean, never a password or a hash."}
        }
      },
      "CapabilitiesResponse": {
        "type": "object",
        "properties": {
          "hard_timeout": {"type": "boolean"},
          "explain": {"type": "boolean"},
          "max_concurrent": {"type": "integer"},
          "isolation": {
            "type": "object",
            "description": "How a statement's writes are isolated, named for both callers because the answer differs by caller. An anonymous statement runs read-only against the shared dataset; a signed-in learner's runs in their own writable workspace (a PostgreSQL environment per ADR 0003, or their own SQLite file per ADR 0006).",
            "properties": {
              "anonymous": {"type": "string", "description": "The isolation an anonymous caller's statement runs under. Currently read_only_tx."},
              "signed_in": {"type": "string", "description": "The isolation a signed-in caller's statement runs under. Currently writable_workspace."},
              "writable": {"type": "boolean", "description": "Whether this gateway can give a signed-in learner a writable workspace at all. False means a signed-in statement still runs read-only."}
            }
          },
          "dialects": {
            "type": "array",
            "items": {"type": "string", "enum": ["postgresql", "sqlite"]},
            "description": "The dialects this gateway can run, in stable order. PostgreSQL is always present and is the default. A client disables a chapter whose dialect is missing from this list and names the one action that fixes it."
          },
          "writable": {
            "type": "boolean",
            "description": "Whether this gateway can give a signed-in learner a writable workspace: a per-account PostgreSQL environment (ADR 0003) or a per-account SQLite file. It says nothing about the caller: a client combines it with whether it holds a session to decide if EXPLAIN ANALYZE and DDL are available."
          },
          "ai": {"$ref": "#/components/schemas/AIAvailability"},
          "plan": {"$ref": "#/components/schemas/PlanCapability"}
        }
      },
      "PlanCapability": {
        "type": "object",
        "description": "What the M5 plan lab offers, so a client can hide the Explain button instead of probing for a failure. 'available' is false when the gateway has no database; 'analyze_requires_auth' is true because EXPLAIN ANALYZE executes the statement (ADR 0003).",
        "properties": {
          "available": {"type": "boolean"},
          "reason": {"type": "string"},
          "datasets": {"type": "array", "items": {"type": "string"}},
          "analyze_requires_auth": {"type": "boolean"},
          "timing_precision": {"type": "array", "items": {"type": "string"}},
          "node_cap": {"type": "integer"},
          "seq_scan_large_rows": {"type": "number"}
        }
      },
      "PlanRequest": {
        "type": "object",
        "description": "The POST /explain body. 'exercise.perf' is the PerfContract from contracts/perf-contract.schema.json; the gateway reads its 'engine' to refuse a chapter authored for another dialect, and the plan is graded against the rest of it in the browser and in backend/internal/perf.",
        "required": ["sql"],
        "properties": {
          "sql": {"type": "string"},
          "analyze": {"type": "boolean", "description": "False (the default) only plans. True runs EXPLAIN ANALYZE and requires a writable environment."},
          "dialect": {"type": "string", "enum": ["postgresql", "sqlite"], "description": "The dialect the plan is for. Absent means postgresql. The plan lab captures PostgreSQL plans only (ADR 0004: SQLite has no EXPLAIN ANALYZE), so another dialect is refused with EVALUATION_ERROR and a sentence naming PostgreSQL."},
          "dataset": {"type": "string", "description": "commerce (the shared read-only dataset, or the caller's own workspace when signed in) or perf (the larger plan fixture, always in the caller's own environment). Absent, \"\", and \"commerce\" all mean commerce; exercise.perf with no dataset also routes to perf."},
          "exercise": {
            "type": "object",
            "properties": {
              "perf": {"type": "object", "description": "A PerfContract (contracts/perf-contract.schema.json)."}
            }
          },
          "limits": {"$ref": "#/components/schemas/Limits"}
        }
      },
      "PlanBundle": {
        "type": "object",
        "description": "The plan result described by contracts/plan.schema.json, the single source of truth for this shape; the summary below names the fields a client keys off rather than restating the contract.",
        "required": ["status", "analyze", "timing_precision", "engine", "plan", "summary", "environment", "warnings", "duration_ms", "error"],
        "properties": {
          "status": {"enum": ["succeeded", "failed"]},
          "analyze": {"type": "boolean"},
          "timing_precision": {"enum": ["analyze", "estimate", "none"]},
          "engine": {"type": "string"},
          "plan": {"type": ["object", "null"]},
          "summary": {"type": "object"},
          "environment": {"type": "object"},
          "warnings": {"type": "array", "items": {"type": "object"}},
          "duration_ms": {"type": "number"},
          "statement": {"type": ["string", "null"]},
          "request_id": {"type": ["string", "null"]},
          "error": {"type": ["object", "null"]}
        }
      },
      "AIAvailability": {
        "type": "object",
        "description": "Whether the AI lab can run, and why not when it cannot. Clients hide the lab when 'available' is false and show 'reason' instead.",
        "properties": {
          "configured": {"type": "boolean"},
          "available": {"type": "boolean"},
          "provider": {"type": "string"},
          "model": {"type": "string"},
          "deterministic": {"type": "boolean"},
          "reason": {"type": "string"},
          "timeout_ms": {"type": "integer", "minimum": 1}
        }
      },
      "AIError": {
        "type": "object",
        "properties": {
          "code": {"type": "string"},
          "stage": {"type": "string"},
          "message": {"type": "string"},
          "detail": {"type": "string"}
        }
      },
      "AIGenerationRequest": {
        "type": "object",
        "properties": {
          "kind": {"enum": ["challenge", "course"]},
          "title": {"type": "string"},
          "topics": {"type": "array", "items": {"type": "string"}},
          "difficulty": {"enum": ["beginner", "intermediate", "advanced"]},
          "scenario": {"type": "string"},
          "constraints": {"type": "array", "items": {"type": "string"}},
          "exercise_count": {"type": "integer"},
          "steps": {"type": "array", "items": {"type": "string"}},
          "require_approval": {"type": "boolean", "description": "Course only. When true the run stops after the outline is validated and stored: the response status is 'awaiting_approval' and POST /courses/{id}/build continues it. Off (and absent) by default, which is a single request that runs outline into chapters."}
        }
      },
      "AIStage": {
        "type": "object",
        "properties": {
          "name": {"type": "string"},
          "state": {"enum": ["done", "failed", "skipped"]},
          "detail": {"type": "string"},
          "ms": {"type": "number"}
        }
      },
      "AIAttempt": {
        "type": "object",
        "properties": {"number": {"type": "integer"}, "stage": {"type": "string"}, "detail": {"type": "string"}}
      },
      "AIEvidence": {
        "type": "object",
        "description": "Measured facts from the run. Zero means the check did not run or found nothing.",
        "properties": {
          "reference_rows": {"type": "integer"},
          "cross_check_rows": {"type": "integer"},
          "fixtures_rejected": {"type": "integer"},
          "fixtures_total": {"type": "integer"},
          "repairs": {"type": "integer"},
          "scratch_database": {"type": "string"}
        }
      },
      "AIResultContract": {
        "type": "object",
        "properties": {
          "columns": {"type": "array", "items": {"type": "string"}},
          "column_order": {"enum": ["exact", "ignored"]},
          "row_order": {"enum": ["exact", "ignored"]},
          "duplicates": {"enum": ["preserved", "ignored"]},
          "numeric_tolerance": {"type": "number"}
        }
      },
      "AISetupPlan": {
        "type": "object",
        "properties": {
          "dataset": {"type": "string"},
          "schema_version": {"type": "integer"},
          "seed_version": {"type": "integer"},
          "statements": {"type": "array", "items": {"type": "string"}}
        }
      },
      "AIProvenance": {
        "type": "object",
        "properties": {
          "provider": {"type": "string"},
          "model": {"type": "string"},
          "provider_version": {"type": "string"},
          "pipeline_version": {"type": "string"},
          "evaluator_version": {"type": "string"},
          "dataset_version": {"type": "integer"},
          "created_at": {"type": "string"},
          "repairs": {"type": "integer"}
        }
      },
      "AIArtifact": {
        "type": "object",
        "description": "A challenge the service has verified. Only ever written to the active set after every pipeline stage passed.",
        "properties": {
          "id": {"type": "string"},
          "version": {"type": "integer"},
          "lesson_id": {"type": "string"},
          "dialect": {"enum": ["postgresql"]},
          "type": {"type": "string"},
          "title": {"type": "string"},
          "prompt": {"type": "string"},
          "difficulty": {"enum": ["beginner", "intermediate", "advanced"]},
          "objectives": {"type": "array", "items": {"type": "string"}},
          "hints": {"type": "array", "items": {"type": "string"}},
          "setup": {"$ref": "#/components/schemas/AISetupPlan"},
          "reference_solution": {"type": "string"},
          "cross_check_solution": {"type": "string"},
          "result_contract": {"$ref": "#/components/schemas/AIResultContract"},
          "checks": {"type": "array", "items": {"type": "object"}},
          "provenance": {"$ref": "#/components/schemas/AIProvenance"}
        }
      },
      "AIOutcome": {
        "type": "object",
        "description": "The generation envelope. 'active' is the only status that makes an artifact visible to learners. 'awaiting_approval' is a course run that stopped after storing its outline, waiting for POST /courses/{id}/build.",
        "properties": {
          "status": {"enum": ["active", "rejected", "unavailable", "failed", "awaiting_approval"]},
          "kind": {"enum": ["challenge", "course"]},
          "artifact": {"type": ["object", "null"]},
          "plan": {"oneOf": [{"type": "null"}, {"$ref": "#/components/schemas/AICoursePlan"}], "description": "Set for an active course, and for one that stopped at awaiting_approval (the outline being approved). A rejected one returns the chapter report instead of a plan, because nothing that did not verify may be presented as something a learner can start."},
          "chapters": {"type": "array", "items": {"type": "object"}, "description": "What verification measured for a course's exercises, reported for a rejection as well as an activation."},
          "stages": {"type": "array", "items": {"$ref": "#/components/schemas/AIStage"}},
          "attempts": {"type": "array", "items": {"$ref": "#/components/schemas/AIAttempt"}},
          "evidence": {"$ref": "#/components/schemas/AIEvidence"},
          "error": {"type": ["object", "null"]}
        }
      },
      "RunEntry": {
        "type": "object",
        "description": "One ordered fact from a run. 'kind' names what it is: a provider message the model saw or produced, a function call the model asked for, the tool result the gateway answered it with, or a transport/policy error. 'truncated' and 'redacted' are honest flags: content the entry clipped to fit, or a credential a scrub removed.",
        "required": ["seq", "kind", "at"],
        "properties": {
          "seq": {"type": "integer", "minimum": 0, "description": "Position in the run, starting at 0."},
          "kind": {"enum": ["message", "tool_call", "tool_result", "error"]},
          "role": {"enum": ["system", "user", "assistant", "tool"], "description": "Set for a message."},
          "content": {"type": "string"},
          "tool_call_id": {"type": "string"},
          "tool_name": {"type": "string"},
          "at": {"type": "string", "format": "date-time"},
          "ms": {"type": "number", "minimum": 0, "description": "The provider latency for the turn this entry belongs to."},
          "truncated": {"type": "boolean"},
          "redacted": {"type": "boolean"}
        }
      },
      "RunSummary": {
        "type": "object",
        "description": "A run without its entries. 'entry_count' is how many entries the run produced; a stored transcript is a prefix when 'truncated' is true.",
        "required": ["id", "kind", "status", "entry_count", "started_at", "finished_at"],
        "properties": {
          "id": {"type": "string"},
          "kind": {"enum": ["challenge", "course"]},
          "owner_id": {"type": "string"},
          "status": {"enum": ["active", "rejected", "failed", "unavailable"]},
          "entry_count": {"type": "integer", "minimum": 0},
          "bytes": {"type": "integer", "minimum": 0},
          "truncated": {"type": "boolean"},
          "error": {"oneOf": [{"type": "null"}, {"$ref": "#/components/schemas/AIError"}]},
          "started_at": {"type": "string", "format": "date-time"},
          "finished_at": {"type": "string", "format": "date-time"}
        }
      },
      "RunTranscript": {
        "type": "object",
        "description": "One generation run's stored conversation, attributed to the run, its kind, its owner, when it ran and how it ended. The owner is the session's own account and does not travel in the body.",
        "required": ["id", "kind", "status", "entries", "entry_count", "started_at", "finished_at"],
        "properties": {
          "id": {"type": "string"},
          "kind": {"enum": ["challenge", "course"]},
          "owner_id": {"type": "string"},
          "status": {"enum": ["active", "rejected", "failed", "unavailable"]},
          "entries": {"type": "array", "items": {"$ref": "#/components/schemas/RunEntry"}},
          "entry_count": {"type": "integer", "minimum": 0},
          "bytes": {"type": "integer", "minimum": 0},
          "truncated": {"type": "boolean"},
          "error": {"oneOf": [{"type": "null"}, {"$ref": "#/components/schemas/AIError"}]},
          "started_at": {"type": "string", "format": "date-time"},
          "finished_at": {"type": "string", "format": "date-time"}
        }
      },
      "RunList": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "runs": {"type": "array", "items": {"$ref": "#/components/schemas/RunSummary"}},
          "error": {"$ref": "#/components/schemas/AIError"}
        }
      },
      "RunResponse": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "run": {"$ref": "#/components/schemas/RunTranscript"}
        }
      },
      "AIRecord": {
        "type": "object",
        "properties": {
          "artifact": {"$ref": "#/components/schemas/AIArtifact"},
          "outcome": {"$ref": "#/components/schemas/AIOutcome"},
          "status": {"enum": ["active", "rejected"]},
          "owner_id": {"type": "string"},
          "created_at": {"type": "string"}
        }
      },
      "AIChallengeList": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "challenges": {"type": "array", "items": {"$ref": "#/components/schemas/AIRecord"}}
        }
      },
      "AICoursePlan": {
        "type": "object",
        "description": "A whole generated course: chapters of markdown teaching, each with verified exercises. Setup belongs to the plan, not to an exercise, because every exercise runs against the same environment.",
        "properties": {
          "id": {"type": "string"},
          "version": {"type": "integer"},
          "title": {"type": "string"},
          "subtitle": {"type": "string"},
          "level": {"type": "string"},
          "dialect": {"type": "string"},
          "objectives": {"type": "array", "items": {"type": "string"}},
          "setup": {"$ref": "#/components/schemas/AISetupPlan"},
          "chapters": {"type": "array", "items": {"$ref": "#/components/schemas/AICourseChapter"}},
          "provenance": {"$ref": "#/components/schemas/AIProvenance"}
        }
      },
      "AICourseChapter": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "n": {"type": "integer"},
          "title": {"type": "string"},
          "summary": {"type": "string"},
          "kind": {"type": "string"},
          "minutes": {"type": "integer"},
          "objectives": {"type": "array", "items": {"type": "string"}},
          "sections": {"type": "array", "items": {"$ref": "#/components/schemas/AICourseSection"}},
          "exercises": {"type": "array", "items": {"$ref": "#/components/schemas/AICourseExercise"}}
        }
      },
      "AICourseSection": {
        "type": "object",
        "description": "Authored prose. 'body' is markdown, rendered by the same renderer as the authored courses.",
        "properties": {
          "title": {"type": "string"},
          "body": {"type": "string"}
        }
      },
      "AICourseExercise": {
        "type": "object",
        "description": "One exercise of a chapter. It is verified as the challenge artifact the pipeline already understands, so the same rules apply. 'expected' is the engine's own answer, measured during verification and never read from the provider.",
        "properties": {
          "id": {"type": "string"},
          "title": {"type": "string"},
          "prompt": {"type": "string"},
          "difficulty": {"enum": ["beginner", "intermediate", "advanced"]},
          "type": {"type": "string"},
          "objectives": {"type": "array", "items": {"type": "string"}},
          "hints": {"type": "array", "items": {"type": "string"}},
          "reference_solution": {"type": "string"},
          "cross_check_solution": {"type": "string"},
          "result_contract": {"$ref": "#/components/schemas/AIResultContract"},
          "checks": {"type": "array", "items": {"type": "object"}},
          "expected": {
            "type": "object",
            "properties": {
              "columns": {"type": "array", "items": {"type": "string"}},
              "rows": {"type": "array", "items": {"type": "array"}}
            }
          }
        }
      },
      "AICourseRecord": {
        "type": "object",
        "description": "A stored course plus the outcome that admitted it. The owner is the session's account and is not carried in the body.",
        "properties": {
          "plan": {"$ref": "#/components/schemas/AICoursePlan"},
          "outcome": {"$ref": "#/components/schemas/AIOutcome"},
          "status": {"enum": ["active", "rejected"]},
          "created_at": {"type": "string"}
        }
      },
      "AICourseList": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "courses": {"type": "array", "items": {"$ref": "#/components/schemas/AICourseRecord"}},
          "error": {"$ref": "#/components/schemas/AIError"}
        }
      },
      "AICourseResponse": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "course": {"$ref": "#/components/schemas/AICourseRecord"}
        }
      },
      "AIRecordResponse": {
        "type": "object",
        "description": "The {ok, challenge} envelope, matching AICourseResponse. One resource-read shape, not two.",
        "properties": {
          "ok": {"type": "boolean"},
          "challenge": {"$ref": "#/components/schemas/AIRecord"}
        }
      },
      "AIJobStage": {
        "type": "object",
        "description": "One stage of a run as a poller sees it. 'queued' and 'running' exist only in a live view: 'done', 'failed' and 'skipped' are final, and only those three ever appear in an outcome envelope.",
        "properties": {
          "name": {"type": "string"},
          "state": {"enum": ["queued", "running", "done", "failed", "skipped"]},
          "detail": {"type": "string"},
          "ms": {"type": "number"}
        }
      },
      "AIJob": {
        "type": "object",
        "description": "A generation job. While it runs, 'stages' is a live copy of the pipeline's progress; once it finishes, 'stages' is the pipeline's own final report, so a poller and a blocking caller can never be told different things about the same run.",
        "properties": {
          "id": {"type": "string"},
          "kind": {"enum": ["challenge", "course"]},
          "status": {"enum": ["running", "active", "rejected", "unavailable", "failed", "awaiting_approval", "discarded"]},
          "stages": {"type": "array", "items": {"$ref": "#/components/schemas/AIJobStage"}},
          "attempts": {"type": "array", "items": {"$ref": "#/components/schemas/AIAttempt"}},
          "chapters": {"type": "array", "items": {"type": "object"}, "description": "The per-chapter verification report in outline order, one entry per planned chapter, in the same shape the outcome envelope carries it: 'verified', 'dropped', 'refused' or 'skipped', with the exercises the engine ran."},
          "chapters_planned": {"type": "integer", "description": "How many chapters the outline planned. A course job only."},
          "chapters_verified": {"type": "integer", "description": "How many chapters have passed the engine so far. A course job only."},
          "current_chapter": {"type": "string", "description": "The chapter the gateway has most recently started verifying. It keeps that name until the next chapter is handed in, so it says which chapter the run is on rather than flickering. A course job only."},
          "error": {"type": ["object", "null"]},
          "course_id": {"type": "string", "description": "Present once a course was stored. While the run is still writing chapters it names a 'building' course, which GET /courses/{id} already answers with the chapters that verified; it names the same course once the run activates it. A run that stopped at 'awaiting_approval' names the same building course, which is the index to approve. Absent on a refused run, which stored nothing."},
          "challenge_id": {"type": "string", "description": "Present once an active challenge was stored."},
          "created_at": {"type": "string"},
          "finished_at": {"type": "string"}
        }
      },
      "AIJobHandle": {
        "type": "object",
        "description": "The job as it is handed back at submission: the id to poll, and what the run is.",
        "properties": {
          "id": {"type": "string"},
          "kind": {"enum": ["challenge", "course"]},
          "status": {"enum": ["running"]}
        }
      },
      "AIJobSubmitted": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "job": {"$ref": "#/components/schemas/AIJobHandle"}
        }
      },
      "AIJobResponse": {
        "type": "object",
        "description": "The poll answer. 'job' is null only in the AI-off answer, where there is no job to describe.",
        "properties": {
          "ok": {"type": "boolean"},
          "job": {"oneOf": [{"type": "null"}, {"$ref": "#/components/schemas/AIJob"}]},
          "error": {"$ref": "#/components/schemas/AIError"}
        }
      },
      "AITutorRequest": {
        "type": "object",
        "properties": {
          "challenge_id": {"type": "string"},
          "sql": {"type": "string"},
          "objective": {"type": "string"},
          "error_code": {"type": "string"},
          "error_message": {"type": "string"},
          "hint_level": {"type": "integer"},
          "result_summary": {"type": "string"}
        }
      },
      "AITutorResponse": {
        "type": "object",
        "properties": {
          "message": {"type": "string"},
          "hint_level": {"type": "integer"},
          "reveals_solution": {"type": "boolean"},
          "provenance": {"$ref": "#/components/schemas/AIProvenance"}
        }
      },
      "AICritiqueRequest": {
        "type": "object",
        "properties": {"sql": {"type": "string"}, "objective": {"type": "string"}}
      },
      "AICritiqueResponse": {
        "type": "object",
        "properties": {
          "message": {"type": "string"},
          "notes": {"type": "array", "items": {"type": "string"}},
          "provenance": {"$ref": "#/components/schemas/AIProvenance"}
        }
      },
      "AIDefectReport": {
        "type": "object",
        "properties": {
          "description": {"type": "string"},
          "sql": {"type": "string"},
          "expected": {"type": "string"},
          "observed": {"type": "string"}
        }
      },
      "AIPatchProposal": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "challenge_id": {"type": "string"},
          "summary": {"type": "string"},
          "changes": {"type": "array", "items": {"type": "object"}}
        }
      },
      "AIDefectVerdict": {
        "type": "object",
        "properties": {
          "verified": {"type": "boolean"},
          "applied": {"type": "boolean"},
          "summary": {"type": "string"},
          "patch": {"type": ["object", "null"]},
          "evidence": {"$ref": "#/components/schemas/AIEvidence"},
          "error": {"type": ["object", "null"]}
        }
      },
      "AIPatchDecision": {
        "type": "object",
        "properties": {"approved": {"type": "boolean"}, "approved_by": {"type": "string"}}
      },
      "AIPatchResult": {
        "type": "object",
        "properties": {
          "applied": {"type": "boolean"},
          "patch": {"type": ["object", "null"]},
          "error": {"type": ["object", "null"]}
        }
      },
      "TablesResponse": {
        "type": "object",
        "properties": {
          "dataset": {"type": "string"},
          "version": {"type": "integer"},
          "tables": {
            "type": "array",
            "items": {
              "type": "object",
              "properties": {
                "name": {"type": "string"},
                "rows": {"type": "integer"},
                "cols": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}},
                "preview": {"type": "array", "items": {"type": "array", "items": {"type": "string"}}}
              }
            }
          },
          "rows": {"type": "integer"}
        }
      },
      "ProgressResponse": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "snapshot": {"oneOf": [{}, {"type": "null"}]}
        }
      },
      "ProgressSnapshot": {
        "description": "Any valid JSON value; persisted verbatim.",
        "oneOf": [
          {"type": "object"},
          {"type": "array"},
          {"type": "string"},
          {"type": "number"},
          {"type": "boolean"},
          {"type": "null"}
        ]
      },
      "EventRequest": {
        "type": "object",
        "required": ["id"],
        "properties": {
          "id": {"type": "string", "description": "Exercise or event identifier."},
          "kind": {"type": "string", "description": "passed, failed, accepted, error, hint or solution."},
          "datasetVersion": {"type": "integer", "description": "Content versions this attempt was made against (technical design 7.3)."},
          "exerciseVersion": {"type": "integer", "description": "Exercise version at the time of the attempt; a later bump marks the attempt as solved on an earlier version."}
        }
      },
      "EventResponse": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "id": {"type": "string"}
        }
      },
      "CourseGenerateRequest": {
        "type": "object",
        "properties": {
          "title": {"type": "string"},
          "exercises": {"type": "integer"},
          "steps": {"type": "array", "items": {"type": "string"}}
        }
      },
      "CourseGenerateResponse": {
        "type": "object",
        "properties": {
          "title": {"type": "string"},
          "chapters": {"type": "integer"},
          "exercises": {"type": "integer"},
          "datasetVersion": {"type": "integer"}
        }
      },
      "ChallengeGenerateRequest": {
        "type": "object",
        "description": "Free-form challenge generation request; the body is accepted and discarded."
      },
      "OkResponse": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"}
        }
      },
      "DiagnosticsResponse": {
        "type": "object",
        "properties": {
          "build": {"type": "string", "description": "Gateway build (git short hash), or \"dev\"."},
          "engine": {
            "type": "object",
            "properties": {
              "name": {"type": "string"},
              "ready": {"type": "boolean"},
              "reason": {"type": "string", "description": "Empty when ready; otherwise why statements cannot run."},
              "version": {"type": "string", "description": "PostgreSQL server version, empty when unavailable."},
              "pool": {
                "type": "object",
                "properties": {
                  "max_conns": {"type": "integer"},
                  "total_conns": {"type": "integer"},
                  "idle_conns": {"type": "integer"},
                  "acquired_conns": {"type": "integer"},
                  "constructing_conns": {"type": "integer"}
                }
              },
              "limits": {
                "type": "object",
                "properties": {
                  "max_rows": {"type": "integer"},
                  "max_bytes": {"type": "integer"},
                  "timeout_ms": {"type": "integer"},
                  "max_concurrent": {"type": "integer"}
                }
              }
            }
          },
          "store": {
            "type": "object",
            "properties": {
              "kind": {"type": "string", "enum": ["postgres", "sqlite", "memory"]},
              "persistent": {"type": "boolean", "description": "False when the gateway is on the in-memory store and a restart would lose accounts and progress."}
            }
          },
          "dataset": {
            "type": "object",
            "properties": {
              "schema": {"type": "string"},
              "version": {"type": "integer"}
            }
          },
          "progress": {
            "type": "object",
            "properties": {"version": {"type": "integer"}}
          },
          "learner": {
            "type": "object",
            "description": "Sandbox facts read over the learner connection. Absent when no database is configured.",
            "properties": {
              "current_user": {"type": "string"},
              "search_path": {"type": "string"},
              "database": {"type": "string"},
              "can_read_app": {"type": ["boolean", "string"], "description": "False is correct: learner SQL must not reach gateway metadata. \"unknown\" means the check could not run."},
              "unavailable": {"type": "boolean"},
              "reason": {"type": "string"}
            }
          },
          "metrics": {"type": "array", "items": {"type": "string"}, "description": "Metric names this build reports."}
        }
      },
      "ErrorEnvelope": {
        "type": "object",
        "properties": {
          "ok": {"type": "boolean"},
          "error": {
            "type": "object",
            "required": ["code", "message"],
            "properties": {
              "code": {
                "type": "string",
                "description": "Stable gateway error code. The envelope adds NOT_FOUND (unknown dataset) to the QueryResult error set.",
                "enum": ["EMPTY_QUERY", "QUERY_SYNTAX_ERROR", "QUERY_RUNTIME_ERROR", "QUERY_TIMEOUT", "RESULT_LIMIT_EXCEEDED", "ENVIRONMENT_UNAVAILABLE", "DIALECT_MISMATCH", "PERSISTENCE_FAILED", "EVALUATION_ERROR", "INVALID_REQUEST", "NOT_FOUND", "AUTH_REQUIRED", "AUTH_INVALID_INPUT", "AUTH_INVALID_CREDENTIALS", "AUTH_EMAIL_TAKEN", "AUTH_RATE_LIMITED"]
              },
              "message": {"type": "string"},
              "detail": {"type": ["string", "null"]}
            }
          }
        }
      }
    }
  }
}`

// openAPIDocument is the marshalled spec served at GET /openapi.json.
var openAPIDocument = json.RawMessage(openAPISpec)

// swaggerUIDocs is a single self-contained Swagger UI page that renders the
// document served at /openapi.json.
const swaggerUIDocs = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>Querycraft Gateway API</title>
  <link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css" />
  <style>html { box-sizing: border-box; overflow-y: scroll; } body { margin: 0; background: #fafafa; }</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: "/openapi.json",
        dom_id: "#swagger-ui",
        deepLinking: true,
        presets: [SwaggerUIBundle.presets.apis],
      });
    };
  </script>
</body>
</html>`

// handleOpenAPI serves the OpenAPI document at GET /openapi.json.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, openAPIDocument)
}

// handleDocs serves the Swagger UI page at GET /docs and GET /docs/.
func (s *Server) handleDocs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(swaggerUIDocs))
}
