// Package dialect owns the per-dialect execution facts that grading depends on:
// how an engine error becomes one of the closed QueryResult error codes, and how
// a value the engine stores is encoded into a JSON result cell.
//
// The facts are not written here. They live in a language-neutral profile under
// contracts/dialects/, so the Go gateway and the browser adapter read one file
// instead of two copies that drift — the same arrangement as the evaluator
// fixtures under contracts/fixtures/. This package is the Go reader of that
// file.
//
// The table tests in dialect_test.go are the executable half of
// docs/sqlite-dialect.md: every claim the profile makes has a case, and the
// profile is checked against the closed enum, the five SQLite affinity classes
// and the value-encoding rules.
package dialect

import (
	"encoding/json"
	"fmt"
)

// The closed QueryResult error codes, in contract order, copied from
// contracts/query-result.schema.json. They are re-declared here so this package
// can be read on its own; the strings are the contract, and
// TestErrorClassificationCodesAreClosed fails if a profile ever names anything
// outside this set.
const (
	CodeEmptyQuery             = "EMPTY_QUERY"
	CodeSyntaxError            = "QUERY_SYNTAX_ERROR"
	CodeRuntimeError           = "QUERY_RUNTIME_ERROR"
	CodeTimeout                = "QUERY_TIMEOUT"
	CodeResultLimitExceeded    = "RESULT_LIMIT_EXCEEDED"
	CodeEnvironmentUnavailable = "ENVIRONMENT_UNAVAILABLE"
	// CodeDialectMismatch is in the closed enum and is deliberately NOT an
	// engine code: the dialect gate refuses the request before any statement
	// reaches the engine. It is listed here because this file re-declares the
	// contract enum, and a list that quietly omitted a member would let a
	// profile name it and still pass the closure test.
	CodeDialectMismatch   = "DIALECT_MISMATCH"
	CodePersistenceFailed = "PERSISTENCE_FAILED"
	CodeEvaluationError   = "EVALUATION_ERROR"
)

// ClassRule is the profile's encoding rule for one storage class or special
// case: the JSON kind it becomes, the rule in one line, and the reason.
type ClassRule struct {
	JSON string `json:"json"`
	Rule string `json:"rule"`
	Note string `json:"note"`
}

// IntegerRule adds the exact-integer boundary and the rule above it.
type IntegerRule struct {
	ClassRule
	MaxSafeInteger int64     `json:"max_safe_integer"`
	BigInt         ClassRule `json:"big_int"`
}

// RealRule adds the named rule for the values JSON cannot hold.
type RealRule struct {
	ClassRule
	NonFinite NonFiniteRule `json:"non_finite"`
}

// NonFiniteRule is how NaN and the two infinities are reported.
type NonFiniteRule struct {
	NaN              string `json:"nan"`
	PositiveInfinity string `json:"positive_infinity"`
	NegativeInfinity string `json:"negative_infinity"`
	Note             string `json:"note"`
}

// BlobRule is how binary is reported: a prefix plus lowercase hex.
type BlobRule struct {
	ClassRule
	Prefix   string `json:"prefix"`
	Encoding string `json:"encoding"`
}

// ColumnTypeLabelRule is the rule for a resolved column's `type` name: the
// declared type verbatim, or a fallback for a column that never had one.
type ColumnTypeLabelRule struct {
	Source   string `json:"source"`
	Fallback string `json:"fallback"`
	Rule     string `json:"rule"`
	Note     string `json:"note"`
}

// ValueEncoding is the profile's `value_encoding` object: one rule per storage
// class the engine can report, plus the column-label rule.
type ValueEncoding struct {
	Note            string              `json:"note"`
	Integer         IntegerRule         `json:"integer"`
	Real            RealRule            `json:"real"`
	Text            ClassRule           `json:"text"`
	Blob            BlobRule            `json:"blob"`
	Null            ClassRule           `json:"null"`
	Boolean         ClassRule           `json:"boolean"`
	Date            ClassRule           `json:"date"`
	Time            ClassRule           `json:"time"`
	ColumnTypeLabel ColumnTypeLabelRule `json:"column_type_label"`
}

// AffinityRule is one line of SQLite's affinity algorithm: a class and the
// declared-type test that selects it.
type AffinityRule struct {
	Affinity string `json:"affinity"`
	When     string `json:"when"`
	Note     string `json:"note"`
}

// TypeRules is the profile's `types` object: the affinity algorithm in the
// order the engine applies it, and the affinity of each declared type this
// dataset uses.
type TypeRules struct {
	Note          string            `json:"note"`
	AffinityRules []AffinityRule    `json:"affinity_rules"`
	Declared      map[string]string `json:"declared"`
}

// MatchRule is one error-classification rule. SQLite has no SQLSTATE, so a rule
// is a message test plus the closed code the message means.
type MatchRule struct {
	Match   string `json:"match"` // prefix | contains | equals
	Pattern string `json:"pattern"`
	Code    string `json:"code"`
	Note    string `json:"note"`
}

// Profile is a dialect profile decoded from contracts/dialects/<dialect>.json.
//
// Capabilities is honest by construction: a false value is a statement that the
// engine cannot do the thing, and every key has a matching entry in
// CapabilityNotes explaining the decision.
type Profile struct {
	Dialect             string            `json:"dialect"`
	Engine              string            `json:"engine"`
	EngineLabel         string            `json:"engine_label"`
	Capabilities        map[string]bool   `json:"capabilities"`
	CapabilityNotes     map[string]string `json:"capability_notes"`
	Types               TypeRules         `json:"types"`
	ValueEncoding       ValueEncoding     `json:"value_encoding"`
	ErrorClassification []MatchRule       `json:"error_classification"`
	Source              string            `json:"source"`
}

// Decode parses a dialect profile document. It is exported so a test, an audit
// or a later adapter can read the same file this package embeds.
func Decode(data []byte) (Profile, error) {
	var p Profile
	if err := json.Unmarshal(data, &p); err != nil {
		return Profile{}, fmt.Errorf("dialect: decode profile: %w", err)
	}
	return p, nil
}
