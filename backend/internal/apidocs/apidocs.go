package apidocs

import (
	_ "embed"
	"encoding/json"
	"net/http"
)

// Spec is the hand-authored OpenAPI 3.1 description of the gateway. It
// documents every endpoint served by Handler() (the /openapi.json and /docs
// routes included), the QueryResult shape shared with
// contracts/query-result.schema.json, and the stable error codes. The JSON
// lives in spec.json, embedded here so the document stays diffable.
//
//go:embed spec.json
var Spec string

// Document is the marshalled spec served at GET /openapi.json.
var Document = json.RawMessage(Spec)

// Handler serves the OpenAPI document at GET /openapi.json.
func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(Document)
}
