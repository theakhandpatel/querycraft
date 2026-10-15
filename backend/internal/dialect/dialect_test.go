package dialect

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
	"time"
)

// contractProfilePath is the source of truth this package mirrors. go:embed
// cannot reference a path outside its own package directory, so
// backend/internal/dialect/sqlite.json is a byte-identical copy and
// TestEmbeddedProfileMatchesContract is what keeps it one.
const contractProfilePath = "../../../contracts/dialects/sqlite.json"

func TestEmbeddedProfileMatchesContract(t *testing.T) {
	want, err := os.ReadFile(filepath.FromSlash(contractProfilePath))
	if err != nil {
		t.Fatalf("read %s: %v", contractProfilePath, err)
	}
	if got := string(sqliteProfileJSON); got != string(want) {
		t.Fatalf("backend/internal/dialect/sqlite.json differs from %s\n"+
			"the Go side embeds a byte-identical mirror (go:embed cannot reach outside the package directory); "+
			"copy the contract file over the mirror, never edit one alone", contractProfilePath)
	}
}

func TestNamesAndByName(t *testing.T) {
	if got, want := Names(), []string{"sqlite"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Names() = %v, want %v", got, want)
	}
	p, ok := ByName("sqlite")
	if !ok {
		t.Fatal(`ByName("sqlite") reported no profile`)
	}
	if p.Dialect != "sqlite" {
		t.Fatalf(`ByName("sqlite").Dialect = %q`, p.Dialect)
	}
	// This milestone ships one engine-error profile. PostgreSQL's arrives in a
	// later step, and until then an unknown name must say so rather than return
	// an empty profile that silently classifies everything as runtime errors.
	if _, ok := ByName("postgresql"); ok {
		t.Error(`ByName("postgresql") returned a profile, but none is shipped yet`)
	}
	if _, ok := ByName(""); ok {
		t.Error(`ByName("") returned a profile`)
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	if _, err := Decode([]byte("{")); err == nil {
		t.Fatal("Decode accepted invalid JSON")
	}
}

func TestErrorCodesAreClosed(t *testing.T) {
	want := []string{
		CodeEmptyQuery, CodeSyntaxError, CodeRuntimeError, CodeTimeout,
		CodeResultLimitExceeded, CodeEnvironmentUnavailable,
		CodeDialectMismatch,
		CodePersistenceFailed, CodeEvaluationError,
	}
	if got := ErrorCodes(); !reflect.DeepEqual(got, want) {
		t.Fatalf("ErrorCodes() = %v, want %v", got, want)
	}
}

func TestProfileShape(t *testing.T) {
	p := SQLite()

	if p.Dialect != "sqlite" {
		t.Errorf("dialect = %q, want sqlite", p.Dialect)
	}
	if p.Engine == "" || p.EngineLabel == "" {
		t.Errorf("engine = %q, engine_label = %q; both must be set (engine is the machine value, engine_label the human one)", p.Engine, p.EngineLabel)
	}
	if p.Source != "docs/sqlite-dialect.md" {
		t.Errorf("source = %q, want docs/sqlite-dialect.md", p.Source)
	}
	if len(p.ErrorClassification) == 0 {
		t.Fatal("error_classification is empty")
	}

	// Honest capabilities: what SQLite cannot do is stated, not omitted.
	falseCapabilities := []string{
		"analyze", "plan", "roles", "schemas", "sqlstate", "boolean_type",
		"date_type", "interval_type", "ilike", "date_trunc", "to_char",
		"distinct_on", "cast_operator",
	}
	for _, key := range falseCapabilities {
		got, ok := p.Capabilities[key]
		if !ok {
			t.Errorf("capabilities is missing %q", key)
			continue
		}
		if got {
			t.Errorf("capabilities[%q] = true, but SQLite cannot do it", key)
		}
	}
	trueCapabilities := []string{
		"writable", "dynamic_typing", "explain_query_plan", "window_functions",
		"recursive_cte", "filter_clause", "returning", "strict_tables",
		"right_full_join", "json1", "rowid_tables", "without_rowid",
		"autoincrement", "like_case_insensitive_ascii", "interrupt",
	}
	for _, key := range trueCapabilities {
		if got, ok := p.Capabilities[key]; !ok || !got {
			t.Errorf("capabilities[%q] = %v (present=%v), want true", key, got, ok)
		}
	}

	// Every capability carries its reason; a bare false would be an assertion
	// without an argument.
	capKeys := make([]string, 0, len(p.Capabilities))
	for k := range p.Capabilities {
		capKeys = append(capKeys, k)
	}
	sort.Strings(capKeys)
	noteKeys := make([]string, 0, len(p.CapabilityNotes))
	for k := range p.CapabilityNotes {
		noteKeys = append(noteKeys, k)
	}
	sort.Strings(noteKeys)
	if !reflect.DeepEqual(capKeys, noteKeys) {
		t.Errorf("capability_notes keys %v do not match capabilities keys %v", noteKeys, capKeys)
	}
	for _, k := range capKeys {
		if p.CapabilityNotes[k] == "" {
			t.Errorf("capability_notes[%q] is empty", k)
		}
	}

	// The affinity algorithm is the engine's, in the engine's order.
	classes := AffinityClasses()
	if len(p.Types.AffinityRules) != len(classes) {
		t.Fatalf("types.affinity_rules has %d rules, want %d (one per class)", len(p.Types.AffinityRules), len(classes))
	}
	for i, want := range classes {
		if got := p.Types.AffinityRules[i].Affinity; got != want {
			t.Errorf("types.affinity_rules[%d].affinity = %q, want %q (the order is the algorithm)", i, got, want)
		}
		if p.Types.AffinityRules[i].When == "" || p.Types.AffinityRules[i].Note == "" {
			t.Errorf("types.affinity_rules[%d] must carry both when and note", i)
		}
	}
	if p.Types.Note == "" {
		t.Error("types.note is empty")
	}
	if len(p.Types.Declared) == 0 {
		t.Error("types.declared is empty")
	}
}

func TestValueEncodingRulesAreDocumented(t *testing.T) {
	p := SQLite()
	ve := p.ValueEncoding

	rules := map[string]ClassRule{
		"integer": ve.Integer.ClassRule,
		"real":    ve.Real.ClassRule,
		"text":    ve.Text,
		"blob":    ve.Blob.ClassRule,
		"null":    ve.Null,
		"boolean": ve.Boolean,
		"date":    ve.Date,
		"time":    ve.Time,
	}
	for name, rule := range rules {
		if rule.JSON == "" || rule.Rule == "" || rule.Note == "" {
			t.Errorf("value_encoding.%s needs json, rule and note; got %+v", name, rule)
		}
	}
	if ve.Note == "" {
		t.Error("value_encoding.note is empty")
	}
	if want := int64(9007199254740991); ve.Integer.MaxSafeInteger != want {
		t.Errorf("integer.max_safe_integer = %d, want %d", ve.Integer.MaxSafeInteger, want)
	}
	if ve.Integer.BigInt.JSON != "string" || ve.Integer.BigInt.Note == "" {
		t.Errorf("integer.big_int = %+v, want a string rule with a note", ve.Integer.BigInt)
	}
	if ve.Real.NonFinite.NaN != "null" {
		t.Errorf("real.non_finite.nan = %q, want null (SQLite stores NaN as NULL)", ve.Real.NonFinite.NaN)
	}
	if ve.Real.NonFinite.PositiveInfinity == "" || ve.Real.NonFinite.NegativeInfinity == "" || ve.Real.NonFinite.Note == "" {
		t.Errorf("real.non_finite = %+v, want both infinities and a note", ve.Real.NonFinite)
	}
	if want := `\x`; ve.Blob.Prefix != want {
		t.Errorf("blob.prefix = %q, want %q", ve.Blob.Prefix, want)
	}
	if ve.Blob.Encoding != "hex-lowercase" {
		t.Errorf("blob.encoding = %q, want hex-lowercase", ve.Blob.Encoding)
	}
	ct := ve.ColumnTypeLabel
	if ct.Source != "sqlite3_column_decltype" || ct.Fallback == "" || ct.Rule == "" || ct.Note == "" {
		t.Errorf("column_type_label = %+v, want source, fallback, rule and note", ct)
	}
}

func TestErrorClassificationShape(t *testing.T) {
	p := SQLite()
	closed := map[string]bool{}
	for _, c := range ErrorCodes() {
		closed[c] = true
	}
	kinds := map[string]bool{}
	for i, rule := range p.ErrorClassification {
		if !closed[rule.Code] {
			t.Errorf("rule %d names %q, which is not in the closed QueryResult enum", i, rule.Code)
		}
		switch rule.Match {
		case "prefix", "contains", "equals":
			kinds[rule.Match] = true
		default:
			t.Errorf("rule %d has match %q, want prefix, contains or equals", i, rule.Match)
		}
		if rule.Note == "" {
			t.Errorf("rule %d (%q) has no note", i, rule.Pattern)
		}
		if i < len(p.ErrorClassification)-1 && rule.Pattern == "" {
			t.Errorf("rule %d is the empty pattern but is not last; the catch-all must not shadow a specific rule", i)
		}
	}
	for _, kind := range []string{"prefix", "contains", "equals"} {
		if !kinds[kind] {
			t.Errorf("no rule uses match %q; the profile should exercise each declared kind", kind)
		}
	}
	last := p.ErrorClassification[len(p.ErrorClassification)-1]
	if last.Match != "contains" || last.Pattern != "" || last.Code != CodeRuntimeError {
		t.Errorf("last rule = %+v, want the catch-all (contains \"\" -> %s)", last, CodeRuntimeError)
	}
}

// emittedCodes is the set of codes the classifier can return.
func emittedCodes(p Profile) map[string]bool {
	out := map[string]bool{}
	for _, rule := range p.ErrorClassification {
		out[rule.Code] = true
	}
	return out
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestReachableCodesAreTheEngineOnes pins the boundary the milestone draws: an
// engine error can be one of five codes, and the other three belong to the
// executor, not to SQLite.
func TestReachableCodesAreTheEngineOnes(t *testing.T) {
	p := SQLite()
	reachable := map[string]bool{
		CodeSyntaxError:            true,
		CodeRuntimeError:           true,
		CodeTimeout:                true,
		CodeResultLimitExceeded:    true,
		CodeEnvironmentUnavailable: true,
	}
	emitted := emittedCodes(p)
	for code := range emitted {
		if !reachable[code] {
			t.Errorf("the profile emits %q, which no SQLite primary result code can produce", code)
		}
	}
	for code := range reachable {
		if !emitted[code] {
			t.Errorf("no rule can produce %q; every reachable code needs a named producer", code)
		}
	}
	for _, code := range []string{CodeEmptyQuery, CodeDialectMismatch, CodePersistenceFailed, CodeEvaluationError} {
		if emitted[code] {
			t.Errorf("the profile emits %q; that code belongs to the executor and must never come from the engine", code)
		}
	}
	_ = sortedSet // keep the helper honest if the assertions above change shape
}

type classifyCase struct {
	name string
	msg  string
	want string
}

// classifyCases is the table of messages to codes. It carries a case for every
// SQLite primary result code the profile names, for the syntax/runtime split of
// SQLITE_ERROR(1), for both lock messages and for the catch-all;
// TestClassifyCasesCoverEveryReachableCode asserts that coverage.
var classifyCases = []classifyCase{
	// QUERY_SYNTAX_ERROR — SQLITE_ERROR(1), before the statement runs.
	{"syntax error", `near "FROM": syntax error`, CodeSyntaxError},
	{"incomplete input", "incomplete input", CodeSyntaxError},
	{"unrecognized token", `unrecognized token: "]"`, CodeSyntaxError},
	{"unterminated string", "unterminated string literal", CodeSyntaxError},
	{"unterminated comment", "unterminated /* comment", CodeSyntaxError},

	// QUERY_RUNTIME_ERROR — SQLITE_ERROR(1) after parsing, plus 19, 20, 25, 8, 23.
	{"no such table", "no such table: orderz", CodeRuntimeError},
	{"no such column", "no such column: totl", CodeRuntimeError},
	{"no such function", "no such function: DATETRUNC", CodeRuntimeError},
	{"no such module", "no such module: fts9", CodeRuntimeError},
	{"ambiguous column", "ambiguous column name: id", CodeRuntimeError},
	{"unique constraint", "UNIQUE constraint failed: customers.email", CodeRuntimeError},
	{"not null constraint", "NOT NULL constraint failed: products.name", CodeRuntimeError},
	{"check constraint", "CHECK constraint failed: quantity > 0", CodeRuntimeError},
	{"foreign key constraint", "FOREIGN KEY constraint failed", CodeRuntimeError},
	{"datatype mismatch", "datatype mismatch", CodeRuntimeError},
	{"misuse of aggregate", "misuse of aggregate: count()", CodeRuntimeError},
	{"readonly database", "attempt to write a readonly database", CodeRuntimeError},
	{"not authorized", "not authorized", CodeRuntimeError},
	{"authorization denied", "authorization denied", CodeRuntimeError},
	{"range", "column index out of range", CodeRuntimeError},
	{"catch-all", "table orders has 4 columns but 5 values were supplied", CodeRuntimeError},

	// QUERY_TIMEOUT — SQLITE_BUSY(5), SQLITE_LOCKED(6), SQLITE_INTERRUPT(9).
	{"busy", "database is locked", CodeTimeout},
	{"locked table", "database table is locked", CodeTimeout},
	{"interrupted", "interrupted", CodeTimeout},

	// RESULT_LIMIT_EXCEEDED — SQLITE_TOOBIG(18); the host caps share the code.
	{"too big", "string or blob too big", CodeResultLimitExceeded},

	// ENVIRONMENT_UNAVAILABLE — 14, 26, 11, 10, 13, 3, 7, 21.
	{"cannot open", "unable to open database file", CodeEnvironmentUnavailable},
	{"not a database", "file is not a database", CodeEnvironmentUnavailable},
	{"corrupt", "database disk image is malformed", CodeEnvironmentUnavailable},
	{"io error", "disk I/O error", CodeEnvironmentUnavailable},
	{"disk full", "database or disk is full", CodeEnvironmentUnavailable},
	{"permission", "access permission denied", CodeEnvironmentUnavailable},
	{"out of memory", "out of memory", CodeEnvironmentUnavailable},
	{"api misuse", "bad parameter or other API misuse", CodeEnvironmentUnavailable},
	{"out of sequence", "library routine called out of sequence", CodeEnvironmentUnavailable},
}

func TestClassifyError(t *testing.T) {
	p := SQLite()
	for _, c := range classifyCases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.ClassifyError(errors.New(c.msg)); got != c.want {
				t.Errorf("ClassifyError(%q) = %q, want %q", c.msg, got, c.want)
			}
			if got := ClassifyError(errors.New(c.msg)); got != c.want {
				t.Errorf("package ClassifyError(%q) = %q, want %q", c.msg, got, c.want)
			}
		})
	}
}

// TestClassifyErrorExpectsTheEnginesSentence records where the framing boundary
// is, because it is the defect this milestone already hit once: "SELECT" reached
// the learner as QUERY_RUNTIME_ERROR because the driver's wrapper, not the
// engine's sentence, was the text being matched.
//
// The profile's patterns are sqlite3_errmsg's sentences, so the sentence
// classifies as the contract says — "incomplete input" is a syntax error — while
// a driver's rendering of that same sentence does not. The adapter that knows its
// driver removes the framing before calling here (engine/sqlite.go,
// sqliteErrorOf); this package stays a pure matcher over a language-neutral file
// that the browser reads too.
func TestClassifyErrorExpectsTheEnginesSentence(t *testing.T) {
	p := SQLite()
	for _, c := range []struct {
		name string
		msg  string
		want string
	}{
		{"the engine's sentence", "incomplete input", CodeSyntaxError},
		{"the engine's sentence, token", `unrecognized token: "#"`, CodeSyntaxError},
		{"the engine's sentence, busy", "database is locked", CodeTimeout},
		{"the engine's sentence, interrupted", "interrupted", CodeTimeout},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := p.ClassifyError(errors.New(c.msg)); got != c.want {
				t.Errorf("ClassifyError(%q) = %q, want %q", c.msg, got, c.want)
			}
		})
	}

	// A framed message is the adapter's to fix, and until it is, the four rules
	// above cannot fire: this is the defect, pinned so the fix stays downstream.
	framed := "SQL logic error: incomplete input (1)"
	if got := p.ClassifyError(errors.New(framed)); got != CodeRuntimeError {
		t.Errorf("ClassifyError(%q) = %q; the profile matches the engine's sentence, so a framed message is expected to fall through to the catch-all (%s) until the adapter unwraps it", framed, got, CodeRuntimeError)
	}
}

func TestClassifyNilErrorHasNoCode(t *testing.T) {
	if got := SQLite().ClassifyError(nil); got != "" {
		t.Fatalf("ClassifyError(nil) = %q, want an empty string (no error has no code)", got)
	}
}

// TestClassifyCasesCoverEveryReachableCode makes "one case per code" a property
// of the test suite rather than a claim in a commit message: adding a code to
// the profile without a case fails here.
func TestClassifyCasesCoverEveryReachableCode(t *testing.T) {
	p := SQLite()
	emitted := emittedCodes(p)
	covered := map[string]bool{}
	for _, c := range classifyCases {
		if !emitted[c.want] {
			t.Errorf("case %q asserts %q, which the profile cannot emit", c.name, c.want)
		}
		covered[c.want] = true
	}
	for _, code := range sortedSet(emitted) {
		if !covered[code] {
			t.Errorf("no case produces %q", code)
		}
	}
}

// TestClassifyErrorNeverEmitsExecutorCodes is the case for the remaining three
// codes of the closed enum. EMPTY_QUERY is the executor's blank-SQL check and
// PERSISTENCE_FAILED and EVALUATION_ERROR are not engine errors at all, so no
// message may produce one.
func TestClassifyErrorNeverEmitsExecutorCodes(t *testing.T) {
	p := SQLite()
	executorCodes := map[string]bool{
		CodeEmptyQuery:        true,
		CodePersistenceFailed: true,
		CodeEvaluationError:   true,
	}
	emitted := emittedCodes(p)
	corpus := []string{
		"", " ", "\n", "null", "undefined", "Error: no error", "panic: runtime error",
		"query deadline exceeded", "rows capped", "syntax", "no such table",
		"UNIQUE constraint failed: products.id", "out of memory", "interrupted",
	}
	for _, c := range classifyCases {
		corpus = append(corpus, c.msg)
	}
	for _, msg := range corpus {
		got := p.ClassifyError(errors.New(msg))
		if got == "" {
			t.Errorf("ClassifyError(%q) returned no code; the catch-all must always classify", msg)
			continue
		}
		if executorCodes[got] {
			t.Errorf("ClassifyError(%q) = %q, which the executor owns, not the engine", msg, got)
		}
		if !emitted[got] {
			t.Errorf("ClassifyError(%q) = %q, which no rule in the profile emits", msg, got)
		}
	}
}

type affinityCase struct {
	declared string
	want     string
}

var affinityCases = []affinityCase{
	// one case per class, plus the variants the dataset relies on
	{"INTEGER", "INTEGER"},
	{"INT", "INTEGER"},
	{"BIGINT", "INTEGER"},
	{"TEXT", "TEXT"},
	{"VARCHAR(20)", "TEXT"},
	{"CHARACTER(2)", "TEXT"},
	{"CLOB", "TEXT"},
	{"BLOB", "BLOB"},
	{"", "BLOB"},
	{"REAL", "REAL"},
	{"FLOAT", "REAL"},
	{"DOUBLE PRECISION", "REAL"},
	{"NUMERIC", "NUMERIC"},
	{"DECIMAL(10,2)", "NUMERIC"},
	{"DATE", "NUMERIC"},
	{"TIMESTAMP", "NUMERIC"},
	{"BOOLEAN", "NUMERIC"},
}

func TestAffinity(t *testing.T) {
	covered := map[string]bool{}
	for _, c := range affinityCases {
		got := Affinity(c.declared)
		if got != c.want {
			t.Errorf("Affinity(%q) = %q, want %q", c.declared, got, c.want)
		}
		covered[c.want] = true
	}
	for _, class := range AffinityClasses() {
		if !covered[class] {
			t.Errorf("no case exercises the %s affinity", class)
		}
	}
	// The declared type is matched case-insensitively and after trimming, as
	// SQLite does.
	if got := Affinity("  integer "); got != "INTEGER" {
		t.Errorf(`Affinity("  integer ") = %q, want INTEGER`, got)
	}
	if got := Affinity("varchar(20)"); got != "TEXT" {
		t.Errorf(`Affinity("varchar(20)") = %q, want TEXT`, got)
	}
}

// TestAffinityRulesMatchTheProfile executes the profile's own claim: the
// declared types the dataset uses map to the class it says they do.
func TestAffinityRulesMatchTheProfile(t *testing.T) {
	p := SQLite()
	got := make([]string, 0, len(p.Types.AffinityRules))
	for _, rule := range p.Types.AffinityRules {
		got = append(got, rule.Affinity)
	}
	if want := AffinityClasses(); !reflect.DeepEqual(got, want) {
		t.Fatalf("profile affinity rules %v, want %v", got, want)
	}
	if len(p.Types.Declared) == 0 {
		t.Fatal("types.declared is empty")
	}
	for declared, want := range p.Types.Declared {
		if class := Affinity(declared); class != want {
			t.Errorf("Affinity(%q) = %q but the profile declares %q", declared, class, want)
		}
	}
}

func TestEncodeValue(t *testing.T) {
	p := SQLite()
	cases := []struct {
		name     string
		raw      any
		affinity string
		want     any
	}{
		{"integer", int64(59), "INTEGER", int64(59)},
		{"integer zero", int(0), "INTEGER", int64(0)},
		{"integer at the JSON boundary", int64(9007199254740991), "INTEGER", int64(9007199254740991)},
		{"integer past the JSON boundary", int64(9007199254740992), "INTEGER", "9007199254740992"},
		{"negative integer past the boundary", int64(-9007199254740992), "INTEGER", "-9007199254740992"},
		{"unsigned integer past the boundary", uint64(math.MaxUint64), "INTEGER", "18446744073709551615"},
		{"real", float64(42.5), "REAL", float64(42.5)},
		{"integral real stays a number", float64(59), "REAL", float64(59)},
		{"nan", math.NaN(), "REAL", nil},
		{"positive infinity", math.Inf(1), "REAL", "Infinity"},
		{"negative infinity", math.Inf(-1), "REAL", "-Infinity"},
		{"text", "Ada Whitfield", "TEXT", "Ada Whitfield"},
		{"dynamic typing: ISO-8601 text in a NUMERIC-affinity column", "2025-11-02", "NUMERIC", "2025-11-02"},
		{"numeric-looking text in a TEXT column", "59.00", "TEXT", "59.00"},
		{"stored integer in a TEXT column", int64(1), "TEXT", int64(1)},
		{"blob", []byte{0xde, 0xad, 0xbe, 0xef}, "BLOB", `\xdeadbeef`},
		{"empty blob", []byte{}, "BLOB", `\x`},
		{"null", nil, "TEXT", nil},
		{"true materialised by the driver", true, "NUMERIC", int64(1)},
		{"false materialised by the driver", false, "NUMERIC", int64(0)},
		{"date driver materialised as a time.Time", time.Date(2025, 11, 2, 0, 0, 0, 0, time.UTC), "NUMERIC", "2025-11-02T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := p.EncodeValue(c.raw, c.affinity); !reflect.DeepEqual(got, c.want) {
				t.Errorf("EncodeValue(%#v, %q) = %#v, want %#v", c.raw, c.affinity, got, c.want)
			}
		})
	}
}

// TestEncodeValueIgnoresAffinity is the dynamic-typing rule as an executable
// claim: the affinity a column was declared with does not change how a stored
// value is reported. The storage class does.
func TestEncodeValueIgnoresAffinity(t *testing.T) {
	p := SQLite()
	values := []any{nil, true, int64(59), float64(42.5), "2025-11-02", []byte{0x01, 0x02}}
	affinities := append([]string{""}, AffinityClasses()...)
	affinities = append(affinities, "GARBAGE")
	base := make([]any, len(values))
	for i, v := range values {
		base[i] = p.EncodeValue(v, "TEXT")
	}
	for _, affinity := range affinities {
		for i, v := range values {
			got := p.EncodeValue(v, affinity)
			if !reflect.DeepEqual(got, base[i]) {
				t.Errorf("EncodeValue(%#v, %q) = %#v, want %#v: the value's class decides, not the declaration", v, affinity, got, base[i])
			}
		}
	}
}
