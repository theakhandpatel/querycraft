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
    {"name": "system", "description": "Liveness and advertised capabilities."},
    {"name": "auth", "description": "Local accounts and bearer-token sessions."},
    {"name": "query", "description": "SQL execution and answer evaluation."},
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
            "description": "Statement failed; the body is still a QueryResult whose error.code is a stable code (EMPTY_QUERY, QUERY_SYNTAX_ERROR, QUERY_RUNTIME_ERROR, QUERY_TIMEOUT, RESULT_LIMIT_EXCEEDED, ENVIRONMENT_UNAVAILABLE).",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/QueryResult"}}}
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
        "summary": "Request generation of a course from a title, exercise count and outline steps.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/CourseGenerateRequest"}}}
        },
        "responses": {
          "201": {
            "description": "Course draft created.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/CourseGenerateResponse"}}}
          }
        }
      }
    },
    "/challenges/generate": {
      "post": {
        "operationId": "postChallengeGenerate",
        "tags": ["generation"],
        "summary": "Request generation of a standalone challenge.",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ChallengeGenerateRequest"}}}
        },
        "responses": {
          "201": {
            "description": "Challenge draft created.",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/OkResponse"}}}
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
        "description": "An unknown email and a wrong password return the same 401 so the endpoint cannot enumerate accounts.",
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
        "required": ["status", "columns", "rows", "row_count", "truncated", "duration_ms", "error", "engine"],
        "properties": {
          "status": {"type": "string", "enum": ["succeeded", "failed"]},
          "columns": {"type": "array", "items": {"$ref": "#/components/schemas/Column"}},
          "rows": {"type": "array", "items": {"type": "array", "items": {}}},
          "row_count": {"type": "integer"},
          "truncated": {"type": "boolean"},
          "duration_ms": {"type": "number"},
          "error": {"oneOf": [{"$ref": "#/components/schemas/QueryError"}, {"type": "null"}]},
          "request_id": {"type": ["string", "null"]},
          "engine": {"type": "string"}
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
              "seed_version": {"type": "integer"}
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
      "HealthResponse": {
        "type": "object",
        "properties": {
          "status": {"type": "string"},
          "engine": {"type": "string"},
          "version": {"type": "string"}
        }
      },
      "CapabilitiesResponse": {
        "type": "object",
        "properties": {
          "hard_timeout": {"type": "boolean"},
          "explain": {"type": "boolean"},
          "max_concurrent": {"type": "integer"},
          "isolation": {"type": "string"}
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
                "enum": ["EMPTY_QUERY", "QUERY_SYNTAX_ERROR", "QUERY_RUNTIME_ERROR", "QUERY_TIMEOUT", "RESULT_LIMIT_EXCEEDED", "ENVIRONMENT_UNAVAILABLE", "PERSISTENCE_FAILED", "EVALUATION_ERROR", "NOT_FOUND", "AUTH_REQUIRED", "AUTH_INVALID_INPUT", "AUTH_INVALID_CREDENTIALS", "AUTH_EMAIL_TAKEN"]
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
