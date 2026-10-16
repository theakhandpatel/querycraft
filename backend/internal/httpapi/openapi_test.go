package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// openAPIOperations is the complete set of method+path pairs Handler() serves.
var openAPIOperations = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/health"},
	{http.MethodGet, "/capabilities"},
	{http.MethodGet, "/metrics"},
	{http.MethodGet, "/diagnostics"},
	{http.MethodPost, "/query"},
	{http.MethodPost, "/explain"},
	{http.MethodPost, "/answers/evaluate"},
	{http.MethodGet, "/environment"},
	{http.MethodPost, "/environment/reset"},
	{http.MethodGet, "/datasets/{id}/tables"},
	{http.MethodPost, "/auth/signup"},
	{http.MethodPost, "/auth/login"},
	{http.MethodPost, "/auth/logout"},
	{http.MethodGet, "/auth/me"},
	{http.MethodGet, "/progress/{course}"},
	{http.MethodPut, "/progress/{course}"},
	{http.MethodPost, "/events"},
	{http.MethodPost, "/courses/generate"},
	{http.MethodGet, "/generation/{id}"},
	{http.MethodGet, "/courses"},
	{http.MethodGet, "/courses/{id}"},
	{http.MethodPost, "/courses/{id}/build"},
	{http.MethodDelete, "/courses/{id}"},
	{http.MethodPost, "/challenges/generate"},
	{http.MethodGet, "/challenges"},
	{http.MethodGet, "/challenges/{id}"},
	{http.MethodPost, "/challenges/{id}/reset"},
	{http.MethodPost, "/challenges/{id}/critique"},
	{http.MethodPost, "/challenges/{id}/report"},
	{http.MethodPost, "/challenges/{id}/patches/{patchID}"},
	{http.MethodPost, "/tutor"},
	{http.MethodGet, "/runs"},
	{http.MethodGet, "/runs/{id}"},
}

func TestOpenAPIDocument(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodGet, "/openapi.json", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Fatalf("Content-Type = %q, want json", ct)
	}

	var doc struct {
		OpenAPI string                                `json:"openapi"`
		Paths   map[string]map[string]json.RawMessage `json:"paths"`
	}
	mustDecode(t, w, &doc)

	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Fatalf("openapi = %q, want a 3.x version", doc.OpenAPI)
	}

	for _, op := range openAPIOperations {
		ops, ok := doc.Paths[op.path]
		if !ok {
			t.Errorf("paths missing %q", op.path)
			continue
		}
		if _, ok := ops[strings.ToLower(op.method)]; !ok {
			t.Errorf("paths[%q] missing method %q", op.path, op.method)
		}
	}
}

// TestDocumentedSecurityMatchesTheHandlers pins R1-F5: a route that requires a
// session must say so in the OpenAPI document, and a route that does not must
// not. Before this, only the progress/runs/auth-me routes declared security, so
// a client reading the spec could not tell that /courses, /challenges or /tutor
// need a bearer token. The expected set is asserted against the live document.
func TestDocumentedSecurityMatchesTheHandlers(t *testing.T) {
	w := do(t, newTestServer(&fakeExecutor{}), http.MethodGet, "/openapi.json", "")
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	mustDecode(t, w, &doc)

	// The routes whose handlers call requireUser (verified by reading them).
	mustBeSecured := []struct{ method, path string }{
		{"post", "/environment/reset"},
		{"get", "/courses"}, {"get", "/courses/{id}"}, {"delete", "/courses/{id}"},
		{"post", "/courses/{id}/build"},
		{"get", "/generation/{id}"},
		{"get", "/challenges"}, {"get", "/challenges/{id}"},
		{"post", "/challenges/{id}/reset"}, {"post", "/challenges/{id}/critique"},
		{"post", "/challenges/{id}/report"}, {"post", "/challenges/{id}/patches/{patchID}"},
		{"post", "/tutor"},
		{"get", "/progress/{course}"}, {"put", "/progress/{course}"}, {"post", "/events"},
		{"get", "/runs"}, {"get", "/runs/{id}"},
		{"post", "/auth/logout"}, {"get", "/auth/me"},
	}
	for _, op := range mustBeSecured {
		raw, ok := doc.Paths[op.path][op.method]
		if !ok {
			t.Errorf("%s %s: not in the document", op.method, op.path)
			continue
		}
		var body struct {
			Security []map[string][]string `json:"security"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("%s %s: %v", op.method, op.path, err)
			continue
		}
		if len(body.Security) == 0 {
			t.Errorf("%s %s requires a session but declares no security", op.method, op.path)
		}
		// A secured route must also document the 401 a client will see without a
		// token; the set that declared security but not 401 was the drift here.
		var full struct {
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(raw, &full); err == nil {
			if _, ok := full.Responses["401"]; !ok {
				t.Errorf("%s %s requires a session but documents no 401 response", op.method, op.path)
			}
		}
	}

	// The public routes must NOT claim a bearer requirement.
	publicRoutes := []struct{ method, path string }{
		{"get", "/health"}, {"get", "/capabilities"}, {"get", "/metrics"},
		{"post", "/query"}, {"post", "/answers/evaluate"}, {"post", "/explain"},
		{"get", "/environment"}, {"get", "/datasets/{id}/tables"},
		{"post", "/auth/signup"}, {"post", "/auth/login"},
	}
	for _, op := range publicRoutes {
		raw, ok := doc.Paths[op.path][op.method]
		if !ok {
			continue
		}
		var body struct {
			Security []map[string][]string `json:"security"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			continue
		}
		if len(body.Security) > 0 {
			t.Errorf("%s %s is a public route but declares security", op.method, op.path)
		}
	}
}

func TestSwaggerDocs(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	for _, path := range []string{"/docs", "/docs/"} {
		w := do(t, s, http.MethodGet, path, "")
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, w.Code)
		}
		body := strings.ToLower(w.Body.String())
		if !strings.Contains(body, "swagger-ui") {
			t.Fatalf("%s body does not reference swagger-ui", path)
		}
	}
}
