package apidocs

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	{http.MethodPost, "/environment/prepare"},
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
	{http.MethodGet, "/admin/courses"},
	{http.MethodGet, "/admin/ai"},
	{http.MethodGet, "/preferences"},
	{http.MethodPut, "/preferences"},
	{http.MethodPut, "/admin/ai"},
	{http.MethodGet, "/admin/ai/models"},
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
	w := httptest.NewRecorder()
	Handler(w, httptest.NewRequest(http.MethodGet, "/openapi.json", nil))
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
	if err := json.Unmarshal(w.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode response: %v", err)
	}

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
	for _, path := range []string{"/docs", "/docs/"} {
		w := httptest.NewRecorder()
		DocsHandler(w, httptest.NewRequest(http.MethodGet, path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want 200", path, w.Code)
		}
		body := strings.ToLower(w.Body.String())
		if !strings.Contains(body, "swagger-ui") {
			t.Fatalf("%s body does not reference swagger-ui", path)
		}
	}
}
