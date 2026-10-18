package perf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// The plan seam: one EXPLAIN, captured under the same deadline, semaphore and
// row/byte caps as POST /query, reduced to the deterministic facts an exercise
// can grade and the raw tree a learner can look at.
//
// Timing honesty is structural here: actual_* are reported when the statement
// ran, but no graded assertion reads them (Grade reads only the summary),
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
	Limits      engine.Limits
	Dataset     string
	ScaleFactor int
}

// Environment SQL. Every value is read over the learner's own connection, in
// the same transaction settings the plan ran under (sessionSettings applies
// work_mem and search_path to each statement), so the reported settings are the
// ones that produced the plan. current_setting reflects the transaction value,
// while pg_settings reports the session default.
const (
	sqlPlanRowCounts = `SELECT c.relname, c.reltuples::float8
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = current_schema() AND c.relkind IN ('r', 'p', 'm') AND c.relname <> 'dataset_meta'
ORDER BY c.relname`

	sqlPlanIndexes = `SELECT tablename, indexname
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
// It uses the engine.Executor seam, not the concrete pgx pool, so the same code
// runs against the real engine and against a test fake.
//
// An engine failure is reported inside the bundle's error envelope, exactly as
// engine.Execute reports it inside a QueryResult, so one taxonomy serves both
// surfaces. There is no error return: a plan capture always produces a bundle,
// and a refusal is a failed bundle rather than a Go error.
func Explain(ctx context.Context, ex engine.Executor, statement string, opts ExplainOptions) *PlanBundle {
	/* Fill, not Merge: the HTTP handler already resolved request-versus-server
	   with Merge, where the server's configured caps are the ceiling. Merging
	   the compiled DefaultLimits() again here would clamp down, so a gateway
	   configured above the default (QC_MAX_ROWS=2000) would silently plan at
	   the default 500. */
	limits := opts.Limits.Fill(engine.DefaultLimits())
	start := time.Now()
	requestID := engine.RequestIDFromContext(ctx)

	precision := TimingEstimate
	if opts.Analyze {
		precision = TimingAnalyze
	}

	// failed builds an honest refusal that still carries correlation ids: the
	// numbers are absent, but the request is not anonymous in the logs.
	failed := func(code, message, detail string) *PlanBundle {
		bundle := FailedBundle(EnginePostgres, code, message, detail)
		bundle.DurationMS = engine.RoundMS(time.Since(start))
		bundle.Statement = planStringPtr(statement)
		bundle.RequestID = requestID
		return bundle
	}

	if engine.IsBlankSQL(statement) {
		return failed(engine.CodeEmptyQuery, "Blank SQL. Write a statement, then Explain.", "")
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
		/* ClassifyFor, not classify: a dialect with its own error vocabulary
		   (the ErrorClassifier seam) must be asked first, exactly as Execute
		   does. classify reads only the shared table and would misname a
		   SQLite error the day the plan lab serves a second dialect. */
		qerr := engine.ClassifyFor(qctx, ex, err)
		return failed(qerr.Code, qerr.Message, planDetail(qerr))
	}
	// The plan is one row of untrusted engine text. A plan larger than the
	// byte cap is a hard failure, the same way Execute treats a result larger
	// than the cap: the whole document cannot be represented, so it is not
	// half-reported.
	if len(raw) > limits.MaxBytes {
		return failed(engine.CodeResultLimit,
			fmt.Sprintf("plan exceeded the %d-byte cap", limits.MaxBytes), "")
	}

	root, perr := parsePlanRoot(raw)
	if perr != nil {
		return failed(engine.CodeRuntime, "The engine's plan could not be read.", perr.Error())
	}

	summary, capped := summarizePlan(root, DefaultPlanNodeCap)
	warnings := deriveWarnings(summary, precision)
	if capped {
		warnings = append(warnings, PlanWarning{
			Code:    WarnNodeCapReached,
			Message: fmt.Sprintf("The plan has more than %d nodes; the flat walk is reported up to the cap.", DefaultPlanNodeCap),
		})
	}

	bundle := &PlanBundle{
		Status:          StatusSucceeded,
		Analyze:         opts.Analyze,
		TimingPrecision: precision,
		Engine:          EnginePostgres,
		Plan:            root,
		Summary:         summary,
		Environment:     readEnvironment(qctx, ex, opts),
		Warnings:        warnings,
		DurationMS:      engine.RoundMS(time.Since(start)),
		Statement:       planStringPtr(statement),
		RequestID:       requestID,
		Error:           nil,
	}
	return bundle
}

// explainRow runs the EXPLAIN and returns the first row's single column as the
// raw JSON text. A read-only transaction is enough: a plain EXPLAIN only plans,
// and an EXPLAIN ANALYZE of a write fails rather than mutating anything on the
// shared dataset.
func explainRow(ctx context.Context, ex engine.Executor, sql string) ([]byte, error) {
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
func parsePlanRoot(raw []byte) (PlanRoot, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return planRootOf(doc), nil
}

// planRootOf unwraps the root node from whatever the driver decoded. It
// accepts the standard `[{"Plan": {...}}]` document, a bare `{"Plan": {...}}`,
// and a node map that already carries "Node Type". Anything else is nil.
func planRootOf(doc any) PlanRoot {
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
func summarizePlan(root PlanRoot, nodeCap int) (PlanSummary, bool) {
	summary := EmptySummary()
	if root == nil {
		return summary, false
	}
	capped := false

	var walk func(node PlanRoot, depth int, parentRelation *string)
	walk = func(node PlanRoot, depth int, parentRelation *string) {
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
		n := PlanNode{
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
				n.Warnings = append(n.Warnings, WarnEstimateOff)
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
			summary.SeqScans = append(summary.SeqScans, PlanSeqScan{Relation: *n.Relation, PlanRows: rows})
		}
		if n.Index != nil && n.Relation != nil && strings.Contains(strings.ToLower(n.NodeType), "index") {
			summary.IndexScans = append(summary.IndexScans, PlanIndexScan{
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
func deriveWarnings(summary PlanSummary, precision string) []PlanWarning {
	warnings := []PlanWarning{}
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
		warnings = append(warnings, PlanWarning{
			Code:    WarnSeqScanLargeTable,
			Message: fmt.Sprintf("%s %s read by a sequential scan over %d+ planned rows.", strings.Join(large, ", "), verb, SeqScanLargeRows),
		})
	}
	if precision == TimingAnalyze {
		for i := range summary.Nodes {
			if planHasWarning(summary.Nodes[i].Warnings, WarnEstimateOff) {
				warnings = append(warnings, PlanWarning{
					Code:    WarnEstimateOff,
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
func readEnvironment(ctx context.Context, ex engine.Executor, opts ExplainOptions) PlanEnv {
	env := EmptyEnvironment(EnginePostgres)
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
			env.Indexes = append(env.Indexes, PlanIndex{
				Relation: planValueString(row, 0),
				Name:     planValueString(row, 1),
			})
		}
	}
	return env
}
