package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/eval"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* ---------- request rendering ---------- */

// requestPrompt renders the validated request for a provider. It is plain text
// on purpose: the provider interface has no structured request, and a
// human-readable brief is the same thing a person would be handed.
func requestPrompt(req schema.GenerationRequest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Kind: %s\n", kindOrChallenge(req.Kind))
	if req.Title != "" {
		fmt.Fprintf(&b, "Title: %s\n", req.Title)
	}
	if len(req.Topics) > 0 {
		fmt.Fprintf(&b, "Topics: %s\n", strings.Join(nonEmpty(req.Topics), ", "))
	}
	fmt.Fprintf(&b, "Difficulty: %s\n", req.Difficulty)
	if req.Scenario != "" {
		fmt.Fprintf(&b, "Scenario: %s\n", req.Scenario)
	}
	for _, c := range req.Constraints {
		fmt.Fprintf(&b, "Constraint: %s\n", c)
	}
	fmt.Fprintf(&b, "Exercises: %d\n", req.ExerciseCount)
	b.WriteString("Design one exercise with a reference solution, an independent cross-check solution, and a result contract.")
	/* What the exercise will actually run against. The environment loads the
	   dataset before any setup statement is applied, so a model that is not
	   told this writes `CREATE TABLE customers` and the run fails with
	   "relation already exists". The allowed shapes are exactly what
	   validateSetupStatement accepts, so the model is not asked to guess at a
	   rule the gate will then apply. */
	b.WriteString(datasetBrief())
	b.WriteString("Do not create, alter or drop these tables.")
	b.WriteString("\nsetup.statements may only contain single CREATE TABLE or INSERT INTO statements that add extra scratch objects your exercise needs. Use an empty list when the dataset alone is enough. Do not use DROP, ALTER, UPDATE, DELETE or SET anywhere in them.")
	return b.String()
}

// datasetBrief names the tables and columns an exercise will run against,
// rendered from the shipped dataset rather than from a hand-written list.
//
// A model that writes SQL against a schema has to be handed that schema.
// dataset.Tables is the source the evaluator runs against, so rendering the
// brief from it also keeps the two from drifting. A model left to guess wrote
// `o.order_id` for a column named `o.id`: the bundle passed validate_artifact
// and the scratch environment, then failed at reference_contract.
func datasetBrief() string {
	tables, ok := dataset.Tables("commerce")
	if !ok || len(tables.Tables) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nThe exercise runs against the commerce dataset, already loaded. Write the reference and cross-check solutions with exactly these tables and column names:\n")
	for _, t := range tables.Tables {
		fmt.Fprintf(&b, "  %s(", t.Name)
		for i, c := range t.Cols {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(strings.Join(nonEmpty(c), " "))
		}
		fmt.Fprintf(&b, ") — %d rows\n", t.Rows)
	}
	return b.String()
}

func requestSummary(req schema.GenerationRequest) string {
	if req.Title != "" {
		return fmt.Sprintf("%s (%s, %d exercises)", req.Title, req.Difficulty, req.ExerciseCount)
	}
	return fmt.Sprintf("%s (%s, %d exercises)", strings.Join(nonEmpty(req.Topics), ", "), req.Difficulty, req.ExerciseCount)
}

func kindOrChallenge(kind string) string {
	if kind == "" {
		return "challenge"
	}
	return kind
}

func systemPrompt() string {
	return "You design SQL exercises over a fixed PostgreSQL commerce dataset. Reply with one JSON object and no prose. Solutions must be single read-only SELECT or WITH statements."
}

func scratchName(art schema.Artifact) string {
	return fmt.Sprintf("ai_%016x", hashPrompt(art.ReferenceSolution))
}

func evalContract(rc schema.ResultContract) eval.Contract {
	return eval.Contract{
		Columns:          rc.Columns,
		ColumnOrder:      rc.ColumnOrder,
		RowOrder:         rc.RowOrder,
		Duplicates:       rc.Duplicates,
		NumericTolerance: rc.NumericTolerance,
	}
}

// queryErrorText flattens an engine error into the one line an envelope Detail
// carries.
func queryErrorText(res engine.QueryResult) string {
	if res.Error == nil {
		return res.Status
	}
	text := res.Error.Message
	if res.Error.Detail != nil && *res.Error.Detail != "" {
		text += " (" + *res.Error.Detail + ")"
	}
	return text
}

func trimTrailingSemicolon(sql string) string {
	return strings.TrimRight(strings.TrimSpace(sql), ";")
}

// quoteIdent quotes a PostgreSQL identifier. It is used only for identifiers
// that came from the server's own catalog or from this package's contract,
// never for a value; values are always passed as bind parameters.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// probeStrings runs one parameterised read-only probe and returns the first
// column's values as strings. engine.Execute has no argument support, and
// interpolating a provider-supplied dataset name into SQL would be an
// injection, so this is a small dedicated helper. Values are formatted with
// fmt rather than a type switch because the only probes here return integers
// and text.
func probeStrings(ctx context.Context, ex engine.Executor, sql string, args ...any) ([]string, error) {
	rows, err := ex.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		if len(out) >= 1024 {
			break
		}
		vals, verr := rows.Values()
		if verr != nil {
			return nil, verr
		}
		if len(vals) == 0 {
			continue
		}
		out = append(out, valueString(vals[0]))
	}
	if rerr := rows.Err(); rerr != nil {
		return nil, rerr
	}
	return out, nil
}

func valueString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", t)
	}
}
