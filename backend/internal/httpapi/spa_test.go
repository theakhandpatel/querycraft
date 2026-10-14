package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

// TestSPACatchAllDoesNotShadowAPI verifies that registering the SPA as the
// "GET /" catch-all leaves the more specific API patterns intact: /health and
// /openapi.json stay JSON, /docs stays the Swagger UI page, and an unknown
// route is handled by the UI (embedded SPA or placeholder) rather than by the
// JSON API.
func TestSPACatchAllDoesNotShadowAPI(t *testing.T) {
	s := New(engine.UnavailableExecutor{}, store.NewMemory(), Config{}, nil)

	t.Run("health stays JSON", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/health", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("content-type = %q, want application/json", ct)
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("health is not JSON: %v (%q)", err, w.Body.String())
		}
		if body["status"] != "ok" {
			t.Fatalf("health status = %v, want ok", body["status"])
		}
	})

	t.Run("openapi.json stays JSON", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/openapi.json", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("content-type = %q, want application/json", ct)
		}
		if !json.Valid(w.Body.Bytes()) {
			t.Fatalf("openapi.json is not valid JSON")
		}
	})

	t.Run("docs stays the Swagger page", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/docs", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("content-type = %q, want text/html", ct)
		}
		if !strings.Contains(w.Body.String(), "swagger-ui") {
			t.Fatalf("docs body does not look like Swagger UI")
		}
	})

	t.Run("root is served by the UI", func(t *testing.T) {
		w := do(t, s, http.MethodGet, "/", "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("content-type = %q, want text/html", ct)
		}
	})
}
