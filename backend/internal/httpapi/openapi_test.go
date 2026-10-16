package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/apidocs"
)

// TestDocumentedSecurityMatchesTheHandlers pins R1-F5: a route that requires a
// session must say so in the OpenAPI document, and a route that does not must
// not. Both sets come from the one routing table Handler() builds the mux
// from, so the document cannot drift from the handlers, and a route the
// document does not name fails instead of being skipped.
func TestDocumentedSecurityMatchesTheHandlers(t *testing.T) {
	var doc struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(apidocs.Document, &doc); err != nil {
		t.Fatalf("decode the OpenAPI document: %v", err)
	}

	for _, rt := range (&Server{}).routes() {
		// The SPA catch-all is a page, not an API operation: the document
		// describes the JSON surface.
		if rt.path == "/" || rt.path == "/{$}" {
			continue
		}
		// The mux also answers the trailing-slash spelling of the docs page;
		// the document names it once, at /docs.
		path := strings.TrimSuffix(rt.path, "/")
		raw, ok := doc.Paths[path][strings.ToLower(rt.method)]
		if !ok {
			t.Errorf("%s %s is served but missing from the document", rt.method, rt.path)
			continue
		}
		var body struct {
			Security  []map[string][]string      `json:"security"`
			Responses map[string]json.RawMessage `json:"responses"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("%s %s: %v", rt.method, rt.path, err)
			continue
		}
		if rt.auth == authRequired {
			if len(body.Security) == 0 {
				t.Errorf("%s %s requires a session but declares no security", rt.method, rt.path)
			}
			// A secured route must also document the 401 a client will see
			// without a token.
			if _, ok := body.Responses["401"]; !ok {
				t.Errorf("%s %s requires a session but documents no 401 response", rt.method, rt.path)
			}
			continue
		}
		if len(body.Security) > 0 {
			t.Errorf("%s %s is a public route but declares security", rt.method, rt.path)
		}
	}
}
