package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The M7 gate. Every test here is about one promise: a course is never shown to
   a learner before its exercises were executed and cross-checked, and the rows
   a learner is graded against are the rows the engine returned while checking.
   The negative cases matter as much as the positive one — a gate that only
   proves the happy path is a gate that has never refused anything. */

// coursePlanFixture is a valid two-chapter course built from the same authored
// topics the built-in provider uses, so its SQL really runs on the commerce
// dataset. Mutations in the tests below start from this and change one thing.
func coursePlanFixture() schema.CoursePlan {
	first, second := fakeTopics[0], fakeTopics[1]
	return schema.CoursePlan{
		Title:      "Trusting a query",
		Subtitle:   "Two chapters on reading the answer instead of the SQL.",
		Level:      "intermediate",
		Dialect:    "postgresql",
		Objectives: []string{"Answer a question with one statement.", "Check the answer against the question."},
		Setup:      schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
		Chapters: []schema.CourseChapter{
			chapterFixture("gen.one", 1, first),
			chapterFixture("gen.two", 2, second),
		},
	}
}

func chapterFixture(id string, n int, t fakeTopic) schema.CourseChapter {
	return schema.CourseChapter{
		ID:         id,
		N:          n,
		Title:      t.title,
		Summary:    t.prompt,
		Kind:       "Practice",
		Minutes:    10,
		Objectives: t.objectives,
		Sections: []schema.CourseSection{
			{Title: "The question", Body: "Read the question twice and write down the grain of the answer: one row per what? Every clause after that is about producing exactly those rows, and nothing else.\n\n" + t.prompt},
		},
		Exercises: []schema.CourseExercise{{
			ID:                 id + ".1",
			Title:              t.title,
			Prompt:             t.prompt,
			Difficulty:         schema.Intermediate,
			Type:               "write",
			Objectives:         t.objectives,
			Hints:              t.hints,
			ReferenceSolution:  t.reference,
			CrossCheckSolution: t.crossCheck,
			ResultContract: schema.ResultContract{
				Columns:          t.columns,
				ColumnOrder:      "exact",
				RowOrder:         "exact",
				Duplicates:       "preserved",
				NumericTolerance: 0,
			},
			Checks: []schema.Check{},
		}},
	}
}

func planJSON(t *testing.T, plan schema.CoursePlan) string {
	t.Helper()
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("encode plan: %v", err)
	}
	return string(raw)
}

// TestCoursePipelineActivatesAVerifiedCourse is the exit gate: a course that
// verifies is activated, stored, and carries the engine's own expected rows.
func TestCoursePipelineActivatesAVerifiedCourse(t *testing.T) {
	engineEx := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	svc := testService(NewFakeProvider(), depot, engineEx)

	out := svc.GenerateCourse(ctx, schema.GenerationRequest{
		Title:         "I want to get good at answering product questions with SQL",
		Difficulty:    schema.Intermediate,
		ExerciseCount: 3,
	})
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %v, stages = %+v)", out.Status, schema.StatusActive, out.Error, out.Stages)
	}
	if out.Plan == nil {
		t.Fatal("an active course must carry its plan")
	}
	if out.Error != nil {
		t.Fatalf("an active course must not carry an error: %v", out.Error)
	}
	if out.Kind != "course" {
		t.Fatalf("kind = %q, want %q", out.Kind, "course")
	}

	plan := out.Plan
	if len(plan.Chapters) == 0 {
		t.Fatal("the course has no chapters")
	}
	if plan.ID == "" || plan.Version != 1 || plan.Provenance.Provider != "fake" ||
		plan.Provenance.Model != "fake-v1" || plan.Provenance.DatasetVersion != 1 || plan.Provenance.CreatedAt.IsZero() {
		t.Fatalf("plan identity or provenance not stamped by the service: %+v", plan)
	}
	if len(out.Chapters) != len(plan.Chapters) {
		t.Fatalf("reported chapters = %d, want %d", len(out.Chapters), len(plan.Chapters))
	}

	exercises := 0
	for ci, ch := range plan.Chapters {
		if len(ch.Sections) == 0 {
			t.Errorf("chapter %d has no sections to read", ci)
		}
		for ei, qex := range ch.Exercises {
			exercises++
			if qex.Expected == nil {
				t.Fatalf("chapter %d exercise %d has no expected result: the grader has nothing to compare against", ci, ei)
			}
			if len(qex.Expected.Rows) == 0 {
				t.Fatalf("chapter %d exercise %d expected 0 rows", ci, ei)
			}
			if len(qex.Expected.Columns) != len(qex.ResultContract.Columns) {
				t.Errorf("exercise %s expected %d columns, contract names %d", qex.ID, len(qex.Expected.Columns), len(qex.ResultContract.Columns))
			}
			// The reference must still produce exactly these rows: the point of
			// measuring them is that they are the engine's answer, and this is
			// the assertion that keeps them from becoming a claim.
			res := runReference(t, ctx, engineEx, qex.ReferenceSolution)
			if res.RowCount != len(qex.Expected.Rows) {
				t.Errorf("exercise %s: reference returned %d rows, expected result has %d", qex.ID, res.RowCount, len(qex.Expected.Rows))
			}
			assertSameRows(t, qex.ID, qex.Expected.Rows, res.Rows)
		}
		if len(out.Chapters[ci].Exercises) != len(ch.Exercises) {
			t.Errorf("chapter %s reported %d exercises, plan has %d", ch.ID, len(out.Chapters[ci].Exercises), len(ch.Exercises))
		}
		for _, res := range out.Chapters[ci].Exercises {
			if res.Status != "verified" {
				t.Errorf("exercise %s status = %q, want verified", res.ID, res.Status)
			}
			if res.ReferenceRows == 0 || res.CrossCheckRows != res.ReferenceRows {
				t.Errorf("exercise %s evidence = %+v, want a measured reference and an agreeing cross-check", res.ID, res)
			}
			if res.FixturesTotal == 0 || res.FixturesRejected != res.FixturesTotal {
				t.Errorf("exercise %s fixtures = %d/%d, want every wrong query rejected", res.ID, res.FixturesRejected, res.FixturesTotal)
			}
		}
	}
	if exercises == 0 {
		t.Fatal("the activated course has no exercises")
	}
	if out.Evidence.ReferenceRows == 0 || out.Evidence.ScratchDatabase == "" {
		t.Errorf("evidence = %+v, want the rows measured and the environment named", out.Evidence)
	}

	// Every stage is reported with a detail, including the ones that were
	// skipped, so "did not run" and "ran and passed" cannot be confused.
	for _, name := range stageOrder {
		if stageDetail(out, name) == "" {
			t.Errorf("stage %s has no detail; the client renders it verbatim", name)
		}
	}
	assertArraysNotNull(t, out)

	// Stored as active, whole.
	rec, found, err := depot.Course(ctx, plan.ID)
	if err != nil || !found {
		t.Fatalf("Course(%q) found = %v err = %v", plan.ID, found, err)
	}
	if rec.Status != schema.StatusActive || len(rec.Plan.Chapters) != len(plan.Chapters) {
		t.Fatalf("stored course = %+v, want the active plan", rec.Status)
	}
	if len(rec.Outcome.Chapters) != len(out.Chapters) {
		t.Fatalf("stored outcome has %d chapter reports, want %d", len(rec.Outcome.Chapters), len(out.Chapters))
	}
}

// TestCourseGradingUsesTheMeasuredRows proves the expected rows are usable as a
// verdict, not decoration: the real evaluator passes the reference and fails a
// plausible wrong answer, using the plan's own expected result and contract.
func TestCourseGradingUsesTheMeasuredRows(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	svc := testService(NewFakeProvider(), newTestDepot(), ex)

	out := svc.GenerateCourse(ctx, schema.GenerationRequest{Title: "Practice grading", Difficulty: schema.Intermediate, ExerciseCount: 3})
	if out.Status != schema.StatusActive || out.Plan == nil {
		t.Fatalf("status = %q, want an active course (error = %v)", out.Status, out.Error)
	}
	plan := out.Plan
	first := plan.Chapters[0].Exercises[0]
	contract := evalContract(first.ResultContract)
	expected := eval.ExpectedResult{Columns: first.Expected.Columns, Rows: first.Expected.Rows}

	pass := eval.Evaluate(ctx, ex, eval.Request{
		SQL:      first.ReferenceSolution,
		Exercise: eval.Exercise{Prompt: first.Prompt, Contract: contract, Result: &expected},
	})
	if !pass.Passed {
		t.Fatalf("the course's own reference did not pass its own expected result: %+v", pass)
	}
	wrong := eval.Evaluate(ctx, ex, eval.Request{
		SQL:      "SELECT 1 AS " + quoteIdent(first.ResultContract.Columns[0]),
		Exercise: eval.Exercise{Prompt: first.Prompt, Contract: contract, Result: &expected},
	})
	if wrong.Passed {
		t.Fatal("a constant answer passed the course's expected result")
	}
}

// TestCourseGateRefusesPlanShapes pins the bounds. Each case names the field a
// repair would have to fix, because the message is what the next attempt reads.
func TestCourseGateRefusesPlanShapes(t *testing.T) {
	tooLong := strings.Repeat("x", minSectionChars-1)
	cases := []struct {
		name   string
		break_ func(*schema.CoursePlan)
		field  string
	}{
		{"stub section", func(p *schema.CoursePlan) { p.Chapters[0].Sections[0].Body = tooLong }, "chapters[0].sections[0].body"},
		{"no chapters", func(p *schema.CoursePlan) { p.Chapters = nil }, "chapters"},
		{"no exercises at all", func(p *schema.CoursePlan) {
			for ci := range p.Chapters {
				p.Chapters[ci].Exercises = nil
			}
		}, "chapters"},
		{"duplicate exercise id", func(p *schema.CoursePlan) { p.Chapters[1].Exercises[0].ID = p.Chapters[0].Exercises[0].ID }, "chapters[1].exercises[0].id"},
		{"drop in setup", func(p *schema.CoursePlan) { p.Setup.Statements = []string{"DROP TABLE dataset.orders"} }, "setup.statements"},
		{"two statements in one reference", func(p *schema.CoursePlan) {
			p.Chapters[0].Exercises[0].ReferenceSolution = "SELECT 1 AS \"customer_name\", 1 AS \"order_count\"; SELECT 2"
		}, "chapters[0].exercises[0].reference_solution"},
		{"empty expected columns", func(p *schema.CoursePlan) { p.Chapters[0].Exercises[0].ResultContract.Columns = nil }, "chapters[0].exercises[0].result_contract.columns"},
		{"setup dataset missing", func(p *schema.CoursePlan) { p.Setup.Dataset = " " }, "setup.dataset"},
		{"unknown dialect", func(p *schema.CoursePlan) { p.Dialect = "mysql" }, "dialect"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan := coursePlanFixture()
			tc.break_(&plan)
			verr := validateCoursePlan(&plan, DefaultLimits())
			if verr == nil {
				t.Fatalf("the plan was accepted; %s must be refused", tc.name)
			}
			if verr.Code != CodeInvalidArtifact {
				t.Errorf("code = %q, want %q", verr.Code, CodeInvalidArtifact)
			}
			if !strings.HasPrefix(verr.Detail, tc.field+": ") {
				t.Errorf("detail = %q, want it to start with %q", verr.Detail, tc.field+": ")
			}
		})
	}
}

// TestCourseGateAcceptsTheBuiltInPlan keeps the gate and the built-in provider
// from drifting: the plan the fake emits must pass the real gate.
func TestCourseGateAcceptsTheBuiltInPlan(t *testing.T) {
	raw, err := NewFakeProvider().courseBundle(coursePrompt(schema.GenerationRequest{Title: "analytics", Difficulty: schema.Intermediate}))
	if err != nil {
		t.Fatalf("courseBundle: %v", err)
	}
	var plan schema.CoursePlan
	if err := decodeJSON(raw, &plan); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if verr := validateCoursePlan(&plan, DefaultLimits()); verr != nil {
		t.Fatalf("the built-in course was refused by the gate: %s: %s", verr.Message, verr.Detail)
	}
	if len(plan.Chapters) == 0 {
		t.Fatal("the built-in course has no chapters")
	}
}

// TestCoursePromptCarriesTheSchemaAndTheDataset pins the two facts a provider
// cannot guess: what the environment already contains, and that a course is the
// unit of work.
func TestCoursePromptCarriesTheSchemaAndTheDataset(t *testing.T) {
	prompt := coursePrompt(schema.GenerationRequest{Title: "cohort retention", Difficulty: schema.Advanced, ExerciseCount: 6})
	if !strings.HasPrefix(prompt, coursePromptMarker) {
		t.Errorf("prompt does not open with the course marker: %q", prompt[:40])
	}
	// The columns come from the dataset, not from a hand-written list, so this
	// assertion is derived from the same source the brief is.
	tables, ok := dataset.Tables("commerce")
	if !ok || len(tables.Tables) == 0 {
		t.Fatal("the commerce dataset has no tables")
	}
	for _, table := range tables.Tables {
		if !strings.Contains(prompt, table.Name+"(") {
			t.Errorf("the prompt does not name table %q", table.Name)
		}
		for _, col := range table.Cols {
			if len(col) == 0 {
				continue
			}
			if !strings.Contains(prompt, col[0]) {
				t.Errorf("the prompt's description of %s omits column %q", table.Name, col[0])
			}
		}
	}
	if !strings.Contains(prompt, "cohort retention") {
		t.Error("prompt does not carry the learner's own goal")
	}
}

// runReference runs one statement on the test engine. The tests that use it are
// asserting about what the engine returns, so they run the statement rather than
// inspecting a stored copy of it.
func runReference(t *testing.T, ctx context.Context, ex engine.Executor, sql string) engine.QueryResult {
	t.Helper()
	res := engine.Execute(ctx, ex, sql, engine.DefaultLimits())
	if res.Status != "succeeded" {
		t.Fatalf("statement failed (%s): %s", res.Status, queryErrorText(res))
	}
	return res
}

// assertSameRows compares two result sets cell by cell. Values arrive from the
// driver already normalized to their wire shape (engine/normalize.go), so
// fmt.Sprint is the same comparison the grid makes.
func assertSameRows(t *testing.T, id string, want, got [][]any) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("exercise %s: %d rows in the plan, %d rows from the engine", id, len(want), len(got))
	}
	for i := range want {
		if len(want[i]) != len(got[i]) {
			t.Fatalf("exercise %s row %d: %d cells in the plan, %d from the engine", id, i, len(want[i]), len(got[i]))
		}
		for j := range want[i] {
			if fmt.Sprint(want[i][j]) != fmt.Sprint(got[i][j]) {
				t.Fatalf("exercise %s row %d cell %d: plan has %v, engine returned %v", id, i, j, want[i][j], got[i][j])
			}
		}
	}
}
