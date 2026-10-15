package perf

import "fmt"

// Grade codes. They are exactly the codes contracts/perf-contract.schema.json
// implies and web/src/api/planContract.ts emits: one vocabulary for both
// languages, like the result-contract codes. A new code is a schema change.
const (
	CodeNodeMissing      = "PERF_NODE_MISSING"
	CodeNodeForbidden    = "PERF_NODE_FORBIDDEN"
	CodeIndexNotUsed     = "PERF_INDEX_NOT_USED"
	CodePlanRowsExceeded = "PERF_PLAN_ROWS_EXCEEDED"
	CodeDialectMismatch  = "PERF_DIALECT_MISMATCH"
)

// PerfRequireNode is one "this plan must contain" assertion. relation and index
// narrow the match; a nil field means "any".
type PerfRequireNode struct {
	NodeType string  `json:"node_type"`
	Relation *string `json:"relation"`
	Index    *string `json:"index"`
	Label    *string `json:"label"`
}

// PerfForbidNode is one "this plan must not contain" assertion. AbovePlanRows is
// what keeps a twelve-row dimension table legal: a sequential scan that the
// planner expects to read at most that many rows is a correct plan.
type PerfForbidNode struct {
	NodeType      string   `json:"node_type"`
	Relation      string   `json:"relation"`
	AbovePlanRows *float64 `json:"above_plan_rows"`
	Label         *string  `json:"label"`
}

// PerfContract is the structural expectation an exercise attaches to its plan.
// There is deliberately no timing field: a field that exists is a field someone
// will grade, and a timing is not reproducible on a shared machine.
type PerfContract struct {
	Engine         string            `json:"engine"`
	RequireNodes   []PerfRequireNode `json:"require_nodes"`
	ForbidNodes    []PerfForbidNode  `json:"forbid_nodes"`
	RequireIndexes []string          `json:"require_indexes"`
	MaxPlanRows    *float64          `json:"max_plan_rows"`
	MinIndexScans  *int              `json:"min_index_scans"`
}

// Failure is one assertion that did not hold. Label is the human sentence the
// lesson shows; Detail is the measured fact behind it.
type Failure struct {
	Code   string
	Label  string
	Detail string
}

// Verdict is the grader's answer. Passed is exactly "no failures", so a caller
// can render either without recomputing anything.
type Verdict struct {
	Passed   bool
	Failures []Failure
}

// Grade decides whether a captured plan does the work the contract asked for.
//
// Every assertion below reads Summary — node types, relations, index names and
// the planner's own row estimate. Nothing here reads actual_time_ms,
// duration_ms or any other timing, because M5 reports timings honestly and
// ungraded (docs/m5-implementation-plan.md, "Why evaluation comes before
// timing"). This is the Go half of gradePlan() in
// web/src/api/planContract.ts and the two must agree case for case; the one
// deliberate difference is the forbid_nodes boundary, where the schema's word
// is "above" and a node at exactly the threshold therefore stays legal.
func Grade(bundle *PlanBundle, contract *PerfContract) Verdict {
	if bundle == nil || bundle.Status != StatusSucceeded || bundle.Plan == nil {
		// A plan that was never captured is an execution failure, not a wrong
		// answer: it is reported with the bundle's own error code, exactly as
		// gradePlan does in the browser.
		code := "ENVIRONMENT_UNAVAILABLE"
		detail := "no plan was captured"
		if bundle != nil && bundle.Error != nil && bundle.Error.Code != "" {
			code = bundle.Error.Code
			detail = bundle.Error.Message
		}
		return Verdict{Failures: []Failure{{
			Code:   code,
			Label:  "The plan could not be captured",
			Detail: detail,
		}}}
	}
	if contract == nil {
		return Verdict{Passed: true, Failures: []Failure{}}
	}

	summary := bundle.Summary
	var failures []Failure

	if contract.Engine != "" && contract.Engine != bundle.Engine {
		failures = append(failures, Failure{
			Code:   CodeDialectMismatch,
			Label:  fmt.Sprintf("Runs on %s (got %s)", contract.Engine, engineName(bundle.Engine)),
			Detail: fmt.Sprintf("bundle engine %q, contract engine %q", bundle.Engine, contract.Engine),
		})
	}

	for _, want := range contract.RequireNodes {
		if !anyNode(summary.Nodes, func(n *PlanNode) bool { return nodeMatches(n, want) }) {
			failures = append(failures, Failure{
				Code:   CodeNodeMissing,
				Label:  requireLabel(want),
				Detail: fmt.Sprintf("no %s node%s in the plan", want.NodeType, relationClause(want.Relation)),
			})
		}
	}

	for _, forbid := range contract.ForbidNodes {
		threshold := 0.0
		if forbid.AbovePlanRows != nil {
			threshold = *forbid.AbovePlanRows
		}
		var present bool
		var measured float64
		for i := range summary.Nodes {
			n := &summary.Nodes[i]
			if n.NodeType != forbid.NodeType || n.Relation == nil || *n.Relation != forbid.Relation {
				continue
			}
			rows := 0.0
			if n.EstimatedRows != nil {
				rows = *n.EstimatedRows
			}
			if rows > measured {
				measured = rows
			}
			// The threshold is exclusive: the schema forbids the node
			// "above a row threshold", so a relation the planner expects to
			// read at *or below* it is a correct plan and stays legal.
			if rows > threshold {
				present = true
			}
		}
		if present {
			failures = append(failures, Failure{
				Code:   CodeNodeForbidden,
				Label:  forbidLabel(forbid, threshold),
				Detail: fmt.Sprintf("%s on %s estimated %.0f rows (threshold %.0f)", forbid.NodeType, forbid.Relation, measured, threshold),
			})
		}
	}

	for _, index := range contract.RequireIndexes {
		if !contains(summary.IndexesUsed, index) {
			failures = append(failures, Failure{
				Code:   CodeIndexNotUsed,
				Label:  fmt.Sprintf("Uses index %s", index),
				Detail: fmt.Sprintf("indexes used: %v", summary.IndexesUsed),
			})
		}
	}

	if contract.MinIndexScans != nil && len(summary.IndexScans) < *contract.MinIndexScans {
		failures = append(failures, Failure{
			Code:   CodeIndexNotUsed,
			Label:  fmt.Sprintf("Uses at least %d index scan(s)", *contract.MinIndexScans),
			Detail: fmt.Sprintf("plan has %d index scan(s)", len(summary.IndexScans)),
		})
	}

	if contract.MaxPlanRows != nil && summary.PlanRows > *contract.MaxPlanRows {
		failures = append(failures, Failure{
			Code:   CodePlanRowsExceeded,
			Label:  fmt.Sprintf("Planner expects at most %.0f rows", *contract.MaxPlanRows),
			Detail: fmt.Sprintf("planner expects %.0f rows", summary.PlanRows),
		})
	}

	if failures == nil {
		failures = []Failure{}
	}
	return Verdict{Passed: len(failures) == 0, Failures: failures}
}

// nodeMatches is the same predicate planContract.ts uses: the node type must
// match, and a non-nil relation or index narrows it.
func nodeMatches(node *PlanNode, want PerfRequireNode) bool {
	if node.NodeType != want.NodeType {
		return false
	}
	if want.Relation != nil && (node.Relation == nil || *node.Relation != *want.Relation) {
		return false
	}
	if want.Index != nil && (node.Index == nil || *node.Index != *want.Index) {
		return false
	}
	return true
}

func anyNode(nodes []PlanNode, match func(*PlanNode) bool) bool {
	for i := range nodes {
		if match(&nodes[i]) {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func engineName(engine string) string {
	if engine == "" {
		return "an unknown engine"
	}
	return engine
}

func relationClause(relation *string) string {
	if relation == nil {
		return ""
	}
	return " on " + *relation
}

func requireLabel(want PerfRequireNode) string {
	if want.Label != nil {
		return *want.Label
	}
	return "Plan contains a " + want.NodeType + " node" + relationClause(want.Relation)
}

func forbidLabel(forbid PerfForbidNode, threshold float64) string {
	if forbid.Label != nil {
		return *forbid.Label
	}
	label := fmt.Sprintf("No %s on %s", forbid.NodeType, forbid.Relation)
	if threshold > 0 {
		label += fmt.Sprintf(" above %.0f estimated rows", threshold)
	}
	return label
}
