package ai

import (
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// baselineArtifact is a minimal bundle that passes validateArtifact. Each case
// mutates exactly one field, so a failure names the field that caused it.
func baselineArtifact() schema.Artifact {
	return schema.Artifact{
		Title:              "Test challenge",
		Prompt:             "Select the constant one.",
		Difficulty:         schema.Beginner,
		Objectives:         []string{"Select a literal."},
		Hints:              []string{"Use SELECT."},
		Setup:              schema.SetupPlan{Dataset: "commerce", SchemaVersion: 1, SeedVersion: 1, Statements: []string{}},
		ReferenceSolution:  `SELECT 1 AS n`,
		CrossCheckSolution: `SELECT (1 + 0) AS n`,
		ResultContract: schema.ResultContract{
			Columns:     []string{"n"},
			ColumnOrder: "exact",
			RowOrder:    "ignored",
			Duplicates:  "preserved",
		},
	}
}

func TestValidateRequest(t *testing.T) {
	limits := DefaultLimits()
	tests := []struct {
		name  string
		req   schema.GenerationRequest
		field string // "" means accepted
	}{
		{name: "title only", req: schema.GenerationRequest{Title: "Join the tables"}},
		{name: "topics only", req: schema.GenerationRequest{Topics: []string{"joins"}}},
		{name: "neither title nor topics", req: schema.GenerationRequest{}, field: "title"},
		{name: "blank title and no topics", req: schema.GenerationRequest{Title: "  "}, field: "title"},
		{name: "empty topic", req: schema.GenerationRequest{Topics: []string{"joins", " "}}, field: "topics"},
		{name: "unknown difficulty", req: schema.GenerationRequest{Title: "x", Difficulty: schema.Difficulty("expert")}, field: "difficulty"},
		{name: "unknown kind", req: schema.GenerationRequest{Title: "x", Kind: "summary"}, field: "kind"},
		{name: "too many topics", req: schema.GenerationRequest{Topics: filledTopics(maxTopics + 1)}, field: "topics"},
		{name: "oversized title", req: schema.GenerationRequest{Title: strings.Repeat("a", limits.MaxPromptChars+1)}, field: "title"},
		{name: "oversized scenario", req: schema.GenerationRequest{Title: "x", Scenario: strings.Repeat("a", limits.MaxPromptChars+1)}, field: "scenario"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRequest(tc.req, limits)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("validateRequest() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateRequest() = nil, want a rejection naming %q", tc.field)
			}
			if !strings.Contains(err.Detail, tc.field) {
				t.Fatalf("detail %q does not name the field %q", err.Detail, tc.field)
			}
			if err.Code != CodeInvalidRequest {
				t.Fatalf("code = %q, want %q", err.Code, CodeInvalidRequest)
			}
		})
	}
}

func filledTopics(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "joins"
	}
	return out
}

func TestNormalizeRequestClampsAndDefaults(t *testing.T) {
	req := normalizeRequest(schema.GenerationRequest{Title: "x", ExerciseCount: 99}, DefaultLimits())
	if req.ExerciseCount != 40 {
		t.Errorf("exercise_count = %d, want 40", req.ExerciseCount)
	}
	req = normalizeRequest(schema.GenerationRequest{Title: "x", ExerciseCount: -3}, DefaultLimits())
	if req.ExerciseCount != 1 {
		t.Errorf("exercise_count = %d, want 1", req.ExerciseCount)
	}
	req = normalizeRequest(schema.GenerationRequest{Title: "x"}, DefaultLimits())
	if req.Difficulty != schema.Intermediate {
		t.Errorf("difficulty = %q, want %q", req.Difficulty, schema.Intermediate)
	}
}

func TestValidateArtifact(t *testing.T) {
	limits := DefaultLimits()
	tests := []struct {
		name   string
		mutate func(*schema.Artifact)
		field  string // "" means accepted
		limits Limits
	}{
		{name: "baseline is accepted"},
		{name: "empty title", mutate: func(a *schema.Artifact) { a.Title = " " }, field: "title"},
		{name: "empty prompt", mutate: func(a *schema.Artifact) { a.Prompt = "" }, field: "prompt"},
		{name: "unknown difficulty", mutate: func(a *schema.Artifact) { a.Difficulty = schema.Difficulty("expert") }, field: "difficulty"},
		{name: "missing dataset", mutate: func(a *schema.Artifact) { a.Setup.Dataset = "" }, field: "setup.dataset"},
		{name: "schema version zero", mutate: func(a *schema.Artifact) { a.Setup.SchemaVersion = 0 }, field: "setup.schema_version"},
		{name: "seed version zero", mutate: func(a *schema.Artifact) { a.Setup.SeedVersion = 0 }, field: "setup.seed_version"},
		{name: "empty reference", mutate: func(a *schema.Artifact) { a.ReferenceSolution = "" }, field: "reference_solution"},
		{name: "empty cross-check", mutate: func(a *schema.Artifact) { a.CrossCheckSolution = "" }, field: "cross_check_solution"},
		{name: "no objectives", mutate: func(a *schema.Artifact) { a.Objectives = nil }, field: "objectives"},
		{name: "missing hints", mutate: func(a *schema.Artifact) { a.Hints = nil }, field: "hints"},
		{name: "too many hints", mutate: func(a *schema.Artifact) { a.Hints = []string{"1", "2", "3", "4"} }, field: "hints"},
		{name: "unknown dialect", mutate: func(a *schema.Artifact) { a.Dialect = "mysql" }, field: "dialect"},
		{name: "unknown type", mutate: func(a *schema.Artifact) { a.Type = "essay" }, field: "type"},
		{name: "no contract columns", mutate: func(a *schema.Artifact) { a.ResultContract.Columns = nil }, field: "result_contract.columns"},
		{name: "empty contract column", mutate: func(a *schema.Artifact) { a.ResultContract.Columns = []string{"n", " "} }, field: "result_contract.columns"},
		{name: "unknown column_order", mutate: func(a *schema.Artifact) { a.ResultContract.ColumnOrder = "sorted" }, field: "result_contract.column_order"},
		{name: "unknown row_order", mutate: func(a *schema.Artifact) { a.ResultContract.RowOrder = "sorted" }, field: "result_contract.row_order"},
		{name: "unknown duplicates", mutate: func(a *schema.Artifact) { a.ResultContract.Duplicates = "collapsed" }, field: "result_contract.duplicates"},
		{name: "prompt over the byte cap", mutate: func(a *schema.Artifact) { a.Prompt = strings.Repeat("x", limits.MaxOutputBytes+1) }, field: "prompt"},
		{name: "too many setup statements", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = make([]string, limits.MaxSetupStatements+1)
		}, field: "setup.statements"},

		// Reference-solution policy.
		{name: "reference is DDL", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `DROP TABLE "dataset"."orders"` }, field: "reference_solution"},
		{name: "reference is DML", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `INSERT INTO "dataset"."orders" ("id") VALUES (1)` }, field: "reference_solution"},
		{name: "reference is COPY", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `COPY "dataset"."orders" TO STDOUT` }, field: "reference_solution"},
		{name: "reference is CALL", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `CALL do_something()` }, field: "reference_solution"},
		{name: "reference is VACUUM", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `VACUUM "dataset"."orders"` }, field: "reference_solution"},
		{name: "reference is SET", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `SET search_path TO public` }, field: "reference_solution"},
		{name: "reference is SHOW", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `SHOW search_path` }, field: "reference_solution"},
		{name: "reference hides DML in a CTE", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `WITH removed AS (DELETE FROM "dataset"."orders" RETURNING "id") SELECT * FROM removed`
		}, field: "reference_solution"},
		{name: "reference is multi-statement", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `SELECT 1 AS n; SELECT 2 AS n`
		}, field: "reference_solution"},
		{name: "reference probes pg_catalog", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `SELECT relname FROM pg_catalog.pg_class`
		}, field: "reference_solution"},
		{name: "reference probes information_schema", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `SELECT table_name FROM information_schema.tables`
		}, field: "reference_solution"},
		{name: "reference probes a pg_ view", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `SELECT count(*) AS n FROM pg_stat_activity`
		}, field: "reference_solution"},
		{name: "cross-check is not read-only", mutate: func(a *schema.Artifact) {
			a.CrossCheckSolution = `DELETE FROM "dataset"."orders"`
		}, field: "cross_check_solution"},

		// Accepted SQL shapes that must not be false positives.
		{name: "trailing semicolon is one statement", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `SELECT 1 AS n;` }},
		{name: "semicolon inside a literal", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `SELECT ';' AS n` }},
		{name: "quoted keyword as an alias", mutate: func(a *schema.Artifact) { a.ReferenceSolution = `SELECT 1 AS "set"` }},
		{name: "dollar-quoted body with semicolons", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `SELECT $tag$ ; DROP TABLE x ; $tag$ AS n`
		}},
		{name: "WITH statement", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = `WITH one AS (SELECT 1 AS n) SELECT n FROM one`
		}},
		{name: "comment before SELECT", mutate: func(a *schema.Artifact) {
			a.ReferenceSolution = "-- pick the constant\nSELECT 1 AS n"
		}},

		// Setup-plan policy.
		{name: "setup DROP", mutate: func(a *schema.Artifact) { a.Setup.Statements = []string{`DROP TABLE "dataset"."orders"`} }, field: "setup.statements"},
		{name: "setup ALTER", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`ALTER TABLE "dataset"."orders" ADD COLUMN x int`}
		}, field: "setup.statements"},
		{name: "setup GRANT", mutate: func(a *schema.Artifact) { a.Setup.Statements = []string{`GRANT ALL ON "dataset"."orders" TO public`} }, field: "setup.statements"},
		{name: "setup TRUNCATE", mutate: func(a *schema.Artifact) { a.Setup.Statements = []string{`TRUNCATE "dataset"."orders"`} }, field: "setup.statements"},
		{name: "setup multi-statement", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`CREATE TABLE scratch (id int); DROP TABLE scratch`}
		}, field: "setup.statements"},
		{name: "setup reaches the catalog", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`INSERT INTO scratch SELECT relname FROM pg_class`}
		}, field: "setup.statements"},
		{name: "setup CREATE INDEX", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`CREATE INDEX i ON scratch (id)`}
		}, field: "setup.statements"},
		{name: "setup CREATE TABLE accepted", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`CREATE TABLE scratch (id integer)`}
		}},
		{name: "setup TEMP TABLE accepted", mutate: func(a *schema.Artifact) {
			a.Setup.Statements = []string{`CREATE TEMPORARY TABLE scratch (id integer)`, `INSERT INTO scratch ("id") VALUES (1)`, `VALUES (1)`}
		}},
		{name: "setup respects a raised statement cap", limits: Limits{MaxSetupStatements: 20}, mutate: func(a *schema.Artifact) {
			a.Setup.Statements = make([]string, 12)
			for i := range a.Setup.Statements {
				a.Setup.Statements[i] = `VALUES (1)`
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			art := baselineArtifact()
			if tc.mutate != nil {
				tc.mutate(&art)
			}
			l := limits
			if tc.limits.MaxSetupStatements > 0 {
				l = tc.limits.Merge(DefaultLimits())
			}
			err := validateArtifact(art, l)
			if tc.field == "" {
				if err != nil {
					t.Fatalf("validateArtifact() = %v (%s), want nil", err, err.Detail)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateArtifact() = nil, want a rejection naming %q", tc.field)
			}
			if !strings.Contains(err.Detail, tc.field) {
				t.Fatalf("detail %q does not name the field %q", err.Detail, tc.field)
			}
			if err.Code != CodeInvalidArtifact {
				t.Fatalf("code = %q, want %q", err.Code, CodeInvalidArtifact)
			}
		})
	}
}

func TestTokenizeSQLKeepsLiteralsOpaque(t *testing.T) {
	tokens := tokenizeSQL(`SELECT 'a;b' AS s, "we;ird" AS q, $x$ ; $x$ AS d -- ; ignored`)
	if segs := splitStatements(tokens); len(segs) != 1 {
		t.Fatalf("segments = %d, want 1: a semicolon inside a literal or comment is not a statement boundary", len(segs))
	}
	tokens = tokenizeSQL(`SELECT 1; SELECT 2`)
	if segs := splitStatements(tokens); len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
}
