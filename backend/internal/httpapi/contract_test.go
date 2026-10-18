package httpapi

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/apidocs"
	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/*
contract_test.go proves that the gateway's JSON responses honour the shared
contracts in contracts/ and the component schemas declared in apidocs.go.

contracts/ is the language-neutral seam shared with the React client. It holds
exactly three schemas:

	query-result.schema.json  -> POST /query output (success *and* error)
	evaluation.schema.json    -> POST /answers/evaluate output + feedback codes
	exercise.schema.json      -> an exercise bundle (the /answers/evaluate input)

The other six gateway endpoints have no contract file; their response shapes are
declared only in apidocs.go, so the local schemas below mirror those components.

The validator is deliberately small and dependency-free. It supports the subset
of JSON Schema the contracts actually use: type, properties, required, items,
enum, oneOf, anyOf and minimum. additionalProperties is ignored, and unknown
keywords ($schema, $id, title, description, ...) are skipped. loadContract
inlines $ref before validation, and validateSchema reports a ref node that
reaches it unresolved instead of skipping the node.
*/

// contractsDir is resolved relative to this test file's package directory
// (backend/internal/httpapi), per the task's layout.
const contractsDir = "../../../contracts"

// Sources that restate the closed error set in another language. The drift test
// reads them from disk, because a symbol list declared in the test would not see
// a constant added to the file.
const (
	dialectCodesSource = "../dialect/dialect.go"
	typesCodesSource   = "../../../web/src/types.ts"
)

// evaluateBody is a representative POST /answers/evaluate request: a valid
// exercise bundle paired with learner SQL. With no database the execution
// degrades to ENVIRONMENT_UNAVAILABLE, which is the expected behaviour.
const evaluateBody = `{
  "sql": "SELECT status, count(*) AS order_count FROM orders GROUP BY status ORDER BY status",
  "exercise": {
    "id": "ex.1",
    "version": 1,
    "lesson_id": "where",
    "type": "solve",
    "prompt": "Count orders by status.",
    "setup": {"dataset": "commerce", "schema_version": 1, "seed_version": 1, "dialect": "postgresql"},
    "result_contract": {
      "columns": ["status", "order_count"],
      "column_order": "exact",
      "row_order": "ignored",
      "duplicates": "preserved",
      "numeric_tolerance": 0
    },
    "result": {"columns": ["status", "order_count"], "rows": [["delivered", 36], ["pending", 13]]}
  }
}`

/* ---------- contract loading ---------- */

// loadContract reads a contract file and inlines every $ref it holds, so both
// the validator and the tests that navigate a schema by hand see the real
// shape. A ref left in place would match every value and let a check pass on
// nothing.
func loadContract(t *testing.T, name string) map[string]any {
	t.Helper()
	path := filepath.Join(contractsDir, name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read contract %s: %v", path, err)
	}
	return inlineContractRefs(t, name, parseSchemaObject(t, path, raw))
}

func parseSchemaObject(t *testing.T, path string, raw []byte) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("parse contract %s: %v", path, err)
	}
	return schema
}

// inlineContractRefs replaces every $ref under node with the node it names.
// Two ref forms appear in the contracts:
//
//	"query-result.schema.json" -> the whole sibling contract file
//	"#/$defs/name"             -> a node inside the document that holds the ref
//
// Sibling keywords next to a $ref are merged, as draft 2020-12 applies both. A
// ref that cannot be resolved and a ref cycle fail the test: inlining exists so
// nothing is skipped silently.
func inlineContractRefs(t *testing.T, docName string, schema map[string]any) map[string]any {
	t.Helper()
	resolved, ok := inlineRefs(t, docName, schema, map[string]bool{}).(map[string]any)
	if !ok {
		t.Fatalf("%s: the document root is not an object", docName)
	}
	return resolved
}

func inlineRefs(t *testing.T, docName string, node any, open map[string]bool) any {
	t.Helper()
	switch n := node.(type) {
	case map[string]any:
		ref, isRef := n["$ref"].(string)
		if !isRef {
			for key, value := range n {
				n[key] = inlineRefs(t, docName, value, open)
			}
			return n
		}
		key := docName + " -> " + ref
		if open[key] {
			t.Fatalf("cyclic $ref %q in %s", ref, docName)
		}
		open[key] = true
		target, targetDoc := refTarget(t, docName, ref)
		resolved := inlineRefs(t, targetDoc, target, open)
		delete(open, key)
		if len(n) == 1 {
			return resolved
		}
		// JSON Schema 2020-12 applies $ref and its sibling keywords together.
		// The contracts use siblings for annotations (description), so merge
		// the resolved node with them instead of dropping either side.
		targetMap, ok := resolved.(map[string]any)
		if !ok {
			t.Fatalf("%s: $ref %q resolves to a non-object and carries siblings", docName, ref)
		}
		merged := make(map[string]any, len(targetMap)+len(n)-1)
		for k, v := range targetMap {
			merged[k] = v
		}
		for k, v := range n {
			if k == "$ref" {
				continue
			}
			merged[k] = inlineRefs(t, docName, v, open)
		}
		return merged
	case []any:
		for i, value := range n {
			n[i] = inlineRefs(t, docName, value, open)
		}
		return n
	default:
		return node
	}
}

// refTarget loads the document and the node a $ref names, relative to the
// document that holds the ref, and reports which document the node came from.
func refTarget(t *testing.T, docName string, ref string) (any, string) {
	t.Helper()
	file, pointer := ref, ""
	if i := strings.Index(ref, "#"); i >= 0 {
		file, pointer = ref[:i], ref[i+1:]
	}
	if file == "" {
		file = docName
	}
	path := filepath.Join(contractsDir, file)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("resolve $ref %q in %s: %v", ref, docName, err)
	}
	var node any = parseSchemaObject(t, path, raw)
	for _, part := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		if part == "" {
			continue
		}
		obj, ok := node.(map[string]any)
		if !ok {
			t.Fatalf("resolve $ref %q in %s: %q is not an object", ref, docName, part)
		}
		next, present := obj[unescapePointerSegment(part)]
		if !present {
			t.Fatalf("resolve $ref %q in %s: no node at %q", ref, docName, part)
		}
		node = next
	}
	return node, file
}

// unescapePointerSegment reverses the ~0 and ~1 escaping of RFC 6901. The ~1
// replacement runs first, so a literal ~01 does not become a separator.
func unescapePointerSegment(s string) string {
	s = strings.ReplaceAll(s, "~1", "/")
	return strings.ReplaceAll(s, "~0", "~")
}

/* ---------- subset JSON Schema validator ---------- */

type contractViolation struct {
	Path     string
	Expected string
	Actual   string
}

func (v contractViolation) String() string {
	return fmt.Sprintf("%s: expected %s, got %s", v.Path, v.Expected, v.Actual)
}

func formatViolations(vs []contractViolation) string {
	lines := make([]string, 0, len(vs))
	for _, v := range vs {
		lines = append(lines, "  - "+v.String())
	}
	return strings.Join(lines, "\n")
}

// validateSchema checks value against schema and returns every violation found,
// using JSON-pointer-style paths such as "/columns/0/name".
func validateSchema(schema map[string]any, value any, path string) []contractViolation {
	if len(schema) == 0 {
		return nil // an empty schema matches any JSON value
	}
	where := path
	if where == "" {
		where = "(root)"
	}
	var out []contractViolation

	if ref, ok := schema["$ref"]; ok {
		// loadContract inlines refs, so a ref node that arrives here was never
		// resolved. Report it; skipping it would match any value.
		return []contractViolation{{where, fmt.Sprintf("resolved $ref %v", ref), "unresolved"}}
	}

	if spec, ok := schema["type"]; ok {
		if !matchesType(spec, value) {
			return []contractViolation{{where, describeTypes(spec), jsonType(value)}}
		}
	}

	if enum, ok := schema["enum"]; ok {
		if !enumContains(enum, value) {
			out = append(out, contractViolation{where, fmt.Sprintf("one of %v", enum), fmt.Sprintf("%v", value)})
		}
	}

	if subs, ok := schema["oneOf"]; ok {
		matched := 0
		for _, sub := range schemaList(subs) {
			if len(validateSchema(sub, value, path)) == 0 {
				matched++
			}
		}
		if matched != 1 {
			out = append(out, contractViolation{where, "exactly one oneOf branch to match", fmt.Sprintf("%d matched", matched)})
		}
	}

	if subs, ok := schema["anyOf"]; ok {
		matched := 0
		for _, sub := range schemaList(subs) {
			if len(validateSchema(sub, value, path)) == 0 {
				matched++
			}
		}
		if matched == 0 {
			out = append(out, contractViolation{where, "at least one anyOf branch to match", "none matched"})
		}
	}

	if obj, ok := value.(map[string]any); ok {
		if req, ok := schema["required"]; ok {
			for _, name := range stringList(req) {
				if _, present := obj[name]; !present {
					out = append(out, contractViolation{where, "required property " + name, "missing"})
				}
			}
		}
		if props, ok := schema["properties"].(map[string]any); ok {
			names := make([]string, 0, len(props))
			for name := range props {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				child, present := obj[name]
				if !present {
					continue
				}
				sub, ok := props[name].(map[string]any)
				if !ok {
					continue
				}
				out = append(out, validateSchema(sub, child, path+"/"+escapePointer(name))...)
			}
		}
	}

	if arr, ok := value.([]any); ok {
		if items, ok := schema["items"].(map[string]any); ok {
			for i, el := range arr {
				out = append(out, validateSchema(items, el, fmt.Sprintf("%s/%d", path, i))...)
			}
		}
	}

	if min, ok := schema["minimum"]; ok {
		if num, ok := asFloat(value); ok {
			if want, ok := asFloat(min); ok && num < want {
				out = append(out, contractViolation{where, fmt.Sprintf("a number >= %v", want), fmt.Sprintf("%v", num)})
			}
		}
	}

	return out
}

func matchesType(spec any, value any) bool {
	switch t := spec.(type) {
	case string:
		return typeNameMatches(t, value)
	case []any:
		for _, name := range t {
			if s, ok := name.(string); ok && typeNameMatches(s, value) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func typeNameMatches(name string, value any) bool {
	switch name {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "null":
		return value == nil
	case "number":
		_, ok := asFloat(value)
		return ok
	case "integer":
		f, ok := asFloat(value)
		return ok && f == math.Trunc(f)
	default:
		return false
	}
}

func asFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func enumContains(enum any, value any) bool {
	for _, candidate := range anyList(enum) {
		if reflect.DeepEqual(candidate, value) {
			return true
		}
	}
	return false
}

func anyList(v any) []any {
	if list, ok := v.([]any); ok {
		return list
	}
	return nil
}

func schemaList(v any) []map[string]any {
	var out []map[string]any
	for _, item := range anyList(v) {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func stringList(v any) []string {
	var out []string
	for _, item := range anyList(v) {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func describeTypes(spec any) string {
	switch t := spec.(type) {
	case string:
		return "type " + t
	case []any:
		parts := make([]string, 0, len(t))
		for _, name := range t {
			parts = append(parts, fmt.Sprintf("%v", name))
		}
		return "type " + strings.Join(parts, " or ")
	default:
		return "the declared type"
	}
}

func jsonType(value any) string {
	switch value.(type) {
	case nil:
		return "null"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, float32, int, int64, json.Number:
		return "number"
	default:
		return fmt.Sprintf("%T", value)
	}
}

func escapePointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

/* ---------- local schemas for endpoints with no contracts/ file ---------- */

func schemaObject(required []string, props map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		req := make([]any, 0, len(required))
		for _, name := range required {
			req = append(req, name)
		}
		schema["required"] = req
	}
	return schema
}

func schemaArray(items map[string]any) map[string]any {
	return map[string]any{"type": "array", "items": items}
}

func schemaString() map[string]any { return map[string]any{"type": "string"} }

// schemaEnum is the local equivalent of a JSON Schema enum.
func schemaEnum(values ...string) map[string]any {
	list := make([]any, 0, len(values))
	for _, v := range values {
		list = append(list, v)
	}
	return map[string]any{"enum": list}
}
func schemaInteger() map[string]any { return map[string]any{"type": "integer"} }
func schemaBoolean() map[string]any { return map[string]any{"type": "boolean"} }

func schemaTypes(types ...string) map[string]any {
	list := make([]any, 0, len(types))
	for _, t := range types {
		list = append(list, t)
	}
	return map[string]any{"type": list}
}

// healthSchema mirrors components.schemas.HealthResponse.
var healthSchema = schemaObject(
	[]string{"status", "engine", "version", "dialects"},
	map[string]any{
		"status":   schemaString(),
		"engine":   schemaString(),
		"version":  schemaString(),
		"dialects": schemaArray(schemaEnum("postgresql", "sqlite")),
		// Additive and non-secret: whether the seeded administrator can sign in
		// with an empty password. Optional in the schema (absent from `required`)
		// so a gateway that does not report it still validates.
		"admin_password_empty": schemaBoolean(),
	},
)

// capabilitiesSchema mirrors components.schemas.CapabilitiesResponse.
var capabilitiesSchema = schemaObject(
	[]string{"hard_timeout", "explain", "max_concurrent", "isolation"},
	map[string]any{
		"hard_timeout":   schemaBoolean(),
		"explain":        schemaBoolean(),
		"max_concurrent": schemaInteger(),
		"isolation": schemaObject(
			[]string{"anonymous", "signed_in"},
			map[string]any{
				"anonymous": schemaString(),
				"signed_in": schemaString(),
				"writable":  schemaBoolean(),
			},
		),
		"plan": schemaObject(nil, map[string]any{
			"available":             schemaBoolean(),
			"reason":                schemaString(),
			"datasets":              schemaArray(schemaString()),
			"analyze_requires_auth": schemaBoolean(),
			"timing_precision":      schemaArray(schemaString()),
			"node_cap":              schemaInteger(),
			"seq_scan_large_rows":   map[string]any{"type": "number"},
		}),
	},
)

// tablesSchema mirrors components.schemas.TablesResponse.
var tablesSchema = schemaObject(
	[]string{"dataset", "version", "tables", "rows"},
	map[string]any{
		"dataset": schemaString(),
		"version": schemaInteger(),
		"rows":    schemaInteger(),
		"tables": schemaArray(schemaObject(
			[]string{"name", "rows", "cols", "preview"},
			map[string]any{
				"name":    schemaString(),
				"rows":    schemaInteger(),
				"cols":    schemaArray(schemaArray(schemaString())),
				"preview": schemaArray(schemaArray(schemaString())),
			},
		)),
	},
)

// evaluateResponseSchema mirrors components.schemas.EvaluateResponse, reusing
// the query-result contract for the embedded qr.
func evaluateResponseSchema(qr map[string]any) map[string]any {
	return schemaObject(
		[]string{"qr", "checks", "passed", "duration_ms"},
		map[string]any{
			"qr": qr,
			"checks": schemaArray(schemaObject(
				[]string{"label", "code", "ok"},
				map[string]any{"label": schemaString(), "code": schemaString(), "ok": schemaBoolean()},
			)),
			"passed":      schemaBoolean(),
			"duration_ms": schemaTypes("string", "number"),
		},
	)
}

// progressSchema mirrors components.schemas.ProgressResponse. snapshot is any
// JSON value, so it carries an empty (permissive) schema.
var progressSchema = schemaObject(
	[]string{"ok", "snapshot"},
	map[string]any{"ok": schemaBoolean(), "snapshot": map[string]any{}},
)

// eventSchema mirrors components.schemas.EventResponse.
var eventSchema = schemaObject(
	[]string{"ok", "id"},
	map[string]any{"ok": schemaBoolean(), "id": schemaString()},
)

// aiOutcomeSchema mirrors components.schemas.AIOutcome. `active` is the only
// status that makes an artifact visible to learners, which is why the status
// enum is asserted rather than the shape of the artifact.
var aiOutcomeSchema = schemaObject(
	[]string{"status", "stages", "attempts"},
	map[string]any{
		"status":   schemaEnum("active", "rejected", "unavailable", "failed", "awaiting_approval"),
		"kind":     schemaEnum("challenge", "course"),
		"artifact": schemaTypes("object", "null"),
		"stages": schemaArray(schemaObject(
			[]string{"name", "state"},
			map[string]any{"name": schemaString(), "state": schemaEnum("done", "failed", "skipped")},
		)),
		"attempts": schemaArray(schemaObject(
			[]string{"number", "stage", "detail"},
			map[string]any{"number": schemaInteger(), "stage": schemaString(), "detail": schemaString()},
		)),
		"evidence": schemaObject(nil, map[string]any{
			"fixtures_rejected": schemaInteger(),
			"fixtures_total":    schemaInteger(),
		}),
		"error": schemaTypes("object", "null"),
	},
)

// okSchema mirrors components.schemas.OkResponse.
var okSchema = schemaObject([]string{"ok"}, map[string]any{"ok": schemaBoolean()})

/* ---------- response decoding ---------- */

func decodeResponseObject(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("response is not a JSON object: %v\nbody: %s", err, body)
	}
	return decoded
}

/* ---------- tests ---------- */

// TestEndpointsConformToSchemas drives every JSON endpoint through the real
// handler with no database and validates the decoded response against the
// schema that governs it: the contracts/*.schema.json file where one exists
// (POST /query), otherwise the apidocs.go component it is declared by.
func TestEndpointsConformToSchemas(t *testing.T) {
	queryResult := loadContract(t, "query-result.schema.json")
	planContract := loadContract(t, "plan.schema.json")

	unavailable := engine.UnavailableExecutor{}
	queryBody := `{"sql":"SELECT status, count(*) FROM orders GROUP BY 1"}`

	cases := []struct {
		name       string
		exec       engine.Executor
		method     string
		path       string
		body       string
		wantStatus int
		schema     map[string]any
		// auth marks an endpoint that only an authenticated caller may reach;
		// the harness signs a throwaway account in for those cases.
		auth  bool
		extra func(t *testing.T, decoded map[string]any)
	}{
		{
			name: "GET /health", exec: unavailable,
			method: http.MethodGet, path: "/health", wantStatus: http.StatusOK,
			schema: healthSchema,
		},
		{
			name: "GET /capabilities", exec: unavailable,
			method: http.MethodGet, path: "/capabilities", wantStatus: http.StatusOK,
			schema: capabilitiesSchema,
		},
		{
			name: "GET /datasets/{id}/tables", exec: unavailable,
			method: http.MethodGet, path: "/datasets/commerce/tables", wantStatus: http.StatusOK,
			schema: tablesSchema,
		},
		{
			name: "POST /query (no database)", exec: unavailable,
			method: http.MethodPost, path: "/query", body: queryBody, wantStatus: http.StatusBadRequest,
			schema: queryResult,
		},
		{
			name: "POST /query (succeeded)", exec: &fakeExecutor{},
			method: http.MethodPost, path: "/query", body: queryBody, wantStatus: http.StatusOK,
			schema: queryResult,
		},
		{
			// contracts/plan.schema.json governs this response, success and
			// failure alike. With no database the gateway reports
			// ENVIRONMENT_UNAVAILABLE and never invents a plan.
			name: "POST /explain (no database)", exec: unavailable,
			method: http.MethodPost, path: "/explain", body: `{"sql":"SELECT 1"}`,
			wantStatus: http.StatusBadRequest, schema: planContract,
			extra: func(t *testing.T, decoded map[string]any) {
				t.Helper()
				errObj, _ := decoded["error"].(map[string]any)
				if errObj == nil || errObj["code"] != engine.CodeEnvironmentUnavailable {
					t.Errorf("error = %v, want %s", decoded["error"], engine.CodeEnvironmentUnavailable)
				}
				if decoded["plan"] != nil {
					t.Errorf("plan = %v, want null", decoded["plan"])
				}
			},
		},
		{
			// EXPLAIN ANALYZE executes the statement, so an anonymous request
			// is refused — with a message that names the reason, and never a
			// silent downgrade to a plain EXPLAIN.
			name: "POST /explain (anonymous analyze)", exec: &planHTTPFake{plan: httpSamplePlan},
			method: http.MethodPost, path: "/explain", body: `{"sql":"SELECT 1","analyze":true}`,
			wantStatus: http.StatusBadRequest, schema: planContract,
			extra: func(t *testing.T, decoded map[string]any) {
				t.Helper()
				errObj, _ := decoded["error"].(map[string]any)
				if errObj == nil || errObj["code"] != engine.CodeEnvironmentUnavailable {
					t.Fatalf("error = %v, want %s", decoded["error"], engine.CodeEnvironmentUnavailable)
				}
				message, _ := errObj["message"].(string)
				if !strings.Contains(strings.ToLower(message), "analyze") {
					t.Errorf("message %q does not name the reason", message)
				}
				if analyze, _ := decoded["analyze"].(bool); analyze {
					t.Errorf("analyze = true; the refusal downgraded nothing but claims it analyzed")
				}
				if decoded["plan"] != nil {
					t.Errorf("plan = %v, want null", decoded["plan"])
				}
			},
		},
		{
			name: "POST /explain (succeeded)", exec: &planHTTPFake{plan: httpSamplePlan},
			method: http.MethodPost, path: "/explain", body: `{"sql":"SELECT count(*) FROM orders"}`,
			wantStatus: http.StatusOK, schema: planContract,
			extra: func(t *testing.T, decoded map[string]any) {
				t.Helper()
				if decoded["status"] != "succeeded" {
					t.Fatalf("status = %v, want succeeded", decoded["status"])
				}
				if _, ok := decoded["plan"].(map[string]any); !ok {
					t.Errorf("plan = %T, want the engine's own node tree", decoded["plan"])
				}
				if decoded["timing_precision"] != "estimate" {
					t.Errorf("timing_precision = %v, want estimate", decoded["timing_precision"])
				}
			},
		},
		{
			name: "POST /answers/evaluate", exec: unavailable,
			method: http.MethodPost, path: "/answers/evaluate", body: evaluateBody, wantStatus: http.StatusOK,
			schema: evaluateResponseSchema(queryResult),
			extra: func(t *testing.T, decoded map[string]any) {
				t.Helper()
				qr, ok := decoded["qr"]
				if !ok {
					t.Fatalf("response has no qr")
				}
				if vs := validateSchema(queryResult, qr, "/qr"); len(vs) > 0 {
					t.Errorf("embedded qr violates contracts/query-result.schema.json:\n%s", formatViolations(vs))
				}
			},
		},
		{
			name: "GET /progress/{course}", exec: unavailable,
			method: http.MethodGet, path: "/progress/sql-analytics", wantStatus: http.StatusOK,
			schema: progressSchema, auth: true,
		},
		{
			name: "POST /events", exec: unavailable,
			method: http.MethodPost, path: "/events", body: `{"id":"ex.1","kind":"passed"}`,
			wantStatus: http.StatusAccepted,
			schema:     eventSchema, auth: true,
		},
		{
			// With no generation service attached the route must say so in the
			// envelope rather than inventing a course. 503 is the transport
			// half of that answer; the body is the contract half.
			name: "POST /courses/generate (AI off)", exec: unavailable,
			method: http.MethodPost, path: "/courses/generate", body: `{"title":"SQL Joins","steps":["a","b","c"]}`,
			wantStatus: http.StatusServiceUnavailable,
			schema:     aiOutcomeSchema, auth: true,
		},
		{
			name: "POST /challenges/generate (AI off)", exec: unavailable,
			method: http.MethodPost, path: "/challenges/generate", body: `{"topics":["joins"]}`,
			wantStatus: http.StatusServiceUnavailable,
			schema:     aiOutcomeSchema, auth: true,
		},
		{
			// A generation is owner-scoped like every other write, so an
			// anonymous caller is refused before any work starts.
			name: "POST /courses/generate (anonymous)", exec: unavailable,
			method: http.MethodPost, path: "/courses/generate", body: `{"title":"SQL Joins"}`,
			wantStatus: http.StatusUnauthorized,
			schema:     map[string]any{},
		},
		{
			name: "POST /tutor (AI off)", exec: unavailable,
			method: http.MethodPost, path: "/tutor", body: `{"sql":"SELECT 1","objective":"count rows"}`,
			wantStatus: http.StatusServiceUnavailable,
			schema:     map[string]any{}, auth: true,
		},
		{
			name: "GET /challenges (AI off)", exec: unavailable,
			method: http.MethodGet, path: "/challenges",
			wantStatus: http.StatusServiceUnavailable,
			schema:     map[string]any{}, auth: true,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(tc.exec)
			token := ""
			if tc.auth {
				token = signupUser(t, srv, "contract@example.test")
			}
			w := doAs(t, srv, token, tc.method, tc.path, tc.body)
			if w.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, tc.wantStatus, w.Body.String())
			}
			decoded := decodeResponseObject(t, w.Body.Bytes())
			if vs := validateSchema(tc.schema, decoded, ""); len(vs) > 0 {
				t.Errorf("response does not match its schema:\n%s", formatViolations(vs))
			}
			if tc.extra != nil {
				tc.extra(t, decoded)
			}
		})
	}
}

// TestAnswerEvaluateAgainstEvaluationContract validates /answers/evaluate
// against the contract that contracts/README.md ties to it.
//
// evaluation.schema.json describes the /answers/evaluate response envelope
// ({qr, checks, passed, duration_ms}); an earlier version described a persisted
// Evaluation record that no endpoint ever emitted (see contracts/README.md).
// In strict mode (QC_CONTRACT_STRICT=1) any violation fails the run.
func TestAnswerEvaluateAgainstEvaluationContract(t *testing.T) {
	evaluation := loadContract(t, "evaluation.schema.json")

	w := do(t, newTestServer(engine.UnavailableExecutor{}), http.MethodPost, "/answers/evaluate", evaluateBody)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", w.Code, w.Body.String())
	}
	decoded := decodeResponseObject(t, w.Body.Bytes())

	violations := validateSchema(evaluation, decoded, "")
	if len(violations) == 0 {
		t.Log("POST /answers/evaluate conforms to contracts/evaluation.schema.json")
		return
	}

	report := formatViolations(violations)
	if os.Getenv("QC_CONTRACT_STRICT") != "" {
		t.Errorf("POST /answers/evaluate violates contracts/evaluation.schema.json:\n%s", report)
		return
	}

	t.Logf("known contract divergence — POST /answers/evaluate vs contracts/evaluation.schema.json:\n%s", report)

	// Keep the test meaningful: only the documented missing fields are allowed.
	// Any other violation means the response drifted in a new way and should fail.
	expected := map[string]bool{
		"attempt_id":        true,
		"status":            true,
		"feedback_codes":    true,
		"evaluator_version": true,
	}
	for _, v := range violations {
		if v.Path != "(root)" || !strings.HasPrefix(v.Expected, "required property ") {
			t.Errorf("unexpected contract violation: %s", v)
			continue
		}
		name := strings.TrimPrefix(v.Expected, "required property ")
		if !expected[name] {
			t.Errorf("unexpected missing required property: %s", name)
		}
	}
}

// TestEvaluationNestedErrorEnumMatchesQueryResult pins the nested qr.error.code
// enum in evaluation.schema.json to the one in query-result.schema.json.
//
// They describe the same Go type (engine.QueryResult) reached by two routes, so
// they must agree. A reviewer found them already drifted once: evaluation was
// missing DIALECT_MISMATCH, which /answers/evaluate can genuinely return, and
// the conformance test above could not catch it because its fixture never
// produces a dialect refusal. Comparing the two enums directly catches it
// whether or not a fixture happens to exercise the branch.
func TestEvaluationNestedErrorEnumMatchesQueryResult(t *testing.T) {
	// Both error schemas are `oneOf: [null, {object with properties.code.enum}]`.
	// The enum is inside the object branch, so find the branch that carries it.
	codeEnum := func(schema map[string]any, path ...string) []string {
		t.Helper()
		cur := schema
		for _, key := range path {
			next, ok := cur[key].(map[string]any)
			if !ok {
				t.Fatalf("schema path %v not found at %q", path, key)
			}
			cur = next
		}
		branches, ok := cur["oneOf"].([]any)
		if !ok {
			t.Fatalf("no oneOf at schema path %v", path)
		}
		for _, branch := range branches {
			obj, ok := branch.(map[string]any)
			if !ok {
				continue
			}
			props, ok := obj["properties"].(map[string]any)
			if !ok {
				continue
			}
			code, ok := props["code"].(map[string]any)
			if !ok {
				continue
			}
			raw, ok := code["enum"].([]any)
			if !ok {
				continue
			}
			out := make([]string, 0, len(raw))
			for _, v := range raw {
				s, _ := v.(string)
				out = append(out, s)
			}
			sort.Strings(out)
			return out
		}
		t.Fatalf("no error code enum found under schema path %v", path)
		return nil
	}

	evaluation := loadContract(t, "evaluation.schema.json")
	queryResult := loadContract(t, "query-result.schema.json")

	evalEnum := codeEnum(evaluation, "properties", "qr", "properties", "error")
	qrEnum := codeEnum(queryResult, "properties", "error")

	if strings.Join(evalEnum, ",") != strings.Join(qrEnum, ",") {
		t.Errorf("evaluation qr.error.code enum drifted from query-result:\n evaluation: %v\n query-result: %v", evalEnum, qrEnum)
	}
}

// TestEvaluationQRRefResolvesToTheQueryResultSchema proves the qr $ref is
// followed instead of skipped. An unresolved ref node matches any value, so a
// contract with a dead ref would let the conformance test pass on nothing.
func TestEvaluationQRRefResolvesToTheQueryResultSchema(t *testing.T) {
	evaluation := loadContract(t, "evaluation.schema.json")
	qr := evaluation["properties"].(map[string]any)["qr"].(map[string]any)
	if _, stillARef := qr["$ref"]; stillARef {
		t.Fatalf("loadContract left qr as an unresolved $ref: %v", qr)
	}
	if requiredOf(t, qr) == nil {
		t.Fatalf("the resolved qr carries no required list: %v", qr)
	}

	// A qr missing every query-result field must fail, or the resolution above
	// proved nothing about what the conformance test checks.
	truncated := map[string]any{
		"qr":          map[string]any{"status": "succeeded"},
		"checks":      []any{},
		"passed":      false,
		"duration_ms": "1.0",
	}
	var atQR bool
	for _, v := range validateSchema(evaluation, truncated, "") {
		if strings.HasPrefix(v.Path, "/qr") {
			atQR = true
		}
	}
	if !atQR {
		t.Fatalf("a truncated qr passed evaluation.schema.json; the qr $ref is not validated")
	}
}

// TestEvaluateRequestExerciseMatchesContract validates the exercise bundle the
// client sends with POST /answers/evaluate against exercise.schema.json.
func TestEvaluateRequestExerciseMatchesContract(t *testing.T) {
	exercise := loadContract(t, "exercise.schema.json")

	var req struct {
		Exercise map[string]any `json:"exercise"`
	}
	if err := json.Unmarshal([]byte(evaluateBody), &req); err != nil {
		t.Fatalf("decode request fixture: %v", err)
	}
	if vs := validateSchema(exercise, req.Exercise, ""); len(vs) > 0 {
		t.Errorf("request exercise violates contracts/exercise.schema.json:\n%s", formatViolations(vs))
	}
}

// TestTheRealClientExerciseShapeMatchesTheContract closes the gap the fixture
// above leaves: that test validates a hand-written body that carries setup and
// result_contract, while the shipped client (web/src/api/execution/http.ts) sends
// {id, version, lesson_id, contract, reference_solution} and none of the
// authored core. The schema used to require the authored fields, so the real
// request would have failed it — and nothing noticed, because only the fixture
// was ever validated. This pins the shape the client actually sends.
func TestTheRealClientExerciseShapeMatchesTheContract(t *testing.T) {
	exercise := loadContract(t, "exercise.schema.json")

	// The exact body execution/http.ts builds for POST /answers/evaluate.
	clientExercise := map[string]any{
		"id":        "ex.sqlite.1",
		"version":   float64(1),
		"lesson_id": "sqlite-essentials",
		"contract": map[string]any{
			"columns":           []any{"n"},
			"column_order":      "exact",
			"row_order":         "ignored",
			"duplicates":        "preserved",
			"numeric_tolerance": float64(0),
		},
		"reference_solution": "SELECT count(*) AS n FROM customers",
	}
	if vs := validateSchema(exercise, clientExercise, ""); len(vs) > 0 {
		t.Errorf("the shape the shipped client sends violates contracts/exercise.schema.json:\n%s", formatViolations(vs))
	}
}

// TestSchemaValidator exercises the validator itself so a passing contract test
// is trustworthy.
func TestSchemaValidator(t *testing.T) {
	object := schemaObject(
		[]string{"name", "count", "tags"},
		map[string]any{
			"name":  schemaString(),
			"count": schemaInteger(),
			"tags":  schemaArray(schemaString()),
		},
	)

	t.Run("valid object", func(t *testing.T) {
		value := map[string]any{"name": "ok", "count": float64(2), "tags": []any{"a", "b"}}
		if vs := validateSchema(object, value, ""); len(vs) > 0 {
			t.Fatalf("unexpected violations: %s", formatViolations(vs))
		}
	})

	t.Run("missing required", func(t *testing.T) {
		vs := validateSchema(object, map[string]any{"name": "ok"}, "")
		if len(vs) != 2 {
			t.Fatalf("want 2 violations (count, tags missing), got:\n%s", formatViolations(vs))
		}
	})

	t.Run("wrong type and item shape", func(t *testing.T) {
		value := map[string]any{"name": "ok", "count": "two", "tags": []any{"a", float64(3)}}
		vs := validateSchema(object, value, "")
		if len(vs) != 2 {
			t.Fatalf("want 2 violations (count type, tags/1 type), got:\n%s", formatViolations(vs))
		}
		if vs[0].Path != "/count" && vs[1].Path != "/count" {
			t.Fatalf("no violation reported at /count:\n%s", formatViolations(vs))
		}
		if vs[0].Path != "/tags/1" && vs[1].Path != "/tags/1" {
			t.Fatalf("no violation reported at /tags/1:\n%s", formatViolations(vs))
		}
	})

	t.Run("enum", func(t *testing.T) {
		schema := map[string]any{"enum": []any{"a", "b"}}
		if vs := validateSchema(schema, "a", ""); len(vs) > 0 {
			t.Fatalf("unexpected violations: %s", formatViolations(vs))
		}
		if vs := validateSchema(schema, "c", ""); len(vs) != 1 {
			t.Fatalf("want 1 violation, got:\n%s", formatViolations(vs))
		}
	})

	t.Run("oneOf", func(t *testing.T) {
		schema := map[string]any{"oneOf": []any{
			map[string]any{"type": "null"},
			map[string]any{"type": "object", "required": []any{"code"}},
		}}
		if vs := validateSchema(schema, nil, ""); len(vs) > 0 {
			t.Fatalf("null should match exactly one branch: %s", formatViolations(vs))
		}
		if vs := validateSchema(schema, map[string]any{"code": "X"}, ""); len(vs) > 0 {
			t.Fatalf("object should match exactly one branch: %s", formatViolations(vs))
		}
		if vs := validateSchema(schema, "x", ""); len(vs) == 0 {
			t.Fatalf("string should match no branch")
		}
	})

	t.Run("anyOf", func(t *testing.T) {
		schema := map[string]any{"anyOf": []any{
			map[string]any{"type": "string"},
			map[string]any{"type": "number"},
		}}
		if vs := validateSchema(schema, float64(1), ""); len(vs) > 0 {
			t.Fatalf("number should match one branch: %s", formatViolations(vs))
		}
		if vs := validateSchema(schema, true, ""); len(vs) == 0 {
			t.Fatalf("boolean should match no branch")
		}
	})

	t.Run("minimum", func(t *testing.T) {
		schema := map[string]any{"type": "integer", "minimum": float64(0)}
		if vs := validateSchema(schema, float64(3), ""); len(vs) > 0 {
			t.Fatalf("unexpected violations: %s", formatViolations(vs))
		}
		if vs := validateSchema(schema, float64(-1), ""); len(vs) == 0 {
			t.Fatalf("negative integer should violate minimum")
		}
	})
}

/* ---------- error-code enum consistency ---------- */

// TestTheQueryErrorEnumIsOneSetEverywhere closes the whole class of drift that
// already shipped once: the closed query-error code set is hand-copied into
// several contract files, two Go packages, the client types and the OpenAPI
// document, and each copy can fall behind independently. It reads every copy
// from disk and asserts set equality with the canonical
// query-result.schema.json, so adding a code in one place and not the others
// fails here instead of at a learner.
//
// plan.schema.json is deliberately EXCLUDED: the plan lab refuses a foreign
// dialect with EVALUATION_ERROR rather than DIALECT_MISMATCH (httpapi/plan.go),
// so its error enum is intentionally the set-minus-DIALECT_MISMATCH. That
// exception is asserted explicitly below rather than left implicit.
func TestTheQueryErrorEnumIsOneSetEverywhere(t *testing.T) {
	// The canonical set, from query-result.schema.json (oneOf[null,object]).
	canonical := enumFromOneOf(t,
		loadContract(t, "query-result.schema.json")["properties"].(map[string]any)["error"].(map[string]any))
	canonicalSet := strings.Join(canonical, ",")

	// evaluation.schema.json: nested qr.error.code (oneOf[null,object]).
	evalErr := loadContract(t, "evaluation.schema.json")["properties"].(map[string]any)["qr"].(map[string]any)["properties"].(map[string]any)["error"].(map[string]any)
	if got := enumFromOneOf(t, evalErr); strings.Join(got, ",") != canonicalSet {
		t.Errorf("evaluation.schema.json qr.error.code drifted:\n got %v\n want %v", got, canonical)
	}

	// dialect.schema.json: queryResultErrorCode is a bare enum.
	dialectErr := loadContract(t, "dialect.schema.json")["$defs"].(map[string]any)["queryResultErrorCode"].(map[string]any)
	if got := enumOf(t, dialectErr); strings.Join(got, ",") != canonicalSet {
		t.Errorf("dialect.schema.json queryResultErrorCode drifted:\n got %v\n want %v", got, canonical)
	}

	// dialect/dialect.go is the one Go declaration of the set; engine/engine.go
	// aliases it (engine imports dialect, so the reverse would be a cycle) and
	// PERSISTENCE_FAILED stays here because the gateway writes that literal
	// itself. The package's constants must equal the canonical set exactly.
	dialectCodes := goCodeConstants(t, dialectCodesSource)
	if strings.Join(dialectCodes, ",") != canonicalSet {
		t.Errorf("dialect/dialect.go Code* constants drifted from query-result.schema.json:\n got %v\n want %v", dialectCodes, canonical)
	}

	// web/src/types.ts: the QueryErrorCode values, read from the file because
	// this test cannot import TypeScript. The union and the runtime guard share
	// one QUERY_ERROR_CODES list, so this is the only statement of the set in
	// the client.
	typesCodes := typescriptQueryErrorCodes(t, typesCodesSource)
	if strings.Join(typesCodes, ",") != canonicalSet {
		t.Errorf("web/src/types.ts QueryErrorCode drifted from query-result.schema.json:\n got %v\n want %v", typesCodes, canonical)
	}

	// The exception, asserted rather than left implicit: plan.schema.json omits
	// DIALECT_MISMATCH on purpose (the plan lab refuses a foreign dialect with
	// EVALUATION_ERROR, httpapi/plan.go), so it must equal canonical minus that
	// one code — no more, no less.
	planCodes := planErrorCodes(t)
	want := make([]string, 0, len(canonical))
	for _, c := range canonical {
		if c != "DIALECT_MISMATCH" {
			want = append(want, c)
		}
	}
	if strings.Join(planCodes, ",") != strings.Join(want, ",") {
		t.Errorf("plan.schema.json error enum is neither the canonical set nor that set minus DIALECT_MISMATCH:\n got %v\n want %v", planCodes, want)
	}

	// The nested qr object's required list must match the canonical one too: the
	// enum guard alone left this unguarded, and evaluation.schema.json had already
	// drifted (it omitted `dialect` while the property existed and query-result
	// required it).
	evalQR := loadContract(t, "evaluation.schema.json")["properties"].(map[string]any)["qr"].(map[string]any)
	qrSchema := loadContract(t, "query-result.schema.json")
	gotReq := requiredOf(t, evalQR)
	wantReq := requiredOf(t, qrSchema)
	if strings.Join(sortedCopy(gotReq), ",") != strings.Join(sortedCopy(wantReq), ",") {
		t.Errorf("evaluation.schema.json qr.required drifted from query-result.required:\n got %v\n want %v", sortedCopy(gotReq), sortedCopy(wantReq))
	}

	// The OpenAPI document embeds two copies: QueryError (the QueryResult shape)
	// and ErrorEnvelope (adds NON-query codes). QueryError must equal canonical;
	// ErrorEnvelope must be a superset of it.
	var spec map[string]any
	if err := json.Unmarshal([]byte(apidocs.Spec), &spec); err != nil {
		t.Fatalf("decode the OpenAPI document: %v", err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)

	qrCode := schemas["QueryError"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)
	if got := enumOf(t, qrCode); strings.Join(got, ",") != canonicalSet {
		t.Errorf("OpenAPI QueryError.code drifted from query-result.schema.json:\n got %v\n want %v", got, canonical)
	}

	envCodes := schemas["ErrorEnvelope"].(map[string]any)["properties"].(map[string]any)["error"].(map[string]any)["properties"].(map[string]any)["code"].(map[string]any)
	envSet := map[string]bool{}
	for _, c := range enumOf(t, envCodes) {
		envSet[c] = true
	}
	for _, c := range canonical {
		if !envSet[c] {
			t.Errorf("OpenAPI ErrorEnvelope.code is missing the canonical code %s", c)
		}
	}
}

// planErrorCodes reads the plan schema's query-error enum, which lives under a
// nested error object rather than a $defs entry.
func planErrorCodes(t *testing.T) []string {
	t.Helper()
	schema := loadContract(t, "plan.schema.json")
	// Walk every object looking for the code enum; the plan schema nests it under
	// properties.error.oneOf[0].properties.code.
	var find func(v any) []string
	find = func(v any) []string {
		switch node := v.(type) {
		case map[string]any:
			if raw, ok := node["enum"].([]any); ok {
				out := make([]string, 0, len(raw))
				for _, e := range raw {
					s, _ := e.(string)
					out = append(out, s)
				}
				if containsString(out, "QUERY_SYNTAX_ERROR") {
					sort.Strings(out)
					return out
				}
			}
			for _, child := range node {
				if got := find(child); got != nil {
					return got
				}
			}
		case []any:
			for _, child := range node {
				if got := find(child); got != nil {
					return got
				}
			}
		}
		return nil
	}
	got := find(schema)
	if got == nil {
		t.Fatal("plan.schema.json has no query-error enum")
	}
	return got
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// requiredOf reads a schema's `required` array as a string slice.
func requiredOf(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	return out
}

// sortedCopy returns a sorted copy so two required lists compare by set.
func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// enumFromOneOf reads the code enum from an error schema shaped
// oneOf: [null, {properties.code.enum}].
func enumFromOneOf(t *testing.T, errSchema map[string]any) []string {
	t.Helper()
	branches, ok := errSchema["oneOf"].([]any)
	if !ok {
		t.Fatalf("error schema has no oneOf: %v", errSchema)
	}
	for _, b := range branches {
		obj, ok := b.(map[string]any)
		if !ok {
			continue
		}
		props, ok := obj["properties"].(map[string]any)
		if !ok {
			continue
		}
		code, ok := props["code"].(map[string]any)
		if !ok {
			continue
		}
		return enumOf(t, code)
	}
	t.Fatalf("no code enum under the error oneOf: %v", errSchema)
	return nil
}

// enumOf reads a sorted copy of a schema's enum.
func enumOf(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["enum"].([]any)
	if !ok {
		t.Fatalf("schema has no enum: %v", schema)
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// goCodeConstants reads the `Code* = "UPPER"` constants of a Go file, sorted.
// It reads the file instead of listing the Go symbols, so a constant added to
// the file fails the drift test.
func goCodeConstants(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	pattern := regexp.MustCompile(`(?m)^\s*Code[A-Za-z0-9]*\s*=\s*"([A-Z_]+)"`)
	var out []string
	for _, match := range pattern.FindAllStringSubmatch(string(raw), -1) {
		out = append(out, match[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s defines no Code* string constants; the pattern or the file moved", path)
	}
	sort.Strings(out)
	return out
}

// typescriptQueryErrorCodes reads the QUERY_ERROR_CODES list of web/src/types.ts.
// The list and the QueryErrorCode type derive from each other, so one parse
// covers both.
func typescriptQueryErrorCodes(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	src := string(raw)
	const marker = "export const QUERY_ERROR_CODES"
	start := strings.Index(src, marker)
	if start < 0 {
		t.Fatalf("%s has no %q declaration", path, marker)
	}
	rest := src[start+len(marker):]
	end := strings.Index(rest, "]")
	if end < 0 {
		t.Fatalf("%s: %q is not a list literal", path, marker)
	}
	var out []string
	for _, match := range regexp.MustCompile(`'([A-Z_]+)'`).FindAllStringSubmatch(rest[:end], -1) {
		out = append(out, match[1])
	}
	if len(out) == 0 {
		t.Fatalf("%s: %q lists no codes", path, marker)
	}
	sort.Strings(out)
	return out
}
