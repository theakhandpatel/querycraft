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
	{http.MethodPost, "/query"},
	{http.MethodPost, "/answers/evaluate"},
	{http.MethodGet, "/datasets/{id}/tables"},
	{http.MethodPost, "/auth/signup"},
	{http.MethodPost, "/auth/login"},
	{http.MethodPost, "/auth/logout"},
	{http.MethodGet, "/auth/me"},
	{http.MethodGet, "/progress/{course}"},
	{http.MethodPut, "/progress/{course}"},
	{http.MethodPost, "/events"},
	{http.MethodPost, "/courses/generate"},
	{http.MethodPost, "/challenges/generate"},
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
