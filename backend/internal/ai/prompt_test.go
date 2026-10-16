package ai

import (
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// The brief a provider receives has to describe the schema the exercise will run
// against. It did not: the prompt said "the commerce dataset" and nothing more,
// and a real model wrote `o.order_id` for a column that is `o.id`, passing
// validate_artifact and the scratch environment before failing at
// reference_contract with the repair budget already spent.
//
// The expectation comes from the dataset package, not from a copy of the column
// list, so a column added to the seeder is required in the prompt by the same
// commit that adds it.
func TestRequestPromptNamesEveryTableAndColumn(t *testing.T) {
	prompt := requestPrompt(schema.GenerationRequest{Kind: "challenge", Difficulty: "beginner", Topics: []string{"joins"}, ExerciseCount: 1})
	tables, ok := dataset.Tables("commerce")
	if !ok || len(tables.Tables) == 0 {
		t.Fatal("the commerce dataset has no tables to describe")
	}
	for _, table := range tables.Tables {
		if !strings.Contains(prompt, table.Name+"(") {
			t.Errorf("the prompt does not name table %q", table.Name)
		}
		start := strings.Index(prompt, table.Name+"(")
		if start < 0 {
			continue // already reported above
		}
		body := prompt[start:]
		if end := strings.Index(body, ")"); end >= 0 {
			body = body[:end]
		}
		for _, col := range table.Cols {
			if len(col) == 0 {
				continue
			}
			if !strings.Contains(body, col[0]) {
				t.Errorf("the prompt's description of %s omits column %q", table.Name, col[0])
			}
		}
	}
}

// The setup rules the prompt states are the rules validateSetupStatement
// applies; an empty list is the correct answer over the sealed dataset, and
// saying otherwise is what produced `CREATE TABLE customers` against a database
// that already had it.
func TestRequestPromptAllowsAnEmptySetup(t *testing.T) {
	prompt := requestPrompt(schema.GenerationRequest{Kind: "challenge", Difficulty: "beginner", Topics: []string{"joins"}, ExerciseCount: 1})
	if !strings.Contains(prompt, "empty list") {
		t.Error("the prompt does not say a setup list may be empty")
	}
	if !strings.Contains(prompt, "Do not create, alter or drop these tables") {
		t.Error("the prompt does not forbid touching the loaded dataset tables")
	}
}

// A chapter body is the learner's reading material, so the rules that keep it
// readable are stated where both course paths read them: the system prompt of
// the whole-plan call and of the one-chapter agent call.
func TestTheCourseSystemPromptStatesTheReadingRules(t *testing.T) {
	prompt := courseSystemPrompt()
	for _, want := range []string{"for a beginner", "active voice", "do not restate its heading"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the course system prompt does not say %q", want)
		}
	}
}

// A hint and a critique are read as prose. The exercise prompt asks for one JSON
// object, so the two must not share it. This test and the request-level test in
// review_test.go are separate on purpose: this one holds the text, that one
// holds which call is sent it.
func TestTheFeedbackPromptAsksForProseNotJSON(t *testing.T) {
	const asksForJSONObject = "Reply with one JSON object"
	if strings.Contains(feedbackSystemPrompt(), asksForJSONObject) {
		t.Errorf("the feedback prompt asks for one JSON object: %q", feedbackSystemPrompt())
	}
	if !strings.Contains(systemPrompt(), asksForJSONObject) {
		t.Errorf("the exercise prompt no longer asks for one JSON object: %q", systemPrompt())
	}
}
