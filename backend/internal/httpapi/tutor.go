package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* The Ask Tutor route (§6.5, §11.2).
 *
 * POST /tutor is a conversation request: the client sends facts (routing, the
 * exercise reference, the learner's SQL, the turns so far) and the gateway
 * assembles the context server-side. The gateway, not the client, runs the
 * learner's SQL and the reference; the client never supplies rows the tutor
 * reads. The reference query arms the no-solution guard and never reaches the
 * prompt. Single-shot complete, no SSE.
 *
 * The one-shot inline hint is the zero-turn case: it carries no exercise
 * bundle, so the context is the learner's own material alone. */

// Row and table caps for the assembled context. The whole context also has one
// bound, ai.Limits.MaxTutorContextBytes; these keep any single block small.
const (
	tutorExpectedRowCap = 50
	tutorResultRowCap   = 20
	tutorTableCap       = 20
)

// tutorRequest is the POST /tutor body. It embeds ai.TutorRequest (whose
// Context and Guards are json:"-") and adds the routing fields /answers/evaluate
// already reads, plus the exercise bundle the client holds.
type tutorRequest struct {
	ai.TutorRequest
	statementRequest
	Exercise *tutorExercise `json:"exercise"`
}

// tutorExercise is the contracts/exercise.schema.json bundle the client sends.
// `contract` and `result_contract` are the two spellings of one contract; a
// reference solution reaches the gateway only to arm the guard and compute the
// oracle, and the client's own `expected` is ignored: the gateway runs the
// reference itself.
type tutorExercise struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	Prompt            string                 `json:"prompt"`
	Difficulty        string                 `json:"difficulty"`
	Type              string                 `json:"type"`
	ReferenceSolution string                 `json:"reference_solution"`
	Solution          string                 `json:"solution"`
	Contract          *schema.ResultContract `json:"contract"`
	ResultContract    *schema.ResultContract `json:"result_contract"`
}

// tutorFacts are the exercise facts the context carries, whatever their source:
// a stored challenge, a stored course exercise, or the client bundle.
type tutorFacts struct {
	ID          string
	Title       string
	Prompt      string
	Difficulty  string
	Type        string
	Reference   string
	CrossCheck  string
	Contract    schema.ResultContract
	HasContract bool
	Expected    schema.ExpectedResult
	HasExpected bool
}

// tutorAssembly is the handler's output: the rendered context, the guard
// strings, and the summary the wire response carries.
type tutorAssembly struct {
	Context string
	Guards  []string
	Summary ai.TutorContextSummary
}

// handleTutor implements POST /tutor.
func (s *Server) handleTutor(w http.ResponseWriter, r *http.Request) {
	var req tutorRequest
	if !readJSON(w, r, &req) {
		return
	}
	u, svc, ok := s.requireAI(w, r)
	if !ok {
		return
	}
	limits := svc.Limits()
	if !s.tutorRate.allow(u.ID) {
		writeAIError(w, ai.NewError(ai.CodeRateLimited, "",
			"The tutor is rate limited for this account. Try again shortly.", ""))
		return
	}

	// A tutor turn executes the learner's SQL, so it spends the same statement
	// budget /query and /answers/evaluate spend. The pipeline owns its own
	// shorter deadline; the transport gives it headroom.
	ctx, cancel := context.WithTimeout(r.Context(), limits.TutorTimeout+5*time.Second)
	defer cancel()
	release, admitted := s.enter(ctx)
	if !admitted {
		writeAIError(w, ai.NewError(ai.CodeEnvironmentDown, "",
			"The gateway is at its concurrency limit; try again shortly.", ""))
		return
	}
	defer release()

	datasetID := resolveDataset(req.Dataset, nil)
	dialect := requestedDialect(req.Dialect)
	exec, refusal := s.statementExecutorFor(ctx, r, dialect, datasetID)
	asm, aerr := s.assembleTutorContext(ctx, u, svc, &req, exec, refusal, datasetID, dialect)
	if aerr != nil {
		writeAIError(w, aerr)
		return
	}
	req.Context = asm.Context
	req.Guards = asm.Guards
	resp, err := svc.Tutor(ctx, req.TutorRequest)
	if err != nil {
		writeAIError(w, err)
		return
	}
	resp.Context = asm.Summary
	writeJSON(w, http.StatusOK, resp)
}

// assembleTutorContext runs the learner's SQL and the reference, resolves the
// exercise facts, and renders the bounded, fenced context. exec is nil when the
// gateway could not provide an executor, in which case the client's own
// error/result summary is used and labelled learner-reported.
func (s *Server) assembleTutorContext(ctx context.Context, u store.User, svc *ai.Service, req *tutorRequest, exec engine.Executor, refusal *engine.QueryResult, datasetID, dialect string) (tutorAssembly, *schema.Error) {
	facts, ferr := s.tutorFacts(ctx, u, svc, req)
	if ferr != nil {
		return tutorAssembly{}, ferr
	}
	limits := svc.Limits()
	display := datasetID
	if display == "" {
		display = commerceDatasetID
	}
	timeoutMS := int(limits.TutorTimeout.Milliseconds())

	attempt := strings.TrimSpace(req.SQL) != ""
	var qr *engine.QueryResult
	learnerResult := "not_run"
	if attempt {
		if exec == nil {
			learnerResult = "unavailable"
		} else {
			res := engine.Execute(ctx, exec, req.SQL, engine.Limits{MaxRows: tutorResultRowCap, TimeoutMS: timeoutMS})
			qr = &res
			if res.Status == "succeeded" {
				learnerResult = "ran"
			} else {
				learnerResult = "error"
			}
		}
	}

	// The expected result is the gateway's own run of the reference. A stored
	// course exercise already carries the engine-measured rows from generation.
	if !facts.HasExpected && exec != nil && strings.TrimSpace(facts.Reference) != "" {
		res := engine.Execute(ctx, exec, facts.Reference, engine.Limits{MaxRows: tutorExpectedRowCap, TimeoutMS: timeoutMS})
		if res.Status == "succeeded" {
			facts.Expected = schema.ExpectedResult{Columns: columnNames(res.Columns), Rows: res.Rows}
			facts.HasExpected = true
		}
	}
	if !facts.HasContract && facts.HasExpected {
		facts.Contract = schema.ResultContract{Columns: facts.Expected.Columns}
		facts.HasContract = true
	}

	tables, tablesOK := dataset.Tables(display)
	tableNames := tutorTableNames(tables, tablesOK)

	summary := ai.TutorContextSummary{
		ExerciseID:      facts.ID,
		Dataset:         display,
		Dialect:         dialect,
		Tables:          tableNames,
		TablesTruncated: tablesOK && len(tables.Tables) > tutorTableCap,
		ExpectedColumns: expectedColumnCount(facts),
		LearnerResult:   learnerResult,
	}
	switch {
	case !facts.HasContract && !facts.HasExpected:
		summary.Expected = "unavailable"
	case !attempt || !facts.HasExpected:
		summary.Expected = "columns_only"
	default:
		summary.Expected = "included"
	}

	context, state := renderTutorContext(limits, facts, display, dialect, req, qr, refusal, learnerResult, tables, tablesOK, summary)
	summary.Expected = state
	if state == "included" {
		rows := len(facts.Expected.Rows)
		if rows > tutorExpectedRowCap {
			rows = tutorExpectedRowCap
		}
		summary.ExpectedRows = rows
	}

	guards := make([]string, 0, 2)
	if strings.TrimSpace(facts.Reference) != "" {
		guards = append(guards, facts.Reference)
	}
	if strings.TrimSpace(facts.CrossCheck) != "" {
		guards = append(guards, facts.CrossCheck)
	}
	return tutorAssembly{Context: context, Guards: guards, Summary: summary}, nil
}

// tutorFacts resolves the exercise the request names. A stored record wins over
// a client bundle, and a request that names none is refused.
func (s *Server) tutorFacts(ctx context.Context, u store.User, svc *ai.Service, req *tutorRequest) (tutorFacts, *schema.Error) {
	switch {
	case strings.TrimSpace(req.CourseID) != "":
		depot := s.aiCourseDepot()
		if depot == nil {
			return tutorFacts{}, ai.NewError(ai.CodeUnavailable, "", aiOffMessage, "")
		}
		rec, owned, err := ownedCourse(ctx, depot, u.ID, req.CourseID)
		if err != nil {
			return tutorFacts{}, ai.NewError(ai.CodeUnavailable, "", "The course could not be loaded.", engine.RedactSecrets(err.Error()))
		}
		if !owned {
			return tutorFacts{}, ai.NewError(ai.CodeNotFound, "", "No such course.", req.CourseID)
		}
		ex, found := findCourseExercise(rec.Plan, req.ExerciseID)
		if !found {
			return tutorFacts{}, ai.NewError(ai.CodeInvalidRequest, "",
				"The course has no exercise with that id.", req.ExerciseID)
		}
		f := tutorFacts{
			ID: ex.ID, Title: ex.Title, Prompt: ex.Prompt, Difficulty: string(ex.Difficulty), Type: ex.Type,
			Reference: ex.ReferenceSolution, CrossCheck: ex.CrossCheckSolution,
			Contract: ex.ResultContract, HasContract: true,
		}
		if ex.Expected != nil {
			f.Expected = *ex.Expected
			f.HasExpected = true
		}
		return f, nil

	case strings.TrimSpace(req.ChallengeID) != "":
		rec, found, err := svc.Challenge(ctx, req.ChallengeID)
		if err != nil {
			return tutorFacts{}, ai.NewError(ai.CodeUnavailable, "", "The challenge could not be loaded.", engine.RedactSecrets(err.Error()))
		}
		if !found || (rec.OwnerID != "" && rec.OwnerID != u.ID) {
			return tutorFacts{}, ai.NewError(ai.CodeNotFound, "", "No such challenge.", req.ChallengeID)
		}
		art := rec.Artifact
		return tutorFacts{
			ID: art.ID, Title: art.Title, Prompt: art.Prompt, Difficulty: string(art.Difficulty), Type: art.Type,
			Reference: art.ReferenceSolution, CrossCheck: art.CrossCheckSolution,
			Contract: art.ResultContract, HasContract: true,
		}, nil

	case req.Exercise != nil:
		ex := req.Exercise
		f := tutorFacts{
			ID: firstNonEmpty(ex.ID, req.ExerciseID), Title: ex.Title, Prompt: ex.Prompt,
			Difficulty: ex.Difficulty, Type: ex.Type,
			Reference: firstNonEmpty(ex.ReferenceSolution, ex.Solution),
		}
		if ex.ResultContract != nil {
			f.Contract, f.HasContract = *ex.ResultContract, true
		} else if ex.Contract != nil {
			f.Contract, f.HasContract = *ex.Contract, true
		}
		return f, nil

	default:
		// The zero-turn inline hint carries no exercise reference: it is the
		// learner's own material alone, exactly as before the conversation
		// existed. A conversation, which claims a context, must name one.
		if len(req.Turns) > 0 {
			return tutorFacts{}, ai.NewError(ai.CodeInvalidRequest, "",
				"A tutor conversation needs an exercise: send `exercise`, or `course_id` with `exercise_id`, or `challenge_id`.", "exercise")
		}
		return tutorFacts{}, nil
	}
}

// findCourseExercise locates one exercise in a stored plan by id. An empty id
// names nothing: a course has no single exercise.
func findCourseExercise(plan schema.CoursePlan, id string) (schema.CourseExercise, bool) {
	if strings.TrimSpace(id) == "" {
		return schema.CourseExercise{}, false
	}
	for _, ch := range plan.Chapters {
		for _, ex := range ch.Exercises {
			if ex.ID == id {
				return ex, true
			}
		}
	}
	return schema.CourseExercise{}, false
}

// renderTutorContext renders the bounded, fenced context and returns the
// expected-result state actually rendered. Every untrusted part goes through
// ai.TutorFence; the whole string is capped at MaxTutorContextBytes.
func renderTutorContext(limits ai.Limits, facts tutorFacts, display, dialect string, req *tutorRequest, qr *engine.QueryResult, refusal *engine.QueryResult, learnerResult string, tables dataset.TablesResponse, tablesOK bool, summary ai.TutorContextSummary) (string, string) {
	budget := limits.MaxTutorContextBytes
	var b strings.Builder
	fit := func(block string) bool {
		if b.Len()+len(block)+1 > budget {
			return false
		}
		b.WriteString(block)
		b.WriteString("\n")
		return true
	}

	fit(ai.TutorFence("EXERCISE", strings.Join([]string{
		"exercise_id: " + facts.ID,
		"title: " + capText(facts.Title, 200),
		"difficulty: " + facts.Difficulty,
		"type: " + facts.Type,
		"objective: " + capText(facts.Prompt, limits.MaxPromptChars),
	}, "\n")))

	if tablesOK {
		lines := []string{"dataset: " + display, "dialect: " + dialect, "tables: " + strings.Join(summary.Tables, ", ")}
		for i, t := range tables.Tables {
			if i >= tutorTableCap {
				break
			}
			lines = append(lines, fmt.Sprintf("table %s(%s)", t.Name, tableColumns(t)))
		}
		fit(ai.TutorFence("SCHEMA", strings.Join(lines, "\n")))
	}

	switch {
	case qr != nil:
		fit(ai.TutorFence("ENGINE_RESULT", strings.Join(resultLines(*qr), "\n")))
	case refusal != nil && refusal.Error != nil:
		lines := []string{"status: unavailable", "learner-reported: true", "error_code: " + refusal.Error.Code, "error_message: " + refusal.Error.Message}
		if req.ResultSummary != "" {
			lines = append(lines, "result_summary: "+req.ResultSummary)
		}
		fit(ai.TutorFence("ENGINE_RESULT", strings.Join(lines, "\n")))
	case strings.TrimSpace(req.SQL) != "":
		lines := []string{"status: unavailable", "learner-reported: true"}
		if req.ErrorCode != "" {
			lines = append(lines, "error_code: "+req.ErrorCode)
		}
		if req.ErrorMessage != "" {
			lines = append(lines, "error_message: "+req.ErrorMessage)
		}
		if req.ResultSummary != "" {
			lines = append(lines, "result_summary: "+req.ResultSummary)
		}
		fit(ai.TutorFence("ENGINE_RESULT", strings.Join(lines, "\n")))
	}

	if facts.HasContract {
		fit(ai.TutorFence("RESULT_CONTRACT", strings.Join([]string{
			"columns: " + strings.Join(facts.Contract.Columns, ", "),
			"column_order: " + defaultString(facts.Contract.ColumnOrder, "ignored"),
			"row_order: " + defaultString(facts.Contract.RowOrder, "ignored"),
			"duplicates: " + defaultString(facts.Contract.Duplicates, "preserved"),
		}, "\n")))
	}

	return finishTutorContext(b, budget), renderExpected(facts, summary.Expected, fit)
}

// renderExpected appends the expected-result block and reports the state the
// context actually carries. Rows appear only when an attempt was made and they
// fit the budget; a block that does not fit degrades to columns only.
func renderExpected(facts tutorFacts, state string, fit func(string) bool) string {
	if state == "unavailable" {
		if !fit(ai.TutorFence("EXPECTED_RESULT", "state: unavailable")) {
			return "unavailable"
		}
		return "unavailable"
	}
	block := func(withRows bool) string {
		lines := []string{"state: " + state, "columns: " + strings.Join(facts.Expected.Columns, ", ")}
		if withRows && len(facts.Expected.Rows) > 0 {
			rows := facts.Expected.Rows
			if len(rows) > tutorExpectedRowCap {
				rows = rows[:tutorExpectedRowCap]
			}
			lines = append(lines, fmt.Sprintf("rows (%d):", len(rows)))
			lines = append(lines, formatRows(facts.Expected.Columns, rows)...)
		}
		return ai.TutorFence("EXPECTED_RESULT", strings.Join(lines, "\n"))
	}
	if state == "included" {
		if fit(block(true)) {
			return "included"
		}
		if fit(block(false)) {
			return "columns_only"
		}
		return "unavailable"
	}
	if !fit(block(false)) {
		return "unavailable"
	}
	return "columns_only"
}

// finishTutorContext caps the rendered context at the byte budget, trimming on
// a UTF-8 boundary so the prompt is always valid text.
func finishTutorContext(b strings.Builder, budget int) string {
	out := b.String()
	if len(out) <= budget {
		return out
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut]
}

/* ---------- rendering helpers ---------- */

func expectedColumnCount(f tutorFacts) int {
	if f.HasContract {
		return len(f.Contract.Columns)
	}
	return len(f.Expected.Columns)
}

func tutorTableNames(tables dataset.TablesResponse, ok bool) []string {
	names := []string{}
	if !ok {
		return names
	}
	for _, t := range tables.Tables {
		names = append(names, t.Name)
	}
	return names
}

func tableColumns(t dataset.TableInfo) string {
	cols := make([]string, 0, len(t.Cols))
	for _, c := range t.Cols {
		if len(c) >= 2 {
			cols = append(cols, c[0]+" "+c[1])
		} else if len(c) == 1 {
			cols = append(cols, c[0])
		}
	}
	return strings.Join(cols, ", ")
}

func columnNames(cols []engine.Column) []string {
	names := make([]string, 0, len(cols))
	for _, c := range cols {
		names = append(names, c.Name)
	}
	return names
}

func resultLines(qr engine.QueryResult) []string {
	cols := columnNames(qr.Columns)
	lines := []string{"status: " + qr.Status, "columns: " + strings.Join(cols, ", ")}
	if qr.Error != nil {
		lines = append(lines, "error_code: "+qr.Error.Code, "error_message: "+qr.Error.Message)
	}
	if len(qr.Rows) > 0 {
		lines = append(lines, fmt.Sprintf("rows (%d):", len(qr.Rows)))
		lines = append(lines, formatRows(cols, qr.Rows)...)
	}
	if qr.Truncated {
		lines = append(lines, "truncated: true")
	}
	return lines
}

func formatRows(cols []string, rows [][]any) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		n := len(cols)
		if len(row) > n {
			n = len(row)
		}
		cells := make([]string, n)
		for i := 0; i < n; i++ {
			switch {
			case i >= len(row):
				cells[i] = "NULL"
			default:
				cells[i] = valueString(row[i])
			}
		}
		out = append(out, strings.Join(cells, " | "))
	}
	return out
}

func valueString(v any) string {
	switch x := v.(type) {
	case nil:
		return "NULL"
	case string:
		return x
	case []byte:
		return string(x)
	default:
		return fmt.Sprint(x)
	}
}

// capText bounds one untrusted text field by runes, so a client cannot stuff a
// single block past the context budget with one field.
func capText(s string, limit int) string {
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	return string(runes[:limit])
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func defaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
