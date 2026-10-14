package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func get(h http.Handler, target string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
	return w
}

// Available must be safe to call before any frontend build has been embedded.
func TestAvailableDoesNotPanic(t *testing.T) {
	_ = Available() // must return a bool, never panic, with only .gitkeep present
}

func TestPlaceholderServesBuildInstructions(t *testing.T) {
	w := get(Placeholder(), "/")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html", ct)
	}
	body := w.Body.String()
	for _, want := range []string{"make build", "make web"} {
		if !strings.Contains(body, want) {
			t.Errorf("placeholder body missing %q", want)
		}
	}
}

// TestHandlerRoot exercises the SPA handler. With a real build embedded, "/"
// serves index.html; with only the placeholder there is no index, so it 404s.
func TestHandlerRoot(t *testing.T) {
	h := Handler()
	w := get(h, "/")
	if Available() {
		if w.Code != http.StatusOK {
			t.Fatalf("GET / with embedded dist: status = %d, want 200", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Fatalf("GET / content-type = %q, want text/html", ct)
		}
		return
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET / without embedded dist: status = %d, want 404", w.Code)
	}
}

// A deep client route should also serve index.html so the SPA router can
// resolve it, but only when a build is embedded.
func TestHandlerHistoryFallback(t *testing.T) {
	w := get(Handler(), "/courses/intro/lesson/3")
	if Available() {
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		return
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

func TestHandlerRejectsTraversal(t *testing.T) {
	if w := get(Handler(), "/../etc/passwd"); w.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d, want 404", w.Code)
	}
}
