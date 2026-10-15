package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/perf"
)

// The plan seam: one EXPLAIN, captured under the same deadline, semaphore and
// row/byte caps as POST /query, reduced to the deterministic facts an exercise
// can grade and the raw tree a learner can look at.
//
// Timing honesty is structural here: actual_* are reported when the statement
// ran, but no graded assertion reads them (perf.Grade reads only the summary),
// and a bundle always names what its numbers are worth in timing_precision.

const (
	// DefaultPlanNodeCap bounds the flat depth-first walk. A runaway planner
	// output (deeply nested join trees) is reported up to the cap and flagged,
	// rather than turning one request into an unbounded amount of work.
	DefaultPlanNodeCap = 200

	// SeqScanLargeRows and EstimateOffRatio mirror web/src/api/planContract.ts
	// (SEQ_SCAN_LARGE_ROWS, ESTIMATE_OFF_RATIO). They are warnings about the
	// plan, not verdicts: a Seq Scan over a twelve-row dimension table is
	// correct and stays quiet.
	SeqScanLargeRows = 1000
	EstimateOffRatio = 100
)

// ExplainOptions tunes one plan capture. Limits and the request deadline come
// from the same source POST /query uses, so a runaway EXPLAIN ANALYZE is
// stopped by the host exactly like a runaway query (QUERY_TIMEOUT).
type ExplainOptions struct {
	Analyze     bool
	Limits      Limits
	Dataset     string
	ScaleFactor int
	NodeCap     int
}

// Environment SQL. Every value is read over the learner's own connection, in
// the same transaction settings the plan ran under (sessionSettings applies
// work_mem and search_path to each statement), so the reported settings are the
// ones that produced the plan — current_setting reflects the transaction value,
// while pg_settings would report the session default.
const (
	sqlPlanRowCounts = `SELECT c.relname, c.reltuples::float8
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'm') AND c.relname <> 'dataset_meta'
ORDER BY c.relname`

	sqlPlanIndexes = `SELECT tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname = current_schema()
ORDER BY tablename, indexname`

	sqlPlanSettings = `SELECT
  current_setting('server_version')       AS server_version,
  current_setting('random_page_cost')     AS random_page_cost,
  current_setting('seq_page_cost')        AS seq_page_cost,
  current_setting('effective_cache_size') AS effective_cache_size,
  current_setting('work_mem')             AS work_mem,
  current_setting('enable_seqscan')       AS enable_seqscan,
  current_setting('enable_hashjoin')      AS enable_hashjoin,
  current_setting('enable_nestloop')      AS enable_nestloop,
  current_setting('enable_mergejoin')     AS enable_mergejoin,
  current_setting('enable_indexscan')     AS enable_indexscan,
  current_setting('enable_bitmapscan')    AS enable_bitmapscan`
)

// planSettings is the column order of sqlPlanSettings. The reported set is the
// planner inputs that change a chosen plan, read-only: M5 never rewrites them to
// make an exercise pass.
var planSettings = []string{
	"random_page_cost", "seq_page_cost", "effective_cache_size", "work_mem",
	"enable_seqscan", "enable_hashjoin", "enable_nestloop", "enable_mergejoin",
	"enable_indexscan", "enable_bitmapscan",
}

// Explain runs one statement through ex as an EXPLAIN and returns a plan bundle.
//
// It uses the Executor seam, not the concrete pgx pool, so the same code runs
// against the real engine and against a test fake; PgxExecutor also carries an
// Explain method for callers that hold the concrete type.
//
// The returned error is always nil today: an engine failure is reported inside
// the bundle's error envelope, exactly as Execute reports it inside a
// QueryResult, so one taxonomy serves both surfaces. It exists because a future
// planner-only engine may fail before a bundle can be built at all.
func Explain(ctx context.Context, ex Executor, statement string, opts ExplainOptions) (*perf.PlanBundle, error) {
	limits := opts.Limits.Merge(DefaultLimits())
	start := time.Now()
	requestID := RequestIDFromContext(ctx)

	precision := perf.TimingEstimate
	if opts.Analyze {
		precision = perf.TimingAnalyze
	}

	// failed builds an honest refusal that still carries correlation ids: the
	// numbers are absent, but the request is not anonymous in the logs.
	failed := func(code, message, detail string) (*perf.PlanBundle, error) {
		bundle := perf.FailedBundle(perf.EnginePostgres, code, message, detail)
		bundle.DurationMS = RoundMS(time.Since(start))
		bundle.Statement = planStringPtr(statement)
		bundle.RequestID = requestID
		return bundle, nil
	}

	if isBlankSQL(statement) {
		return failed(CodeEmptyQuery, "Blank SQL. Write a statement, then Explain.", "")
	}

	// The deadline is shared with the EXPLAIN and with every environment read;
	// the executor turns it into a server-side statement_timeout, so a runaway
	// ANALYZE is stopped by PostgreSQL as well as by the client.
	qctx, cancel := context.WithTimeout(ctx, limits.Timeout())
	defer cancel()

	explainSQL := "EXPLAIN (FORMAT JSON) " + statement
	if opts.Analyze {
		explainSQL = "EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) " + statement
	}

	raw, err := explainRow(qctx, ex, explainSQL)
	if err != nil {
		qerr := classify(qctx, err)
		return failed(qerr.Code, qerr.Message, planDetail(qerr))
	}
	// The plan is one row of untrusted engine text. A plan larger than the
	// byte cap is a hard failure, the same way Execute treats a result larger
	// than the cap: the whole document cannot be represented, so it is not
	// half-reported.
	if len(raw) > limits.MaxBytes {
		return failed(CodeResultLimit,
			fmt.Sprintf("plan exceeded the %d-byte cap", limits.MaxBytes), "")
	}

	root, perr := parsePlanRoot(raw)
	if perr != nil {
		return failed(CodeRuntime, "The engine's plan could not be read.", perr.Error())
	}

	nodeCap := opts.NodeCap
	if nodeCap <= 0 {
		nodeCap = DefaultPlanNodeCap
	}
	summary, capped := summarizePlan(root, nodeCap)
	warnings := deriveWarnings(summary, precision)
	if capped {
		warnings = append(warnings, perf.PlanWarning{
			Code:    perf.WarnNodeCapReached,
			Message: fmt.Sprintf("The plan has more than %d nodes; the flat walk is reported up to the cap.", nodeCap),
		})
	}

	bundle := &perf.PlanBundle{
		Status:          perf.StatusSucceeded,
		Analyze:         opts.Analyze,
		TimingPrecision: precision,
		Engine:          perf.EnginePostgres,
		Plan:            root,
		Summary:         summary,
		Environment:     readEnvironment(qctx, ex, opts),
		Warnings:        warnings,
		DurationMS:      RoundMS(time.Since(start)),
		Statement:       planStringPtr(statement),
		RequestID:       requestID,
		Error:           nil,
	}
	return bundle, nil
}

// Explain implements the plan seam on the real executor.
func (e *PgxExecutor) Explain(ctx context.Context, statement string, opts ExplainOptions) (*perf.PlanBundle, error) {
	return Explain(ctx, e, statement, opts)
}

// explainRow runs the EXPLAIN and returns the first row's single column as the
// raw JSON text. A read-only transaction is enough: a plain EXPLAIN only plans,
// and an EXPLAIN ANALYZE of a write fails rather than mutating anything on the
// shared dataset.
func explainRow(ctx context.Context, ex Executor, sql string) ([]byte, error) {
	rows, err := ex.Query(ctx, sql)
	if err != nil {
		return nil, err
	}
	if rows == nil {
		return nil, errors.New("EXPLAIN returned no result set")
	}
	defer rows.Close()
	if !rows.Next() {
		if rerr := rows.Err(); rerr != nil {
			return nil, rerr
		}
		return nil, errors.New("EXPLAIN returned no rows")
	}
	vals, verr := rows.Values()
	if verr != nil {
		return nil, verr
	}
	if len(vals) == 0 {
		return nil, errors.New("EXPLAIN returned no columns")
	}
	return planText(vals[0])
}

// planText normalizes the EXPLAIN value to JSON text. Depending on the driver,
// a `json` column arrives as text, as bytes, or already decoded.
func planText(v any) ([]byte, error) {
	switch x := v.(type) {
	case nil:
		return nil, errors.New("EXPLAIN returned NULL")
	case string:
		return []byte(x), nil
	case []byte:
		return x, nil
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return nil, err
		}
		return b, nil
	}
}

// parsePlanRoot unwraps PostgreSQL's `[{"Plan": {...}}]` document to its root
// node. Anything that is already a node (`{"Node Type": ...}`) is taken as-is.
func parsePlanRoot(raw []byte) (perf.PlanRoot, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return planRootOf(doc), nil
}

func planRootOf(doc any) perf.PlanRoot {
	if arr, ok := doc.([]any); ok {
		if len(arr) == 0 {
			return nil
		}
		doc = arr[0]
	}
	m, ok := doc.(map[string]any)
	if !ok {
		return nil
	}
	if plan, ok := m["Plan"].(map[string]any); ok {
		return plan
	}
	if _, ok := m["Node Type"].(string); ok {
		return m
	}
	return nil
}

// summarizePlan is the identical depth-first walk web/src/api/planContract.ts
// performs: given the raw plan tree it returns the deterministic facts Grade
// reads. It reports whether the node cap cut the walk short.
func summarizePlan(root perf.PlanRoot, nodeCap int) (perf.PlanSummary, bool) {
	summary := perf.EmptySummary()
	if root == nil {
		return summary, false
	}
	capped := false

	var walk func(node perf.PlanRoot, depth int, parentRelation *string)
	walk = func(node perf.PlanRoot, depth int, parentRelation *string) {
		nodeType := planString(node, "Node Type")
		if nodeType == "" {
			return
		}
		if len(summary.Nodes) >= nodeCap {
			capped = true
			return
		}
		// A Bitmap Index Scan names the index but not the relation: PostgreSQL
		// prints the relation on the Bitmap Heap Scan above it and leaves it off
		// the scan underneath, so a child with no relation of its own is read
		// back against its parent's. Without this, IndexScans and Relations
		// silently drop every bitmap access -- the common shape for a selective
		// predicate -- and a contract that states "two index scans" is graded
		// against a summary that counts one. Kept identical to the browser
		// adapter (web/src/api/planContract.ts), which walks the same tree.
		relation := planStringRef(node, "Relation Name")
		if relation == nil {
			relation = parentRelation
		}
		n := perf.PlanNode{
			Depth:              depth,
			NodeType:           nodeType,
			Relation:           relation,
			Alias:              planStringRef(node, "Alias"),
			Index:              planStringRef(node, "Index Name"),
			JoinType:           planStringRef(node, "Join Type"),
			ParentRelationship: planStringRef(node, "Parent Relationship"),
			EstimatedRows:      planNumberRef(node, "Plan Rows"),
			EstimatedCost:      planNumberRef(node, "Total Cost"),
			ActualRows:         planNumberRef(node, "Actual Rows"),
			ActualTimeMS:       planNumberRef(node, "Actual Total Time"),
			Loops:              planNumberRef(node, "Actual Loops"),
			BuffersRead:        planBlockReads(node),
			Filter:             planStringRef(node, "Filter"),
			IndexCondition:     planStringRef(node, "Index Cond"),
			SortKey:            planStringSlice(node, "Sort Key"),
			Warnings:           []string{},
		}
		// The same per-node estimate flag the browser adapter sets, so an
		// exported bundle carries the same note whichever engine captured it.
		if n.EstimatedRows != nil && n.ActualRows != nil {
			est, act := *n.EstimatedRows, *n.ActualRows
			if (est > 0 && act/est >= EstimateOffRatio) || (act > 0 && est/act >= EstimateOffRatio) {
				n.Warnings = append(n.Warnings, perf.WarnEstimateOff)
			}
		}
		summary.Nodes = append(summary.Nodes, n)

		if children, ok := node["Plans"].([]any); ok {
			for _, child := range children {
				if m, ok := child.(map[string]any); ok {
					walk(m, depth+1, relation)
				}
			}
		}
	}
	walk(root, 0, nil)

	for i := range summary.Nodes {
		n := &summary.Nodes[i]
		summary.NodeTypes = appendUnique(summary.NodeTypes, n.NodeType)
		if n.Relation != nil {
			summary.Relations = appendUnique(summary.Relations, *n.Relation)
		}
		if n.Index != nil {
			summary.IndexesUsed = appendUnique(summary.IndexesUsed, *n.Index)
		}
		if n.NodeType == "Seq Scan" && n.Relation != nil {
			rows := 0.0
			if n.EstimatedRows != nil {
				rows = *n.EstimatedRows
			}
			summary.SeqScans = append(summary.SeqScans, perf.PlanSeqScan{Relation: *n.Relation, PlanRows: rows})
		}
		if n.Index != nil && n.Relation != nil && strings.Contains(strings.ToLower(n.NodeType), "index") {
			summary.IndexScans = append(summary.IndexScans, perf.PlanIndexScan{
				Relation: *n.Relation, Index: *n.Index, NodeType: n.NodeType,
			})
		}
		if n.EstimatedRows != nil && *n.EstimatedRows > summary.MaxEstimatedRows {
			summary.MaxEstimatedRows = *n.EstimatedRows
		}
	}
	summary.NodeCount = len(summary.Nodes)
	summary.PlanRows = planNumber(root, "Plan Rows")
	summary.TotalCost = planNumber(root, "Total Cost")
	return summary, capped
}

// deriveWarnings is deriveWarnings() from planContract.ts: the warnings the
// summary itself justifies, computed the same way on both engines.
func deriveWarnings(summary perf.PlanSummary, precision string) []perf.PlanWarning {
	warnings := []perf.PlanWarning{}
	var large []string
	for _, s := range summary.SeqScans {
		if s.PlanRows >= SeqScanLargeRows {
			large = append(large, s.Relation)
		}
	}
	if len(large) > 0 {
		verb := "are"
		if len(large) == 1 {
			verb = "is"
		}
		warnings = append(warnings, perf.PlanWarning{
			Code:    perf.WarnSeqScanLargeTable,
			Message: fmt.Sprintf("%s %s read by a sequential scan over %d+ planned rows.", strings.Join(large, ", "), verb, SeqScanLargeRows),
		})
	}
	if precision == perf.TimingAnalyze {
		for i := range summary.Nodes {
			if planHasWarning(summary.Nodes[i].Warnings, perf.WarnEstimateOff) {
				warnings = append(warnings, perf.PlanWarning{
					Code:    perf.WarnEstimateOff,
					Message: fmt.Sprintf("At least one node's actual rows differ from the planner's estimate by %dx or more.", EstimateOffRatio),
				})
				break
			}
		}
	}
	return warnings
}

// readEnvironment reports what the plan was produced against. Every read is
// best-effort: a bundle with an empty environment is less useful but still
// honest, while a plan with no environment at all would violate the contract.
func readEnvironment(ctx context.Context, ex Executor, opts ExplainOptions) perf.PlanEnv {
	env := perf.EmptyEnvironment(perf.EnginePostgres)
	env.ScaleFactor = opts.ScaleFactor
	if env.ScaleFactor <= 0 {
		env.ScaleFactor = 1
	}
	if opts.Dataset != "" {
		dataset := opts.Dataset
		env.Dataset = &dataset
	}

	if vals, err := planFirstRow(ctx, ex, sqlPlanSettings); err == nil {
		env.EngineVersion = planValueString(vals, 0)
		for i, name := range planSettings {
			if v := planValueString(vals, i+1); v != "" {
				env.Settings[name] = v
			}
		}
	}

	if rows, err := planRows(ctx, ex, sqlPlanRowCounts); err == nil {
		for _, row := range rows {
			name := planValueString(row, 0)
			if name == "" {
				continue
			}
			// reltuples is -1 for a relation that was never analyzed; reporting
			// a negative row count would be worse than reporting zero.
			count := planValueFloat(row, 1)
			if count < 0 {
				count = 0
			}
			env.RowCounts[name] = count
		}
	}

	if rows, err := planRows(ctx, ex, sqlPlanIndexes); err == nil {
		for _, row := range rows {
			idx := perf.PlanIndex{
				Relation: planValueString(row, 0),
				Name:     planValueString(row, 1),
			}
			if def := planValueString(row, 2); def != "" {
				idx.Definition = &def
			}
			env.Indexes = append(env.Indexes, idx)
		}
	}
	return env
}

/* ---------- JSON plan helpers ---------- */

func planString(node map[string]any, key string) string {
	if s, ok := node[key].(string); ok {
		return s
	}
	return ""
}

func planStringRef(node map[string]any, key string) *string {
	if s, ok := node[key].(string); ok {
		return &s
	}
	return nil
}

func planNumber(node map[string]any, key string) float64 {
	if f, ok := node[key].(float64); ok {
		return f
	}
	return 0
}

func planNumberRef(node map[string]any, key string) *float64 {
	if f, ok := node[key].(float64); ok {
		return &f
	}
	return nil
}

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

func planBlockRead(node map[string]any, keys ...string) (float64, bool) {
	for _, key := range keys {
		if f, ok := node[key].(float64); ok {
			return f, true
		}
	}
	return 0, false
}

func planHasWarning(warnings []string, code string) bool {
	for _, w := range warnings {
		if w == code {
			return true
		}
	}
	return false
}

func appendUnique(list []string, value string) []string {
	for _, s := range list {
		if s == value {
			return list
		}
	}
	return append(list, value)
}

/* ---------- environment row helpers ---------- */

func planFirstRow(ctx context.Context, ex Executor, sql string, args ...any) ([]any, error) {
	rows, err := planRows(ctx, ex, sql, args...)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("no rows")
	}
	return rows[0], nil
}

func planRows(ctx context.Context, ex Executor, sql string, args ...any) ([][]any, error) {
	rows, err := ex.Query(ctx, sql, args...)
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

func planDetail(e *QueryError) string {
	if e == nil || e.Detail == nil {
		return ""
	}
	return *e.Detail
}
