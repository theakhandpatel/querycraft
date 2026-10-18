// Plan JSON helpers. A captured plan arrives as an untrusted map[string]any,
// and PostgreSQL omits a field rather than sending a zero. These readers keep
// that distinction: the scalar getters return a value plus a zero, while the
// *Ref getters return nil for an absent field. The environment helpers read
// the planner's own tables over the learner's connection.
package perf

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* ---------- JSON plan helpers ---------- */

// planString reads a string field, or "" when the field is absent.
func planString(node map[string]any, key string) string {
	if s, ok := node[key].(string); ok {
		return s
	}
	return ""
}

// planStringRef returns a pointer to a string field, or nil when the field is
// absent, so "not reported" stays distinct from an empty string.
func planStringRef(node map[string]any, key string) *string {
	if s, ok := node[key].(string); ok {
		return &s
	}
	return nil
}

// planNumber reads a numeric field, or 0 when the field is absent.
func planNumber(node map[string]any, key string) float64 {
	if f, ok := node[key].(float64); ok {
		return f
	}
	return 0
}

// planNumberRef returns a pointer to a numeric field, or nil when the field is
// absent.
func planNumberRef(node map[string]any, key string) *float64 {
	if f, ok := node[key].(float64); ok {
		return &f
	}
	return nil
}

// planStringSlice reads an array field as strings and formats each element.
// An absent field returns nil, not an empty slice.
func planStringSlice(node map[string]any, key string) []string {
	list, ok := node[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(list))
	for _, v := range list {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// planBlockReads sums the blocks the node read from disk (as opposed to those
// it found in shared buffers), which is the reproducible half of the I/O a plan
// costs.
func planBlockReads(node map[string]any) *float64 {
	read, ok := planBlockRead(node, "Shared Read Blocks", "Read Blocks", "Local Read Blocks", "Temp Read Blocks")
	if !ok {
		return nil
	}
	shared, _ := planBlockRead(node, "Shared Read Blocks", "Read Blocks")
	local, _ := planBlockRead(node, "Local Read Blocks")
	temp, _ := planBlockRead(node, "Temp Read Blocks")
	total := shared + local + temp
	if total <= 0 {
		return &read
	}
	return &total
}

// planBlockRead returns the value of the first key present, and whether one was.
func planBlockRead(node map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if f, ok := node[key].(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func planHasWarning(warnings []string, code string) bool {
	return slices.Contains(warnings, code)
}

func appendUnique(list []string, value string) []string {
	if slices.Contains(list, value) {
		return list
	}
	return append(list, value)
}

/* ---------- environment row helpers ---------- */

// planFirstRow runs one environment query and returns its first row. It is the
// one-row path, used for the settings read.
func planFirstRow(ctx context.Context, ex engine.Executor, sql string) ([]any, error) {
	rows, err := planRows(ctx, ex, sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no rows")
	}
	return rows[0], nil
}

// planRows runs one environment query and collects every row. It returns nil
// rows as nil, which the callers treat as an empty result.
func planRows(ctx context.Context, ex engine.Executor, sql string) ([][]any, error) {
	rows, err := ex.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, nil
	}
	defer rows.Close()
	var out [][]any
	for rows.Next() {
		vals, verr := rows.Values()
		if verr != nil {
			return out, verr
		}
		out = append(out, vals)
	}
	return out, rows.Err()
}

// planValueString reads one value as a string. A NULL or an out-of-range index
// reads as "".
func planValueString(vals []any, i int) string {
	if i >= len(vals) || vals[i] == nil {
		return ""
	}
	switch v := vals[i].(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprint(v)
	}
}

// planValueFloat reads one value as a float, accepting the numeric types a
// driver may return for a float column. Anything else reads as 0.
func planValueFloat(vals []any, i int) float64 {
	if i >= len(vals) {
		return 0
	}
	switch v := vals[i].(type) {
	case float64:
		return v
	case float32:
		return float64(v)
	case int64:
		return float64(v)
	case int:
		return float64(v)
	case string:
		var f float64
		if _, err := fmt.Sscanf(v, "%g", &f); err == nil {
			return f
		}
	}
	return 0
}

func planStringPtr(s string) *string { return &s }

// planDetail returns the engine's detail text, or "" when there is none.
func planDetail(e *engine.QueryError) string {
	if e == nil || e.Detail == nil {
		return ""
	}
	return *e.Detail
}
