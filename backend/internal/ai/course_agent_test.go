package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The M8 agent's tests.
 *
 * The promise under test: a course is written one chapter at a time, in one
 * conversation, and each chapter is stored only after the engine ran it. The
 * tests below drive the conversation from a script rather than from a model, so
 * every claim is about the gateway's behaviour — what it persists, what it
 * refuses, what it hands back to the model to repair against.
 *
 * The negative cases matter as much as the happy one: a chapter that cannot be
 * repaired is dropped rather than allowed to end the course, a course in which
 * nothing verified is refused and stores nothing, and a model that loops is
 * bounded by the round budget. */

// scriptedAgent answers the outline call and then replays a queue of scripted
// turns, one per tool-calling request. It records every request, which is how a
// test reads the conversation the gateway actually had — including the tool
// results it sent back.
type scriptedAgent struct {
	outline string
	steps   []provider.ProviderResponse
	calls   []provider.ProviderRequest
}

func (a *scriptedAgent) Descriptor() provider.ProviderDescriptor {
	return provider.ProviderDescriptor{Name: "scripted", Model: "scripted-agent-v1", Version: "1", Configured: true}
}

func (a *scriptedAgent) Complete(_ context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	a.calls = append(a.calls, req)
	if len(req.Tools) == 0 {
		return provider.ProviderResponse{Text: a.outline, Provider: "scripted", Model: "scripted-agent-v1", FinishReason: "stop"}, nil
	}
	if len(a.steps) == 0 {
		// Nothing scripted left: a plain reply, which the loop answers with a
		// nudge rather than by inventing a tool call.
		return provider.ProviderResponse{Text: "I am done.", Provider: "scripted", Model: "scripted-agent-v1", FinishReason: "stop"}, nil
	}
	step := a.steps[0]
	a.steps = a.steps[1:]
	step.Provider, step.Model = "scripted", "scripted-agent-v1"
	return step, nil
}

// agentCalls is every tool-calling request the provider saw, in order.
func (a *scriptedAgent) agentCalls() []provider.ProviderRequest {
	var out []provider.ProviderRequest
	for _, req := range a.calls {
		if len(req.Tools) > 0 {
			out = append(out, req)
		}
	}
	return out
}

// lastToolMessages is the tool results the gateway sent on one turn.
func lastToolMessages(req provider.ProviderRequest) []string {
	var out []string
	for _, m := range req.Messages {
		if m.Role == "tool" && m.ToolCallID != "" {
			out = append(out, m.Content)
		}
	}
	return out
}

// saveChapterCall scripts one save_chapter turn.
func saveChapterCall(t *testing.T, ch schema.CourseChapter) provider.ProviderResponse {
	t.Helper()
	args, err := json.Marshal(ch)
	if err != nil {
		t.Fatalf("encode chapter: %v", err)
	}
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_" + ch.ID,
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolSaveChapter, Arguments: string(args)},
		}},
		FinishReason: "tool_calls",
	}
}

// saveChapterArgs scripts a save_chapter turn whose arguments are raw text, so
// a test can hand the gateway something the model could not have written.
func saveChapterArgs(id, args string) provider.ProviderResponse {
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_" + id,
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolSaveChapter, Arguments: args},
		}},
		FinishReason: "tool_calls",
	}
}

func finishCourseCall() provider.ProviderResponse {
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_finish_course",
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolFinishCourse, Arguments: `{"summary":"done"}`},
		}},
		FinishReason: "tool_calls",
	}
}

// outlineFor renders the outline of a plan fixture: exactly what the agent's
// first call asks for, derived from the plan the chapters come from.
func outlineFor(t *testing.T, plan schema.CoursePlan) string {
	t.Helper()
	outline := CourseOutline{
		Title:      plan.Title,
		Subtitle:   plan.Subtitle,
		Level:      plan.Level,
		Objectives: plan.Objectives,
		Setup:      plan.Setup,
	}
	for i, ch := range plan.Chapters {
		outline.Chapters = append(outline.Chapters, OutlineChapter{
			ID:         ch.ID,
			N:          i + 1,
			Title:      ch.Title,
			Summary:    ch.Summary,
			Kind:       ch.Kind,
			Minutes:    ch.Minutes,
			Objectives: ch.Objectives,
			Exercises:  len(ch.Exercises),
		})
	}
	raw, err := json.Marshal(outline)
	if err != nil {
		t.Fatalf("encode outline: %v", err)
	}
	return string(raw)
}

// crossCheckChapter deep-copies a chapter and replaces its first exercise's
// cross-check. The copy matters: a chapter holds slices, so a test that edited
// the fixture in place would hand the gateway the same broken chapter twice and
// prove nothing about repair.
func crossCheckChapter(t *testing.T, ch schema.CourseChapter, cross string) schema.CourseChapter {
	t.Helper()
	raw, err := json.Marshal(ch)
	if err != nil {
		t.Fatalf("encode chapter: %v", err)
	}
	var out schema.CourseChapter
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode chapter: %v", err)
	}
	out.Exercises[0].CrossCheckSolution = cross
	return out
}

// brokenCrossCheck is a cross-check that keeps one row instead of the whole
// answer, so it cannot agree with its reference under the contract. It is the
// M7 negative, re-used here as the chapter a repair has to fix.
const brokenCrossCheck = `SELECT c.name AS customer_name, 1 AS order_count FROM "dataset"."customers" c ORDER BY customer_name`

// TestTheOutlinePromptNamesTheChapterBudget pins the half of the fix a model
// reads: the outline call is where the chapter count is chosen, so it is where
// the rule "every chapter needs an exercise" has to be stated. coursePrompt —
// shared with the whole-plan path — must not grow the sentence, because the
// coupling only exists where chapters are chosen.
func TestTheOutlinePromptNamesTheChapterBudget(t *testing.T) {
	tests := []struct {
		exerciseCount int
		budget        int
	}{
		{0, 2},   // omitted: the course floor
		{1, 2},   // below the floor: raised to it
		{2, 2},   // at the floor
		{8, 8},   // inside the range: the learner's number stands
		{40, 12}, // above the ceiling: the per-course exercise bound
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("exercise_count=%d", tc.exerciseCount), func(t *testing.T) {
			req := schema.GenerationRequest{Title: "A budgeted course", Difficulty: schema.Intermediate, ExerciseCount: tc.exerciseCount}
			prompt := courseOutlinePrompt(req)
			want := fmt.Sprintf("budget is %d exercise(s)", tc.budget)
			if !strings.Contains(prompt, want) {
				t.Fatalf("the outline prompt does not name the budget %d:\n%s", tc.budget, prompt)
			}
			if !strings.Contains(prompt, fmt.Sprintf("at most %d chapter(s)", tc.budget)) {
				t.Fatalf("the outline prompt does not bound the chapter count at the budget %d", tc.budget)
			}
			// The shared whole-plan prompt carries the budget line but never
			// the chapter coupling: the whole-plan path is not the agent.
			if shared := coursePrompt(req); strings.Contains(shared, "at most") {
				t.Fatalf("coursePrompt grew the outline-only chapter rule:\n%s", shared)
			}
		})
	}
}

// outlineWithBudgetChapters renders an outline of n chapters, each planning one
// exercise — except that when emptyLast is set the final chapter plans none,
// which is exactly how a model that read "Exercises: N, spread across the
// chapters" as a total to divide expresses "the budget ran out before here".
func outlineWithBudgetChapters(t *testing.T, n int, emptyLast bool) string {
	t.Helper()
	outline := CourseOutline{
		Title:      "A budgeted course",
		Subtitle:   "One exercise per chapter, planned against an exact budget.",
		Level:      "intermediate",
		Objectives: []string{"Plan a course within its exercise budget."},
		Setup:      schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
	}
	for i := 0; i < n; i++ {
		exercises := 1
		if emptyLast && i == n-1 {
			exercises = 0
		}
		outline.Chapters = append(outline.Chapters, OutlineChapter{
			ID:         fmt.Sprintf("gen.ch%d", i+1),
			N:          i + 1,
			Title:      fmt.Sprintf("Chapter %d", i+1),
			Summary:    "One chapter of the budgeted course.",
			Objectives: []string{"Practise one thing."},
			Exercises:  exercises,
		})
	}
	raw, err := json.Marshal(outline)
	if err != nil {
		t.Fatalf("encode outline: %v", err)
	}
	return string(raw)
}

// TestARequestSmallerThanItsOutlineIsNeverBlamedOnTheSchema is the defect's
// regression: an exercise_count small enough that a multi-chapter outline
// cannot fill it must end in a successful run or a sentence that tells the
// learner what to ask for — never the raw provider-shaped
// "chapters[0].exercises: a chapter must plan at least one exercise".
//
// The rows cross the request's budget (exercise_count under the course floor and
// ceiling) with chapter counts that under-plan it, meet it, and over-plan it.
// The over-planning rows are the ones the old code refused with the schema
// error; the under-planning rows prove the successful path is unchanged.
func TestARequestSmallerThanItsOutlineIsNeverBlamedOnTheSchema(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)

	tests := []struct {
		name          string
		exerciseCount int
		chapters      int
		emptyLast     bool
		wantBudget    int
		wantActive    bool
	}{
		{"omitted budget, one chapter", 0, 1, false, 2, true},
		{"omitted budget, three chapters, one left empty", 0, 3, true, 2, false},
		{"one exercise, three chapters, one left empty", 1, 3, true, 2, false},
		{"two exercises, four chapters, one left empty", 2, 4, true, 2, false},
		{"two exercises, two chapters", 2, 2, false, 2, true},
		{"eight exercises, three chapters", 8, 3, false, 8, true},
		{"eight exercises, three chapters, one left empty", 8, 3, true, 8, false},
		{"eight exercises, eight chapters", 8, 8, false, 8, true},
		{"forty exercises clamps to the course ceiling", 40, 2, false, maxCourseExerciseTotal, true},
		{"forty exercises, one chapter left empty", 40, 4, true, maxCourseExerciseTotal, false},
		{"one exercise, eight chapters, one left empty", 1, 8, true, 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			depot := newTestDepot()
			outline := outlineWithBudgetChapters(t, tc.chapters, tc.emptyLast)

			// Chapters to hand in for the rows whose outline the gate accepts.
			// The empty chapter is never written, because a chapter that plans
			// no exercise is not a chapter the model can save.
			steps := make([]provider.ProviderResponse, 0, tc.chapters+1)
			for i := 0; i < tc.chapters; i++ {
				if tc.emptyLast && i == tc.chapters-1 {
					break
				}
				ch := chapterFixture(fmt.Sprintf("gen.ch%d", i+1), i+1, fakeTopics[i%len(fakeTopics)])
				steps = append(steps, saveChapterCall(t, ch))
			}
			steps = append(steps, finishCourseCall())

			agent := &scriptedAgent{outline: outline, steps: steps}
			svc := testService(agent, depot, ex)
			out := svc.GenerateCourse(ctx, schema.GenerationRequest{
				Title:         "A course within a small budget",
				Difficulty:    schema.Intermediate,
				ExerciseCount: tc.exerciseCount,
			})

			if tc.wantActive {
				if out.Status != schema.StatusActive {
					t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusActive, out.Error)
				}
				if out.Plan == nil || len(out.Plan.Chapters) != tc.chapters {
					t.Fatalf("active course has %v chapters, want the %d the outline planned", out.Plan, tc.chapters)
				}
				// This row is a request that already worked: it must still work.
				if calls := agent.agentCalls(); len(calls) == 0 {
					t.Fatal("an accepted outline was never asked to write a chapter")
				}
				return
			}

			if out.Status != schema.StatusFailed {
				t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusFailed, out.Error)
			}
			if out.Error == nil {
				t.Fatal("a refused outline must name the reason")
			}
			if out.Error.Code != CodeInvalidArtifact || out.Error.Stage != schema.StageValidateArtifact {
				t.Fatalf("error = %+v, want %s at %s", out.Error, CodeInvalidArtifact, schema.StageValidateArtifact)
			}
			detail := out.Error.Detail
			// The defect, verbatim: a request the contract permits answered
			// with the provider's own field path.
			if strings.Contains(detail, "chapters[0].exercises") || strings.Contains(out.Error.Message, "chapters[") {
				t.Fatalf("the learner was handed the provider's schema path: %s", detail)
			}
			// The fix: the message names the requested budget and what to ask
			// for instead.
			if !strings.Contains(detail, fmt.Sprintf("%d exercise(s)", tc.wantBudget)) {
				t.Fatalf("detail = %q, want it to name the requested budget %d", detail, tc.wantBudget)
			}
			if !strings.Contains(detail, "ask for at least") && !strings.Contains(detail, "give every chapter") {
				t.Fatalf("detail = %q, want an instruction the learner can act on", detail)
			}
			// The outline is refused before any chapter is asked for, so the
			// run costs the provider nothing beyond the outline call itself.
			if calls := agent.agentCalls(); len(calls) != 0 {
				t.Fatalf("the gateway asked for %d chapter(s) after refusing the outline", len(calls))
			}
			if n := depot.courseCount(); n != 0 {
				t.Fatalf("a refused outline left %d course(s) stored", n)
			}
		})
	}
}

// TestTheRefusedOutlineNamesTheChapterCountWhenTheBudgetCannotReachIt: when the
// outline plans more chapters than the budget has exercises, the mismatch is
// structural, and the actionable sentence has to say so — it names the number
// to ask for. This is the exact request from progress.md: one exercise, more
// chapters than one.
func TestTheRefusedOutlineNamesTheChapterCountWhenTheBudgetCannotReachIt(t *testing.T) {
	// No engine: the outline is refused before a scratch environment is opened,
	// which is itself the claim — an unsatisfiable request costs no database.
	depot := newTestDepot()
	agent := &scriptedAgent{outline: outlineWithBudgetChapters(t, 3, true)}
	svc := testService(agent, depot, nil)

	out := svc.GenerateCourse(testContext(t), schema.GenerationRequest{
		Title:         "One exercise, three chapters",
		Difficulty:    schema.Intermediate,
		ExerciseCount: 1,
	})
	if out.Status != schema.StatusFailed || out.Error == nil {
		t.Fatalf("outcome = %+v, want a failure that names the reason", out)
	}
	if !strings.Contains(out.Error.Detail, "at least 3 exercise(s)") {
		t.Fatalf("detail = %q, want it to name the number of exercises that would fit", out.Error.Detail)
	}
	if !strings.Contains(out.Error.Detail, "2 exercise(s)") {
		t.Fatalf("detail = %q, want it to name the requested budget (the floor, 2)", out.Error.Detail)
	}
	if n := depot.courseCount(); n != 0 {
		t.Fatalf("the refused request stored %d course(s)", n)
	}
}

// TestCourseOutlineIsGatedBeforeAnyChapterIsAsked: the outline is validated
// against the same bounds the whole-plan gate applies, and a bad one stops the
// run before a single chapter is asked for.
func TestCourseOutlineIsGatedBeforeAnyChapterIsAsked(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	plan := coursePlanFixture()
	agent := &scriptedAgent{outline: outlineFor(t, plan)}
	// Five exercises in one chapter is over the per-chapter bound. The scope is
	// the outline's own field, so the chapter count in the outline is what the
	// gate reads.
	var outline CourseOutline
	if err := json.Unmarshal([]byte(agent.outline), &outline); err != nil {
		t.Fatalf("decode the test's own outline: %v", err)
	}
	outline.Chapters[0].Exercises = maxCourseExercises + 1
	raw, err := json.Marshal(outline)
	if err != nil {
		t.Fatalf("encode outline: %v", err)
	}
	agent.outline = string(raw)
	svc := testService(agent, newTestDepot(), ex)

	out := svc.GenerateCourse(ctx, schema.GenerationRequest{Title: "An outline the gate refuses", Difficulty: schema.Intermediate})
	if out.Status != schema.StatusFailed {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusFailed)
	}
	if out.Error == nil || out.Error.Code != CodeInvalidArtifact || out.Error.Stage != schema.StageValidateArtifact {
		t.Fatalf("error = %+v, want %s at %s", out.Error, CodeInvalidArtifact, schema.StageValidateArtifact)
	}
	if !strings.Contains(out.Error.Detail, "exercises") {
		t.Errorf("detail = %q, want it to name the field the outline got wrong", out.Error.Detail)
	}
	if calls := agent.agentCalls(); len(calls) != 0 {
		t.Fatalf("the gateway asked for %d chapter(s) after refusing the outline", len(calls))
	}
	if len(agent.calls) != 1 {
		t.Fatalf("provider calls = %d, want exactly the outline ask", len(agent.calls))
	}
}

// TestTheAgentRepairsAChapterFromTheEnginesMessage is the reason the loop is a
// conversation: the tool result carries the engine's own message, and the
// chapter that comes back fixed is accepted and stored with its measured rows.
func TestTheAgentRepairsAChapterFromTheEnginesMessage(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()

	broken := crossCheckChapter(t, plan.Chapters[0], brokenCrossCheck)
	good := plan.Chapters[0]

	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps:   []provider.ProviderResponse{saveChapterCall(t, broken), saveChapterCall(t, good), finishCourseCall()},
	}
	svc := testService(agent, depot, ex)

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A chapter that has to be repaired"))
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusActive, out.Error)
	}
	if out.Plan == nil {
		t.Fatal("an active course must carry its plan")
	}

	turns := agent.agentCalls()
	if len(turns) < 3 {
		t.Fatalf("tool-calling turns = %d, want the refused write, the repair and the close", len(turns))
	}
	// The second turn carries the engine's verdict on the first write: the
	// gateway answers a save_chapter with what it measured, and the refusal is
	// what the next turn is written against.
	repaired := lastToolMessages(turns[1])
	if len(repaired) != 1 {
		t.Fatalf("the repair turn answered with %d tool result(s), want 1", len(repaired))
	}
	if !strings.Contains(repaired[0], `"status":"refused"`) || !strings.Contains(repaired[0], `"stage":"cross_check"`) {
		t.Fatalf("the refusal did not name the stage the engine refused at: %s", repaired[0])
	}
	if !strings.Contains(repaired[0], "disagree") {
		t.Fatalf("the refusal did not carry the evaluator's own message: %s", repaired[0])
	}
	// And it names the exercise the engine stopped at: a repair against "the
	// chapter disagrees somewhere" is a rewrite; a repair against one exercise
	// is a fix.
	if want := `"exercise":"` + plan.Chapters[0].Exercises[0].ID + `"`; !strings.Contains(repaired[0], want) {
		t.Fatalf("the refusal did not name the exercise the engine refused (%s): %s", want, repaired[0])
	}

	// One conversation: the last turn still carries the result of the first
	// write, so the model repairs with the whole history in front of it.
	last := turns[len(turns)-1]
	if len(last.Messages) <= len(turns[0].Messages) {
		t.Fatalf("the conversation did not grow: %d messages then, %d now", len(turns[0].Messages), len(last.Messages))
	}
	if !strings.Contains(strings.Join(lastToolMessages(last), "\n"), `"stage":"cross_check"`) {
		t.Fatal("the last turn lost the engine's earlier verdict: this is not one conversation")
	}

	// The repaired chapter is in the plan with the rows the engine measured.
	plan1 := out.Plan.Chapters[0]
	if len(plan1.Sections) == 0 || len(plan1.Exercises) == 0 {
		t.Fatalf("the stored chapter has no content: %+v", plan1)
	}
	first2 := plan1.Exercises[0]
	if first2.Expected == nil || len(first2.Expected.Rows) == 0 {
		t.Fatal("the stored exercise has no measured rows: the grader has nothing to compare against")
	}
	if out.Plan.Provenance.Repairs != 1 {
		t.Errorf("provenance repairs = %d, want 1: the in-session repair is part of the course's history", out.Plan.Provenance.Repairs)
	}
	// The stored record is the active course, readable by id.
	rec, found, err := depot.Course(ctx, out.Plan.ID)
	if err != nil || !found || rec.Status != schema.StatusActive {
		t.Fatalf("stored course = %+v (found = %v, err = %v), want the active plan", rec.Status, found, err)
	}
}

// TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten: the point of
// streaming. After chapter one verifies, the course is already in the store
// under "building" with that chapter's content and the planned shape of the
// chapters that do not exist yet.
func TestACourseIsStoredWhileItsLaterChaptersAreStillBeingWritten(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps:   []provider.ProviderResponse{saveChapterCall(t, plan.Chapters[0]), saveChapterCall(t, plan.Chapters[1]), finishCourseCall()},
	}
	svc := testService(agent, depot, ex)

	// A closure that reads the store at the moment the second chapter is being
	// handed in — that is the moment the first chapter is on disk and the
	// second is not.
	var (
		seen      schema.CourseRecord
		seenFound bool
		seenAt    int
	)
	svc.cfg.Provider = &observingAgent{inner: agent, onTurn: func(turn int) {
		if turn != 2 {
			return
		}
		recs, err := depot.Courses(ctx)
		if err != nil || len(recs) == 0 {
			return
		}
		seen = recs[0]
		seenFound = true
		seenAt = turn
	}}

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A course written while you watch"))
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusActive, out.Error)
	}
	if !seenFound || seenAt != 2 {
		t.Fatalf("the store was never read while the second chapter was being written (found = %v, turn = %d)", seenFound, seenAt)
	}
	if seen.Status != schema.StatusBuilding {
		t.Fatalf("the course read mid-run is %q, want %q", seen.Status, schema.StatusBuilding)
	}
	if seen.Plan.ID != out.Plan.ID {
		t.Fatalf("the mid-run course has id %q, the finished one %q", seen.Plan.ID, out.Plan.ID)
	}
	if len(seen.Plan.Chapters) != len(plan.Chapters) {
		t.Fatalf("the mid-run course has %d chapters, want the %d the outline planned", len(seen.Plan.Chapters), len(plan.Chapters))
	}
	if len(seen.Plan.Chapters[0].Sections) == 0 {
		t.Error("the verified chapter is not readable mid-run: the first chapter is missing its content")
	}
	if len(seen.Plan.Chapters[1].Sections) != 0 {
		t.Error("the chapter that has not been written is carrying content")
	}
	if len(seen.Outcome.Chapters) != 1 || seen.Outcome.Chapters[0].Status != ChapterVerified {
		t.Fatalf("the mid-run report = %+v, want exactly the verified first chapter", seen.Outcome.Chapters)
	}
}

// TestAChapterThatNeverVerifiesIsDroppedAndTheRestIsKept: the course is smaller,
// not absent. The dropped chapter is reported with the engine's verdict against
// it, and the chapters that did verify are stored and active.
func TestAChapterThatNeverVerifiesIsDroppedAndTheRestIsKept(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	broken := crossCheckChapter(t, plan.Chapters[1], brokenCrossCheck)

	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps: []provider.ProviderResponse{
			saveChapterCall(t, plan.Chapters[0]),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			finishCourseCall(),
		},
	}
	svc := testService(agent, depot, ex)

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A course with one chapter that will not run"))
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusActive, out.Error)
	}
	if out.Plan == nil {
		t.Fatal("an active course must carry its plan")
	}
	if len(out.Plan.Chapters) != 1 || out.Plan.Chapters[0].ID != plan.Chapters[0].ID {
		t.Fatalf("the stored course has chapters %v, want only the one that verified", chapterIDsOf(out.Plan.Chapters))
	}
	if len(out.Chapters) != 2 {
		t.Fatalf("the report has %d chapters, want one entry per planned chapter", len(out.Chapters))
	}
	if out.Chapters[0].Status != ChapterVerified {
		t.Errorf("chapter %s = %q, want %q", out.Chapters[0].ID, out.Chapters[0].Status, ChapterVerified)
	}
	dropped := out.Chapters[1]
	if dropped.Status != ChapterDropped {
		t.Fatalf("chapter %s = %q, want %q", dropped.ID, dropped.Status, ChapterDropped)
	}
	if dropped.Error == nil || dropped.Error.Stage != schema.StageCrossCheck {
		t.Fatalf("the dropped chapter does not name the stage that refused it: %+v", dropped.Error)
	}
	failed := false
	for _, exercise := range dropped.Exercises {
		if exercise.Status == "failed" && exercise.Error != nil {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("the dropped chapter names no failing exercise: %+v", dropped.Exercises)
	}
	// The model was told the chapter was dropped, not asked to repair it
	// again: the verdict on the third write is a drop, and the turn after it
	// carries that drop rather than another refusal.
	turns := agent.agentCalls()
	last := turns[len(turns)-1]
	told := lastToolMessages(last)[len(lastToolMessages(last))-1]
	if !strings.Contains(told, `"status":"dropped"`) {
		t.Fatalf("the model was never told the chapter was dropped: %s", told)
	}
	// The drop reached the store: no unverified exercise is in the record.
	rec, found, err := depot.Course(ctx, out.Plan.ID)
	if err != nil || !found {
		t.Fatalf("the active course is not stored: found = %v err = %v", found, err)
	}
	for _, ch := range rec.Plan.Chapters {
		if len(ch.Exercises) == 0 {
			t.Fatalf("the stored course contains a chapter with no verified exercises: %+v", ch)
		}
	}
}

// TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored: streaming must
// not leave a course behind that nothing verified. The row written for the
// outline is deleted, so the learner is never offered a course with no content.
func TestACourseWithNoVerifiedChapterIsRefusedAndNothingIsStored(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	plan.Chapters = plan.Chapters[:1]
	broken := crossCheckChapter(t, plan.Chapters[0], brokenCrossCheck)

	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps: []provider.ProviderResponse{
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			saveChapterCall(t, broken),
			finishCourseCall(),
		},
	}
	svc := testService(agent, depot, ex)

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A course that never verifies"))
	if out.Status != schema.StatusRejected {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusRejected, out.Error)
	}
	if out.Plan != nil {
		t.Fatal("a refused course must not return a plan")
	}
	if out.Error == nil || out.Error.Code != CodeCrossCheckFailed {
		t.Fatalf("error = %+v, want %s", out.Error, CodeCrossCheckFailed)
	}
	if n := depot.courseCount(); n != 0 {
		t.Fatalf("a refused course left %d record(s) stored", n)
	}
	if len(out.Chapters) != 1 || out.Chapters[0].Status != ChapterDropped {
		t.Fatalf("report = %+v, want the dropped chapter", out.Chapters)
	}
}

// TestTheRunBudgetEndsAStuckLoop: a model that never writes a chapter it was
// asked for ends the run at the round budget instead of looping forever. The
// course is refused because nothing verified, and nothing is stored.
func TestTheRunBudgetEndsAStuckLoop(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	agent := &scriptedAgent{outline: outlineFor(t, plan)}
	// Every turn asks to save a chapter the outline never named. Nothing is
	// ever verified, so only the round budget can end this.
	agent.steps = nil
	svc := testService(&loopingAgent{scriptedAgent: agent}, depot, ex)

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A model that will not write a chapter"))
	if out.Status != schema.StatusRejected {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusRejected, out.Error)
	}
	if got := len(agent.agentCalls()); got != maxAgentRounds {
		t.Fatalf("tool-calling turns = %d, want the budget of %d", got, maxAgentRounds)
	}
	if n := depot.courseCount(); n != 0 {
		t.Fatalf("a run that verified nothing left %d record(s) stored", n)
	}
	for _, ch := range out.Chapters {
		if ch.Status != ChapterSkipped {
			t.Fatalf("chapter %s = %q, want %q: no chapter was ever written", ch.ID, ch.Status, ChapterSkipped)
		}
	}
}

// TestASaveChapterWithUnusableArgumentsIsAnsweredNotCrashing: provider output is
// untrusted input, so a call the gateway cannot read is a tool result the model
// can repair against rather than a panic or an empty turn.
func TestASaveChapterWithUnusableArgumentsIsAnsweredNotCrashing(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	plan := coursePlanFixture()
	agent := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps: []provider.ProviderResponse{
			saveChapterArgs("garbage", "this is not json"),
			saveChapterCall(t, plan.Chapters[0]),
			finishCourseCall(),
		},
	}
	svc := testService(agent, newTestDepot(), ex)

	out := svc.GenerateCourse(ctx, exerciseTitleRequest("A chapter the model mangled"))
	turns := agent.agentCalls()
	if len(turns) == 0 {
		t.Fatal("the gateway never entered the chapter conversation")
	}
	answered := strings.Join(lastToolMessages(turns[1]), "\n")
	if !strings.Contains(answered, `"status":"invalid"`) {
		t.Fatalf("unreadable arguments were not reported back as invalid: %s", answered)
	}
	if out.Status != schema.StatusActive {
		t.Fatalf("status = %q, want the course to continue after the bad call (%+v)", out.Status, out.Error)
	}
}

/* ---------- observing helpers ---------- */

// exerciseTitleRequest is the tiny request these tests drive the agent with: a
// goal and nothing else, which is what the prompt is written for.
func exerciseTitleRequest(title string) schema.GenerationRequest {
	return schema.GenerationRequest{Title: title, Difficulty: schema.Intermediate}
}

// observingAgent wraps a scripted agent and calls onTurn before each
// tool-calling request, which is how a test reads the store mid-run.
type observingAgent struct {
	inner  provider.Provider
	onTurn func(turn int)
	turn   int
}

func (a *observingAgent) Descriptor() provider.ProviderDescriptor { return a.inner.Descriptor() }

func (a *observingAgent) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	if len(req.Tools) > 0 {
		a.turn++
		if a.onTurn != nil {
			a.onTurn(a.turn)
		}
	}
	return a.inner.Complete(ctx, req)
}

// loopingAgent always asks to save a chapter the outline never named, so no
// chapter is ever resolved.
type loopingAgent struct{ *scriptedAgent }

func (a *loopingAgent) Complete(_ context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	a.calls = append(a.calls, req)
	if len(req.Tools) == 0 {
		return provider.ProviderResponse{Text: a.outline, FinishReason: "stop"}, nil
	}
	return provider.ProviderResponse{
		ToolCalls: []provider.ToolCall{{
			ID:       "call_unknown",
			Type:     "function",
			Function: provider.ToolCallFunction{Name: toolSaveChapter, Arguments: `{"id":"not-in-the-outline"}`},
		}},
		FinishReason: "tool_calls",
	}, nil
}

// chapterIDsOf names the chapters of a plan, for a failure message.
func chapterIDsOf(chapters []schema.CourseChapter) []string {
	out := make([]string, 0, len(chapters))
	for _, ch := range chapters {
		out = append(out, ch.ID)
	}
	return out
}

/* ---------- M10: the outline pause and the build that continues it ---------- */

// TestTheOutlinePhaseStopsAwaitingApproval is the pause: a run that asks for
// approval validates the outline, stores it as a `building` course, and stops
// — no chapter conversation is entered, and the run reports awaiting_approval.
// The stored row is the outline, with its chapter titles, and it names the id
// the learner will approve.
func TestTheOutlinePhaseStopsAwaitingApproval(t *testing.T) {
	// No engine: the pause happens before any chapter, so no scratch database
	// is opened. That the test passes with a nil executor is the claim.
	depot := newTestDepot()
	plan := coursePlanFixture()
	agent := &scriptedAgent{outline: outlineFor(t, plan)}
	svc := testService(agent, depot, nil)

	req := exerciseTitleRequest("A course whose outline I want to approve")
	req.RequireApproval = true
	out := svc.GenerateCourse(testContext(t), req)

	if out.Status != schema.StatusAwaitingApproval {
		t.Fatalf("status = %q, want %q (error = %+v)", out.Status, schema.StatusAwaitingApproval, out.Error)
	}
	if out.Error != nil {
		t.Fatalf("a pause is not a failure, but it carries an error: %+v", out.Error)
	}
	if out.Plan == nil {
		t.Fatal("an awaiting-approval run must name the stored outline it is asking about")
	}
	// The chapter conversation never started: only the outline call was made.
	if calls := agent.agentCalls(); len(calls) != 0 {
		t.Fatalf("the paused run asked for %d chapter(s) before approval", len(calls))
	}
	if len(agent.calls) != 1 {
		t.Fatalf("provider calls = %d, want exactly the outline ask", len(agent.calls))
	}

	// The stored row is a building course carrying the outline's chapters.
	rec, found, err := depot.Course(testContext(t), out.Plan.ID)
	if err != nil || !found {
		t.Fatalf("the paused run stored nothing: found = %v err = %v", found, err)
	}
	if rec.Status != schema.StatusBuilding {
		t.Fatalf("the stored course is %q, want %q", rec.Status, schema.StatusBuilding)
	}
	if len(rec.Plan.Chapters) != len(plan.Chapters) {
		t.Fatalf("the stored course has %d chapters, want the %d the outline planned", len(rec.Plan.Chapters), len(plan.Chapters))
	}
	for i, ch := range rec.Plan.Chapters {
		if ch.Title != plan.Chapters[i].Title {
			t.Errorf("stored chapter %d is %q, want the outline's %q", i, ch.Title, plan.Chapters[i].Title)
		}
		if len(ch.Sections) != 0 || len(ch.Exercises) != 0 {
			t.Errorf("stored chapter %q carries content before approval: %+v", ch.ID, ch)
		}
	}
	// The request is stored beside it, because the chapter phase rebuilds its
	// prompt from the learner's own words and the plan does not keep them.
	if rec.Request == nil || rec.Request.Title != req.Title {
		t.Fatalf("the stored request = %+v, want the one that produced the outline", rec.Request)
	}
}

// TestApprovingAPausedCourseRunsTheChapterPhase: the build continues the stored
// outline to an active course, with the engine's measured rows, and the new run
// is the same run the pause produced (same course id).
func TestApprovingAPausedCourseRunsTheChapterPhase(t *testing.T) {
	ex := bootPG(t)
	depot := newTestDepot()
	plan := coursePlanFixture()

	// Phase 1: pause on the outline.
	paused := &scriptedAgent{outline: outlineFor(t, plan)}
	svc := testService(paused, depot, nil)
	req := exerciseTitleRequest("A course built after its outline is approved")
	req.RequireApproval = true
	out := svc.GenerateCourse(testContext(t), req)
	if out.Status != schema.StatusAwaitingApproval || out.Plan == nil {
		t.Fatalf("the pause did not happen: status = %q plan = %+v", out.Status, out.Plan)
	}

	// Phase 2: approve. The provider now answers the chapter conversation, and
	// a scratch environment exists to verify in.
	steps := make([]provider.ProviderResponse, 0, len(plan.Chapters)+1)
	for _, ch := range plan.Chapters {
		steps = append(steps, saveChapterCall(t, ch))
	}
	steps = append(steps, finishCourseCall())
	approve := &scriptedAgent{steps: steps}
	approveSvc := testService(approve, depot, ex)

	built := approveSvc.BuildCourse(testContext(t), out.Plan.ID)
	if built.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", built.Status, schema.StatusActive, built.Error)
	}
	if built.Plan == nil || built.Plan.ID != out.Plan.ID {
		t.Fatalf("the built course = %+v, want the paused course %s", built.Plan, out.Plan.ID)
	}
	// The chapter conversation began with the resumed request, not a blank one.
	turns := approve.agentCalls()
	if len(turns) == 0 {
		t.Fatal("approving the course never entered the chapter conversation")
	}
	if !strings.Contains(turns[0].Messages[1].Content, req.Title) {
		t.Errorf("the resumed prompt does not carry the learner's goal %q", req.Title)
	}
	// Every stored chapter carries its measured rows.
	for _, ch := range built.Plan.Chapters {
		for _, exercise := range ch.Exercises {
			if exercise.Expected == nil || len(exercise.Expected.Rows) == 0 {
				t.Fatalf("built exercise %s has no measured rows", exercise.ID)
			}
		}
	}
	// And the row is active on the next read.
	rec, found, err := depot.Course(testContext(t), out.Plan.ID)
	if err != nil || !found || rec.Status != schema.StatusActive {
		t.Fatalf("stored course = %q (found = %v, err = %v), want %q", rec.Status, found, err, schema.StatusActive)
	}
}

// TestAnAlreadyVerifiedChapterIsNotAskedForAgain: resume must not spend the
// chapter phase re-writing what the store already has. A row with one verified
// chapter is continued, and the provider is asked only for the rest.
func TestAnAlreadyVerifiedChapterIsNotAskedForAgain(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()

	// A chapter only carries measured rows if it was verified by a real run, so
	// first produce one: a complete build against the engine gives both
	// chapters their expected rows.
	full := &scriptedAgent{
		outline: outlineFor(t, plan),
		steps:   []provider.ProviderResponse{saveChapterCall(t, plan.Chapters[0]), saveChapterCall(t, plan.Chapters[1]), finishCourseCall()},
	}
	firstSvc := testService(full, newTestDepot(), ex)
	fullOut := firstSvc.GenerateCourse(ctx, exerciseTitleRequest("A course that verifies both chapters"))
	if fullOut.Status != schema.StatusActive || fullOut.Plan == nil {
		t.Fatalf("the setup build did not activate: status = %q error = %+v", fullOut.Status, fullOut.Error)
	}

	// Now the building row an interrupted run would leave: chapter one verified
	// and stored with its measured rows, chapter two still an outline skeleton.
	verified := fullOut.Plan.Chapters[0]
	skeleton := schema.CourseChapter{ID: plan.Chapters[1].ID, N: plan.Chapters[1].N, Title: plan.Chapters[1].Title, Summary: plan.Chapters[1].Summary, Objectives: plan.Chapters[1].Objectives, Sections: []schema.CourseSection{}, Exercises: []schema.CourseExercise{}}
	rec := schema.CourseRecord{
		Plan:    schema.CoursePlan{ID: fullOut.Plan.ID, Version: 1, Title: fullOut.Plan.Title, Subtitle: fullOut.Plan.Subtitle, Level: fullOut.Plan.Level, Dialect: "postgresql", Objectives: fullOut.Plan.Objectives, Setup: fullOut.Plan.Setup, Chapters: []schema.CourseChapter{verified, skeleton}},
		Status:  schema.StatusBuilding,
		Request: &schema.GenerationRequest{Title: "A course interrupted after one chapter", Difficulty: schema.Intermediate, Kind: "course"},
		Outcome: schema.Outcome{Status: schema.StatusBuilding, Kind: "course", Chapters: []schema.ChapterResult{
			{ID: verified.ID, Title: verified.Title, Status: ChapterVerified, Exercises: []schema.ExerciseResult{{ID: verified.Exercises[0].ID, Status: "verified"}}},
		}},
	}
	if err := depot.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("seed the interrupted course: %v", err)
	}

	// Only chapter two is handed in; if the gateway asked for chapter one again
	// the scripted queue would run dry and the run would not finish.
	resume := &scriptedAgent{steps: []provider.ProviderResponse{saveChapterCall(t, plan.Chapters[1]), finishCourseCall()}}
	resumeSvc := testService(resume, depot, ex)
	built := resumeSvc.BuildCourse(ctx, rec.Plan.ID)
	if built.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", built.Status, schema.StatusActive, built.Error)
	}
	calls := resume.agentCalls()
	if len(calls) == 0 {
		t.Fatal("the resume never entered the chapter conversation")
	}
	// The opening turn names the already-stored chapter so the model does not
	// write it again.
	joined := ""
	for _, m := range calls[0].Messages {
		joined += m.Content
	}
	if !strings.Contains(joined, verified.ID) {
		t.Errorf("the resumed conversation does not say chapter %s is already stored", verified.ID)
	}
	if !strings.Contains(joined, `"status":"verified"`) {
		t.Errorf("the resumed conversation does not report the stored chapter as verified")
	}
	// Both chapters are in the final plan, and neither lost its rows.
	if len(built.Plan.Chapters) != 2 {
		t.Fatalf("the built course has %d chapters, want 2", len(built.Plan.Chapters))
	}
	for _, ch := range built.Plan.Chapters {
		for _, exr := range ch.Exercises {
			if exr.Expected == nil || len(exr.Expected.Rows) == 0 {
				t.Fatalf("built exercise %s has no measured rows", exr.ID)
			}
		}
	}
	// The chapter already stored was not rewritten: its measured rows are the
	// original run's.
	if got := built.Plan.Chapters[0].Exercises[0].Expected.Rows; len(got) == 0 {
		t.Fatal("the skipped chapter lost the rows the first run measured")
	}
}

// TestBuildRefusesAnUnknownCourse: a build for a row that is not there reports
// NOT_FOUND rather than inventing a course or crashing.
func TestBuildRefusesAnUnknownCourse(t *testing.T) {
	svc := testService(&scriptedAgent{}, newTestDepot(), nil)
	out := svc.BuildCourse(testContext(t), "course-does-not-exist")
	if out.Status != schema.StatusFailed {
		t.Fatalf("status = %q, want %q", out.Status, schema.StatusFailed)
	}
	if out.Error == nil || out.Error.Code != CodeNotFound {
		t.Fatalf("error = %+v, want %s", out.Error, CodeNotFound)
	}
}

// TestALegacyRecordWithoutARequestIsStillBuildable: a course stored before the
// request was persisted has none, and the resume must still work — the goal is
// recovered from the plan's title and the rest is left at its defaults rather
// than invented.
func TestALegacyRecordWithoutARequestIsStillBuildable(t *testing.T) {
	ex := bootPG(t)
	ctx := testContext(t)
	depot := newTestDepot()
	plan := coursePlanFixture()
	plan.ID = "course-legacy-no-request"
	rec := schema.CourseRecord{
		Plan:      plan,
		Status:    schema.StatusBuilding,
		CreatedAt: time.Now().UTC(),
		Outcome:   schema.Outcome{Status: schema.StatusBuilding, Kind: "course", Plan: &plan, Chapters: []schema.ChapterResult{}},
	}
	if err := depot.SaveCourse(ctx, rec); err != nil {
		t.Fatalf("seed the legacy course: %v", err)
	}
	steps := make([]provider.ProviderResponse, 0, len(plan.Chapters)+1)
	for _, ch := range plan.Chapters {
		steps = append(steps, saveChapterCall(t, ch))
	}
	steps = append(steps, finishCourseCall())
	agent := &scriptedAgent{steps: steps}
	svc := testService(agent, depot, ex)

	built := svc.BuildCourse(ctx, plan.ID)
	if built.Status != schema.StatusActive {
		t.Fatalf("status = %q, want %q (error = %+v)", built.Status, schema.StatusActive, built.Error)
	}
	req := requestFromRecord(rec)
	if req.Title != plan.Title {
		t.Errorf("a legacy record's goal = %q, want the plan title %q", req.Title, plan.Title)
	}
	if req.Kind != "course" {
		t.Errorf("recovered kind = %q, want course", req.Kind)
	}
	if req.Difficulty != schema.Intermediate {
		t.Errorf("recovered difficulty = %q, want the default %q", req.Difficulty, schema.Intermediate)
	}
	// The exercise budget is the one thing the plan can speak to: it counts the
	// exercises the stored chapters carry. Nothing else is invented.
	if req.ExerciseCount != exerciseCount(&plan) {
		t.Errorf("recovered exercise count = %d, want the plan's own %d", req.ExerciseCount, exerciseCount(&plan))
	}
	if req.Scenario != "" || len(req.Topics) != 0 {
		t.Errorf("recovered request = %+v, want the fields the plan cannot know left empty", req)
	}
}
