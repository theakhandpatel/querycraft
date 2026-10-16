package ai

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// The policy gate. A generated bundle is untrusted input: every field is
// bounded, the reference solution must be a single read-only statement, and the
// setup plan may only build scratch objects. The rules live here, in one place,
// because both the pipeline stage and the test table call the same functions —
// a rule that is only asserted in a test is a rule the pipeline does not have.
//
// This is deliberately a tokenizer and allowlist, not a SQL parser. A parser
// would have to agree with PostgreSQL's grammar to be useful, and a
// near-parser that disagrees in one corner is worse than an honest, slightly
// over-strict gate: a false rejection costs one re-generation, a false
// acceptance runs generated SQL in a shared environment.

/* ---------- request validation ---------- */

// Request bounds that are not part of Limits, because they are about shape
// rather than cost. They are constants so the test table can name them.
const (
	maxTopics      = 16
	maxConstraints = 16
	maxSteps       = 40
	maxTopicChars  = 200
)

// validateRequest checks the learner's ask before any provider call. It returns
// an envelope error naming the offending field in Detail, or nil.
func validateRequest(req schema.GenerationRequest, l Limits) *schema.Error {
	if req.Kind != "" && req.Kind != "challenge" && req.Kind != "course" {
		return reqError("kind", fmt.Sprintf("unknown kind %q", req.Kind))
	}
	// The count is checked before "is there anything to generate from", so an
	// oversized request is refused for the reason that actually applies rather
	// than for a missing title it does not have.
	if len(req.Topics) > maxTopics {
		return reqError("topics", fmt.Sprintf("%d topics exceeds the limit of %d", len(req.Topics), maxTopics))
	}
	if strings.TrimSpace(req.Title) == "" && len(nonEmpty(req.Topics)) == 0 {
		return reqError("title", "a title or at least one topic is required")
	}
	for i, t := range req.Topics {
		if strings.TrimSpace(t) == "" {
			return reqError("topics", fmt.Sprintf("topic %d is empty", i))
		}
		if utf8.RuneCountInString(t) > maxTopicChars {
			return reqError("topics", fmt.Sprintf("topic %d is longer than %d characters", i, maxTopicChars))
		}
	}
	if len(req.Constraints) > maxConstraints {
		return reqError("constraints", fmt.Sprintf("%d constraints exceeds the limit of %d", len(req.Constraints), maxConstraints))
	}
	if len(req.Steps) > maxSteps {
		return reqError("steps", fmt.Sprintf("%d steps exceeds the limit of %d", len(req.Steps), maxSteps))
	}
	for field, value := range map[string]string{
		"title":    req.Title,
		"scenario": req.Scenario,
	} {
		if utf8.RuneCountInString(value) > l.MaxPromptChars {
			return reqError(field, fmt.Sprintf("%d characters exceeds the limit of %d", utf8.RuneCountInString(value), l.MaxPromptChars))
		}
	}
	for i, s := range append(append([]string{}, req.Steps...), req.Constraints...) {
		if utf8.RuneCountInString(s) > l.MaxPromptChars {
			return reqError("steps", fmt.Sprintf("entry %d is longer than %d characters", i, l.MaxPromptChars))
		}
	}
	if req.Difficulty != "" && !validDifficulty(req.Difficulty) {
		return reqError("difficulty", fmt.Sprintf("unknown difficulty %q", req.Difficulty))
	}
	return nil
}

// normalizeRequest applies the clamping and defaulting validateRequest
// promises, so the pipeline does not have to remember them separately.
func normalizeRequest(req schema.GenerationRequest, l Limits) schema.GenerationRequest {
	req.ExerciseCount = clampInt(req.ExerciseCount, 1, 40)
	if req.Difficulty == "" {
		req.Difficulty = schema.Intermediate
	}
	return req
}

func reqError(field, detail string) *schema.Error {
	return NewError(CodeInvalidRequest, schema.StageValidateRequest,
		"The generation request is not usable.", field+": "+detail)
}

func validDifficulty(d schema.Difficulty) bool {
	switch d {
	case schema.Beginner, schema.Intermediate, schema.Advanced:
		return true
	}
	return false
}

func nonEmpty(in []string) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func clampInt(v, lo, hi int) int {
	return min(max(v, lo), hi)
}

/* ---------- artifact validation ---------- */

// validateArtifact is the gate every generated bundle must pass before it is
// executed. Identity fields (id, version) and provenance are not checked: those
// are the service's to stamp, not the model's to supply, and a supplied value
// is overwritten rather than trusted.
func validateArtifact(a schema.Artifact, l Limits) *schema.Error {
	type check struct {
		field   string
		present bool
	}
	required := []check{
		{"title", strings.TrimSpace(a.Title) != ""},
		{"prompt", strings.TrimSpace(a.Prompt) != ""},
		{"difficulty", validDifficulty(a.Difficulty)},
		{"setup.dataset", strings.TrimSpace(a.Setup.Dataset) != ""},
		{"setup.schema_version", a.Setup.SchemaVersion >= 1},
		{"setup.seed_version", a.Setup.SeedVersion >= 1},
		{"reference_solution", strings.TrimSpace(a.ReferenceSolution) != ""},
		{"cross_check_solution", strings.TrimSpace(a.CrossCheckSolution) != ""},
		{"objectives", len(a.Objectives) > 0},
		{"hints", a.Hints != nil},
	}
	for _, c := range required {
		if !c.present {
			return artifactError(c.field, "required and must be non-empty")
		}
	}
	if a.Dialect != "" && a.Dialect != "postgresql" {
		return artifactError("dialect", fmt.Sprintf("unknown dialect %q", a.Dialect))
	}
	if a.Type != "" && !validExerciseType(a.Type) {
		return artifactError("type", fmt.Sprintf("unknown exercise type %q", a.Type))
	}
	if len(a.Hints) > l.MaxHints {
		return artifactError("hints", fmt.Sprintf("%d hints exceeds the limit of %d", len(a.Hints), l.MaxHints))
	}
	if len([]byte(a.Prompt)) > l.MaxOutputBytes {
		return artifactError("prompt", fmt.Sprintf("%d bytes exceeds the limit of %d", len([]byte(a.Prompt)), l.MaxOutputBytes))
	}
	if len(a.ResultContract.Columns) == 0 {
		return artifactError("result_contract.columns", "at least one column is required")
	}
	for i, c := range a.ResultContract.Columns {
		if strings.TrimSpace(c) == "" {
			return artifactError("result_contract.columns", fmt.Sprintf("column %d is empty", i))
		}
	}
	for field, value := range map[string]string{
		"result_contract.column_order": a.ResultContract.ColumnOrder,
		"result_contract.row_order":    a.ResultContract.RowOrder,
		"result_contract.duplicates":   a.ResultContract.Duplicates,
	} {
		if value != "" && !validContractEnum(field, value) {
			return artifactError(field, fmt.Sprintf("unknown value %q", value))
		}
	}
	if err := validateReference(a.ReferenceSolution); err != nil {
		return err
	}
	if err := validateReference(a.CrossCheckSolution); err != nil {
		return artifactError("cross_check_solution", strings.TrimPrefix(err.Detail, "reference_solution: "))
	}
	if len(a.Setup.Statements) > l.MaxSetupStatements {
		return artifactError("setup.statements", fmt.Sprintf("%d statements exceeds the limit of %d", len(a.Setup.Statements), l.MaxSetupStatements))
	}
	for i, stmt := range a.Setup.Statements {
		if err := validateSetupStatement(stmt); err != nil {
			/* The helper already names the field; repeating it here produced
			   "setup.statements: statement 0: setup.statements: the statement is
			   empty", which is what the repair pass then had to read — a repair
			   is only as good as the sentence describing the fault. */
			return artifactError("setup.statements", fmt.Sprintf("statement %d: %s", i, strings.TrimPrefix(err.Detail, "setup.statements: ")))
		}
	}
	return nil
}

func artifactError(field, detail string) *schema.Error {
	return NewError(CodeInvalidArtifact, schema.StageValidateArtifact,
		"The generated challenge did not pass validation.", field+": "+detail)
}

func validExerciseType(t string) bool {
	switch t {
	case "complete", "write", "modify", "predict", "fix", "combine", "solve":
		return true
	}
	return false
}

func validContractEnum(field, value string) bool {
	switch field {
	case "result_contract.column_order", "result_contract.row_order":
		return value == "exact" || value == "ignored"
	case "result_contract.duplicates":
		return value == "preserved" || value == "ignored"
	}
	return false
}

/* ---------- the SQL tokenizer ---------- */

// tokenKind distinguishes the parts of a statement whose meaning depends on
// their form: an unquoted `set` is a keyword, `"set"` is a column name.
type tokenKind int

const (
	tokWord tokenKind = iota // unquoted identifier or keyword
	tokQuotedIdent
	tokNumber
	tokString // a literal; its contents are never keywords
	tokPunct
)

type sqlToken struct {
	text string
	kind tokenKind
}

// tokenizeSQL is a small, forgiving lexer: it understands enough of PostgreSQL
// to know where strings, comments and dollar-quotes start and end, which is
// what makes keyword and statement-boundary checks trustworthy. Anything it
// cannot classify becomes punctuation, so an unusual character can only make
// the gate stricter, never looser.
func tokenizeSQL(sql string) []sqlToken {
	var out []sqlToken
	i := 0
	for i < len(sql) {
		c := sql[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			i = skipBlockComment(sql, i)
		case c == '\'':
			i = skipSingleQuoted(sql, i)
			out = append(out, sqlToken{kind: tokString})
		case c == '"':
			text, next := readDoubleQuoted(sql, i)
			out = append(out, sqlToken{text: text, kind: tokQuotedIdent})
			i = next
		case c == '$':
			if text, next, ok := readDollarQuoted(sql, i); ok {
				out = append(out, sqlToken{text: text, kind: tokString})
				i = next
				continue
			}
			start := i
			for i < len(sql) && isIdentPart(sql[i]) {
				i++
			}
			out = append(out, sqlToken{text: sql[start:i], kind: tokWord})
		case isIdentStart(c):
			start := i
			for i < len(sql) && isIdentPart(sql[i]) {
				i++
			}
			out = append(out, sqlToken{text: sql[start:i], kind: tokWord})
		case c >= '0' && c <= '9':
			start := i
			for i < len(sql) && (isIdentPart(sql[i]) || sql[i] == '.') {
				i++
			}
			out = append(out, sqlToken{text: sql[start:i], kind: tokNumber})
		default:
			out = append(out, sqlToken{text: string(c), kind: tokPunct})
			i++
		}
	}
	return out
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= utf8.RuneSelf
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '$'
}

func skipBlockComment(sql string, i int) int {
	depth := 0
	for i < len(sql) {
		if i+1 < len(sql) && sql[i] == '/' && sql[i+1] == '*' {
			depth++
			i += 2
			continue
		}
		if i+1 < len(sql) && sql[i] == '*' && sql[i+1] == '/' {
			depth--
			i += 2
			if depth == 0 {
				return i
			}
			continue
		}
		i++
	}
	return i
}

func skipSingleQuoted(sql string, i int) int {
	i++ // opening quote
	for i < len(sql) {
		if sql[i] == '\'' {
			if i+1 < len(sql) && sql[i+1] == '\'' {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return i
}

func readDoubleQuoted(sql string, i int) (string, int) {
	i++ // opening quote
	var b strings.Builder
	for i < len(sql) {
		if sql[i] == '"' {
			if i+1 < len(sql) && sql[i+1] == '"' {
				b.WriteByte('"')
				i += 2
				continue
			}
			return b.String(), i + 1
		}
		b.WriteByte(sql[i])
		i++
	}
	return b.String(), i
}

// readDollarQuoted recognises $tag$ ... $tag$ (PostgreSQL's dollar-quoted
// strings). It reports false when the text is not a dollar-quote, so a `$` in
// an identifier is not mistaken for one.
func readDollarQuoted(sql string, i int) (string, int, bool) {
	j := i + 1
	for j < len(sql) && sql[j] != '$' {
		if !isIdentPart(sql[j]) {
			return "", i, false
		}
		j++
	}
	if j >= len(sql) {
		return "", i, false
	}
	tag := sql[i : j+1] // includes both dollars
	end := strings.Index(sql[j+1:], tag)
	if end < 0 {
		return "", i, false
	}
	return "", j + 1 + end + len(tag), true
}

/* ---------- reference solution policy ---------- */

// forbiddenWords are statements and clauses that change something, reach the
// server, or read the catalog. The check is token-based and applies only to
// unquoted words, so `"set"` as a column alias stays legal while a real SET
// does not.
var forbiddenWords = map[string]bool{
	"CREATE": true, "ALTER": true, "DROP": true, "TRUNCATE": true,
	"GRANT": true, "REVOKE": true, "INSERT": true, "UPDATE": true,
	"DELETE": true, "MERGE": true, "COPY": true, "CALL": true,
	"VACUUM": true, "ANALYZE": true, "SET": true, "RESET": true,
	"SHOW": true, "DO": true, "LOCK": true, "REINDEX": true,
	"CLUSTER": true, "COMMENT": true, "DISCARD": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true, "SAVEPOINT": true,
	"RELEASE": true, "LISTEN": true, "NOTIFY": true, "UNLISTEN": true,
	"PREPARE": true, "EXECUTE": true, "DEALLOCATE": true, "EXPLAIN": true,
}

// validateReference enforces that a generated solution is one read-only
// statement. It is the load-bearing check of the whole gate: this is the SQL
// the pipeline will actually run.
func validateReference(sql string) *schema.Error {
	tokens := tokenizeSQL(sql)
	segs := splitStatements(tokens)
	if len(segs) == 0 || len(segs[0]) == 0 {
		return artifactError("reference_solution", "the statement is empty")
	}
	if len(segs) > 1 {
		return artifactError("reference_solution", fmt.Sprintf("expected a single statement, found %d", len(segs)))
	}
	first := firstWord(segs[0])
	if first != "SELECT" && first != "WITH" {
		return artifactError("reference_solution", fmt.Sprintf("a read-only statement must start with SELECT or WITH, found %q", first))
	}
	for _, tok := range segs[0] {
		if tok.kind == tokWord && forbiddenWords[strings.ToUpper(tok.text)] {
			return artifactError("reference_solution", fmt.Sprintf("%q is not allowed in a read-only solution", strings.ToUpper(tok.text)))
		}
		if (tok.kind == tokWord || tok.kind == tokQuotedIdent) && referencesCatalog(tok.text) {
			return artifactError("reference_solution", fmt.Sprintf("%q reaches the database catalog", tok.text))
		}
	}
	return nil
}

func referencesCatalog(ident string) bool {
	lower := strings.ToLower(ident)
	if strings.HasPrefix(lower, "pg_") {
		return true
	}
	switch lower {
	case "information_schema", "pg_catalog", "pg_toast", "current_setting", "set_config", "pg_read_file":
		return true
	}
	return false
}

/* ---------- setup plan policy ---------- */

// setupForbiddenWords are the mutations a scratch setup may never contain. The
// allowlist of first keywords already blocks most of them; this catches a
// forbidden verb that appears after a legal start, e.g. `CREATE TABLE ...;
// INSERT ...` is caught as two statements, but a nested DROP in a CTE is not.
var setupForbiddenWords = map[string]bool{
	"DROP": true, "ALTER": true, "GRANT": true, "REVOKE": true,
	"TRUNCATE": true, "DELETE": true, "UPDATE": true, "COPY": true,
	"CALL": true, "VACUUM": true, "DO": true, "SET": true,
	"RESET": true, "SHOW": true, "EXPLAIN": true, "REINDEX": true,
	"CLUSTER": true, "COMMENT": true, "DISCARD": true,
	"BEGIN": true, "COMMIT": true, "ROLLBACK": true,
}

// validateSetupStatement enforces that one setup entry only creates and fills a
// scratch object. Statements are applied in a throwaway environment, but the
// environment shares a cluster with everything else, so "throwaway" is a
// property of the database, not of the statement.
func validateSetupStatement(sql string) *schema.Error {
	tokens := tokenizeSQL(sql)
	segs := splitStatements(tokens)
	if len(segs) == 0 || len(segs[0]) == 0 {
		return artifactError("setup.statements", "the statement is empty")
	}
	if len(segs) > 1 {
		return artifactError("setup.statements", fmt.Sprintf("expected a single statement, found %d", len(segs)))
	}
	words := wordsOf(segs[0])
	if len(words) == 0 {
		return artifactError("setup.statements", "the statement has no leading keyword")
	}
	switch words[0] {
	case "CREATE":
		if len(words) < 2 || !isTableCreate(words[1:]) {
			return artifactError("setup.statements", "CREATE is only allowed for a scratch TABLE")
		}
	case "INSERT":
		if len(words) < 2 || words[1] != "INTO" {
			return artifactError("setup.statements", "INSERT must be INSERT INTO")
		}
	case "VALUES":
	default:
		return artifactError("setup.statements", fmt.Sprintf("%q may not start a setup statement", words[0]))
	}
	for _, tok := range segs[0] {
		if tok.kind == tokWord && setupForbiddenWords[strings.ToUpper(tok.text)] {
			return artifactError("setup.statements", fmt.Sprintf("%q is not allowed in a setup statement", strings.ToUpper(tok.text)))
		}
		if (tok.kind == tokWord || tok.kind == tokQuotedIdent) && referencesCatalog(tok.text) {
			return artifactError("setup.statements", fmt.Sprintf("%q reaches the database catalog", tok.text))
		}
	}
	return nil
}

// isTableCreate accepts TABLE, TEMP TABLE, TEMPORARY TABLE, UNLOGGED TABLE and
// the optional IF NOT EXISTS, in the order PostgreSQL accepts them.
func isTableCreate(rest []string) bool {
	i := 0
	for i < len(rest) && (rest[i] == "TEMP" || rest[i] == "TEMPORARY" || rest[i] == "UNLOGGED" || rest[i] == "GLOBAL" || rest[i] == "LOCAL") {
		i++
	}
	if i >= len(rest) || rest[i] != "TABLE" {
		return false
	}
	return true
}

/* ---------- shared statement helpers ---------- */

// splitStatements splits tokens on top-level semicolons. Empty segments are
// dropped, so a single trailing semicolon is not a second statement.
func splitStatements(tokens []sqlToken) [][]sqlToken {
	var segs [][]sqlToken
	var cur []sqlToken
	for _, tok := range tokens {
		if tok.kind == tokPunct && tok.text == ";" {
			if len(cur) > 0 {
				segs = append(segs, cur)
			}
			cur = nil
			continue
		}
		cur = append(cur, tok)
	}
	if len(cur) > 0 {
		segs = append(segs, cur)
	}
	return segs
}

// firstWord returns the first unquoted word of a statement, uppercased.
func firstWord(tokens []sqlToken) string {
	for _, tok := range tokens {
		if tok.kind == tokWord {
			return strings.ToUpper(tok.text)
		}
	}
	return ""
}

// wordsOf returns every unquoted word of a statement, uppercased.
func wordsOf(tokens []sqlToken) []string {
	out := make([]string, 0, len(tokens))
	for _, tok := range tokens {
		if tok.kind == tokWord {
			out = append(out, strings.ToUpper(tok.text))
		}
	}
	return out
}
