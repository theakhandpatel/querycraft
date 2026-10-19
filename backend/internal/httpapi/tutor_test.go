package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/ai"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/schema"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* The Ask Tutor route, from the transport's point of view. The service's own
 * prompt policy is proved in backend/internal/ai; what belongs here is the route
 * shape, the server-side assembly, the rate limit and the attempt gate. */

// stubRunner attaches an executor to a tutor service. The tutor route does not
// use the Runner itself, but ai.Service.Available requires one, so a configured
// gateway needs it non-nil.
type stubRunner struct{ exec engine.Executor }

func (r stubRunner) Scratch(context.Context, string) (engine.Executor, func(), error) {
	return r.exec, func() {}, nil
}

// tutorServer is a gateway with AI attached to the fake provider and a memory
// depot, so /tutor has somewhere to run.
func tutorServer(t *testing.T) *Server {
	t.Helper()
	s := newTestServer(&fakeExecutor{})
	s.UseAI(ai.Config{
		Provider: ai.NewFakeProvider(),
		Depot:    store.NewMemory(),
		Runner:   stubRunner{exec: &fakeExecutor{}},
	})
	return s
}

const tutorBundle = `"exercise":{"id":"ex.1","title":"Rows per status","prompt":"Count the rows per status.","difficulty":"beginner","type":"write","reference_solution":"SELECT status, count(*) FROM orders GROUP BY status","contract":{"columns":["status","order_count"],"column_order":"exact","row_order":"ignored","duplicates":"preserved","numeric_tolerance":0}}`

func TestTutorConversationThroughTheFakeProvider(t *testing.T) {
	s := tutorServer(t)
	token := signupUser(t, s, "tutor-chat@example.test")

	body := `{"sql":"SELECT 1","objective":"count rows","dataset":"commerce","dialect":"postgresql",` +
		tutorBundle + `,"turns":[{"role":"learner","text":"why is my count off?"},{"role":"tutor","text":"check the grouping"}]}`
	w := doAs(t, s, token, http.MethodPost, "/tutor", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var out ai.TutorResponse
	mustDecode(t, w, &out)
	if out.Message == "" {
		t.Fatal("the tutor returned no message")
	}
	if out.RevealsSolution {
		t.Fatal("the tutor claimed to reveal the solution")
	}
	if out.Context.Dialect != "postgresql" {
		t.Fatalf("context dialect = %q, want postgresql", out.Context.Dialect)
	}
	if out.Context.Dataset != "commerce" {
		t.Fatalf("context dataset = %q, want commerce", out.Context.Dataset)
	}
	if len(out.Context.Tables) == 0 {
		t.Fatal("the context carried no dataset schema")
	}
	if out.Context.LearnerResult != "unavailable" {
		t.Fatalf("learner_result = %q, want unavailable (no workspace in this test)", out.Context.LearnerResult)
	}
}

// The attempt gate: before the learner runs, the context carries the contract
// columns and no expected rows; after a run, it carries the rows.
func TestTutorAttemptGate(t *testing.T) {
	s := newTestServer(&fakeExecutor{})
	svc := ai.NewService(ai.Config{
		Provider: ai.NewFakeProvider(),
		Depot:    store.NewMemory(),
		Runner:   stubRunner{exec: &fakeExecutor{}},
	})

	noAttempt := &tutorRequest{Exercise: &tutorExercise{
		ID:                "ex.1",
		Prompt:            "Count the rows per status.",
		ReferenceSolution: "SELECT status FROM orders -- reference_only_marker",
		ResultContract:    &schema.ResultContract{Columns: []string{"status", "order_count"}},
	}}
	asm, err := s.assembleTutorContext(context.Background(), store.User{ID: "u1"}, svc, noAttempt, &fakeExecutor{}, nil, "", "postgresql")
	if err != nil {
		t.Fatalf("assemble (no attempt) error = %v", err)
	}
	if asm.Summary.Expected != "columns_only" {
		t.Fatalf("expected state = %q without an attempt, want columns_only", asm.Summary.Expected)
	}
	if asm.Summary.LearnerResult != "not_run" {
		t.Fatalf("learner_result = %q without SQL, want not_run", asm.Summary.LearnerResult)
	}
	if strings.Contains(asm.Context, "delivered") {
		t.Fatal("expected rows reached the context before an attempt")
	}

	attempt := &tutorRequest{Exercise: noAttempt.Exercise}
	attempt.SQL = "SELECT 1"
	asm, err = s.assembleTutorContext(context.Background(), store.User{ID: "u1"}, svc, attempt, &fakeExecutor{}, nil, "", "postgresql")
	if err != nil {
		t.Fatalf("assemble (attempt) error = %v", err)
	}
	if asm.Summary.Expected != "included" {
		t.Fatalf("expected state = %q after an attempt, want included", asm.Summary.Expected)
	}
	if asm.Summary.LearnerResult != "ran" {
		t.Fatalf("learner_result = %q after a run, want ran", asm.Summary.LearnerResult)
	}
	if !strings.Contains(asm.Context, "rows (") {
		t.Fatalf("the expected rows did not reach the context: %q", asm.Context)
	}
	// The reference query is a guard, never part of the rendered context.
	if strings.Contains(asm.Context, "reference_only_marker") {
		t.Fatal("the reference query leaked into the rendered context")
	}
}

func TestTutorRequiresAnExercise(t *testing.T) {
	s := tutorServer(t)
	token := signupUser(t, s, "tutor-no-exercise@example.test")

	// A zero-turn hint without an exercise reference keeps working: it is the
	// inline "Explain my mistake" path, unchanged.
	w := doAs(t, s, token, http.MethodPost, "/tutor", `{"sql":"SELECT 1","objective":"count rows"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("zero-turn hint status = %d body = %s, want 200", w.Code, w.Body.String())
	}

	// A conversation, which claims a context, must name the exercise.
	w = doAs(t, s, token, http.MethodPost, "/tutor",
		`{"sql":"SELECT 1","objective":"count rows","turns":[{"role":"learner","text":"why?"}]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s, want 400", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ai.CodeInvalidRequest) {
		t.Fatalf("body = %s, want %s", w.Body.String(), ai.CodeInvalidRequest)
	}
}

func TestTutorRateLimitAnswers429(t *testing.T) {
	s := New(&fakeExecutor{}, store.NewMemory(),
		Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2, TutorTurnsPerHour: 1}, nil)
	s.UseAI(ai.Config{
		Provider: ai.NewFakeProvider(),
		Depot:    store.NewMemory(),
		Runner:   stubRunner{exec: &fakeExecutor{}},
	})
	token := signupUser(t, s, "tutor-rate@example.test")
	body := `{"sql":"SELECT 1","objective":"count rows",` + tutorBundle + `}`

	if w := doAs(t, s, token, http.MethodPost, "/tutor", body); w.Code != http.StatusOK {
		t.Fatalf("first turn status = %d body = %s, want 200", w.Code, w.Body.String())
	}
	w := doAs(t, s, token, http.MethodPost, "/tutor", body)
	if w.Code != http.StatusTooManyRequests {
		t.Fatalf("second turn status = %d body = %s, want 429", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), ai.CodeRateLimited) {
		t.Fatalf("body = %s, want %s", w.Body.String(), ai.CodeRateLimited)
	}
}

func TestTutorRateBucket(t *testing.T) {
	r := newTutorRate(2, 0)
	if !r.allow("a") || !r.allow("a") {
		t.Fatal("the first two turns of a fresh bucket must pass")
	}
	if r.allow("a") {
		t.Fatal("the third turn exceeded the budget and must be refused")
	}
	if !r.allow("b") {
		t.Fatal("another account must have its own bucket")
	}
}

// The expected rows the tutor sees are capped at tutorExpectedRowCap, whatever
// the stored measured result holds.
func TestExpectedRowsAreCapped(t *testing.T) {
	rows := make([][]any, tutorExpectedRowCap+40)
	for i := range rows {
		rows[i] = []any{i}
	}
	facts := tutorFacts{Expected: schema.ExpectedResult{Columns: []string{"n"}, Rows: rows}}

	var b strings.Builder
	state := renderExpected(facts, "included", func(block string) bool {
		b.WriteString(block)
		return true
	})
	if state != "included" {
		t.Fatalf("state = %q, want included", state)
	}
	if !strings.Contains(b.String(), fmt.Sprintf("rows (%d)", tutorExpectedRowCap)) {
		t.Fatalf("the expected rows were not capped at %d: %q", tutorExpectedRowCap, b.String())
	}
}
