// Package perf owns the M5 performance lab: the plan facts a captured EXPLAIN
// reports and the structural grading that reads them.
//
// A plan is reported, never timed. PlanBundle mirrors
// contracts/plan.schema.json and web/src/api/planContract.ts field for field, so
// the browser adapter and the gateway cannot describe the same plan two
// different ways; Grade reads only the deterministic half (Summary) and never a
// timing field.
package perf

// Engine names. PostgreSQL is the only dialect M5 grades (ADR 0004); the field
// exists so a second dialect can be added without reshaping the contract.
const EnginePostgres = "postgresql"

// Bundle status and timing precision, from contracts/plan.schema.json.
const (
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"

	TimingAnalyze  = "analyze"  // executed, so per-node timings are measured
	TimingEstimate = "estimate" // planner cost only, no timings exist
	TimingNone     = "none"     // the request failed, so there is nothing to show
)

// Warning codes. They are exactly the enum in contracts/plan.schema.json: a new
// warning is a schema change, not a new string at a call site.
const (
	WarnSeqScanLargeTable  = "PLAN_SEQ_SCAN_LARGE_TABLE"
	WarnEstimateOff        = "PLAN_ESTIMATE_OFF_BY_ORDERS_OF_MAGNITUDE"
	WarnNodeCapReached     = "PLAN_NODE_CAP_REACHED"
	WarnRowCapReached      = "PLAN_ROW_CAP_REACHED"
	WarnSettingsNonDefault = "PLAN_SETTINGS_NON_DEFAULT"
	WarnAnalyzeSkipped     = "PLAN_ANALYZE_SKIPPED_NO_ENVIRONMENT"
)

// PlanRoot is the root plan node exactly as the engine emitted it, verbatim and
// untrusted. It is reported for the viewer and is never graded; a nil map
// marshals as JSON null, which is what a failed bundle carries.
type PlanRoot map[string]any

// PlanNode is one node of the flat, depth-first walk the viewer renders.
// Pointers keep "the engine did not report this" distinct from a real zero;
// Warnings is never nil so the array marshals as [] and not null.
type PlanNode struct {
	Depth              int      `json:"depth"`
	NodeType           string   `json:"node_type"`
	Relation           *string  `json:"relation"`
	Alias              *string  `json:"alias"`
	Index              *string  `json:"index"`
	JoinType           *string  `json:"join_type"`
	ParentRelationship *string  `json:"parent_relationship"`
	EstimatedRows      *float64 `json:"estimated_rows"`
	EstimatedCost      *float64 `json:"estimated_cost"`
	ActualRows         *float64 `json:"actual_rows"`
	ActualTimeMS       *float64 `json:"actual_time_ms"`
	Loops              *float64 `json:"loops"`
	BuffersRead        *float64 `json:"buffers_read"`
	Filter             *string  `json:"filter"`
	IndexCondition     *string  `json:"index_condition"`
	SortKey            []string `json:"sort_key"`
	Warnings           []string `json:"warnings"`
}

// PlanSeqScan is a relation read by a sequential scan, with the planner's own
// row estimate, so a caller can judge whether the scan was a choice or a
// mistake.
type PlanSeqScan struct {
	Relation string  `json:"relation"`
	PlanRows float64 `json:"plan_rows"`
}

// PlanIndexScan is one index access path.
type PlanIndexScan struct {
	Relation string `json:"relation"`
	Index    string `json:"index"`
	NodeType string `json:"node_type"`
}

// PlanSummary is the deterministic, gradeable half of a bundle. Everything
// Grade reads lives here; no field is a timing.
type PlanSummary struct {
	NodeCount        int             `json:"node_count"`
	NodeTypes        []string        `json:"node_types"`
	Relations        []string        `json:"relations"`
	IndexesUsed      []string        `json:"indexes_used"`
	SeqScans         []PlanSeqScan   `json:"seq_scans"`
	IndexScans       []PlanIndexScan `json:"index_scans"`
	MaxEstimatedRows float64         `json:"max_estimated_rows"`
	PlanRows         float64         `json:"plan_rows"`
	TotalCost        float64         `json:"total_cost"`
	Nodes            []PlanNode      `json:"nodes"`
}

// PlanIndex is one index the plan was produced against.
type PlanIndex struct {
	Relation   string  `json:"relation"`
	Name       string  `json:"name"`
	Definition *string `json:"definition"`
}

// PlanEnvironment is what the plan was produced against, so a number is never
// reported without the scale and settings that produced it (M5 exit gate).
type PlanEnv struct {
	Engine        string             `json:"engine"`
	EngineVersion string             `json:"engine_version"`
	ScaleFactor   int                `json:"scale_factor"`
	Dataset       *string            `json:"dataset"`
	RowCounts     map[string]float64 `json:"row_counts"`
	Indexes       []PlanIndex        `json:"indexes"`
	Settings      map[string]string  `json:"settings"`
}

// PlanWarning is a stable code plus the engine's own words, both bound by the
// schema enum so no call site can invent a code.
type PlanWarning struct {
	Code     string  `json:"code"`
	Message  string  `json:"message"`
	Relation *string `json:"relation,omitempty"`
	NodeType *string `json:"node_type,omitempty"`
}

// PlanError is the same envelope as QueryResult, so one error taxonomy serves
// both surfaces.
type PlanError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Detail  *string `json:"detail"`
}

// PlanBundle matches contracts/plan.schema.json exactly.
type PlanBundle struct {
	Status          string        `json:"status"`
	Analyze         bool          `json:"analyze"`
	TimingPrecision string        `json:"timing_precision"`
	Engine          string        `json:"engine"`
	Plan            PlanRoot      `json:"plan"`
	Summary         PlanSummary   `json:"summary"`
	Environment     PlanEnv       `json:"environment"`
	Warnings        []PlanWarning `json:"warnings"`
	DurationMS      float64       `json:"duration_ms"`
	Statement       *string       `json:"statement"`
	RequestID       *string       `json:"request_id"`
	Error           *PlanError    `json:"error"`
}

// EmptySummary is the summary of a plan that was never captured: zero nodes and
// empty arrays rather than nulls (the M4 lesson: a nil slice violates
// "type":"array").
func EmptySummary() PlanSummary {
	return PlanSummary{
		NodeTypes:   []string{},
		Relations:   []string{},
		IndexesUsed: []string{},
		SeqScans:    []PlanSeqScan{},
		IndexScans:  []PlanIndexScan{},
		Nodes:       []PlanNode{},
	}
}

// EmptyEnvironment says nothing was measured rather than reporting a scale the
// engine never saw. It mirrors emptyEnvironment() in planContract.ts.
func EmptyEnvironment(engine string) PlanEnv {
	return PlanEnv{
		Engine:        engine,
		EngineVersion: "",
		ScaleFactor:   0,
		Dataset:       nil,
		RowCounts:     map[string]float64{},
		Indexes:       []PlanIndex{},
		Settings:      map[string]string{},
	}
}

// FailedBundle is an honest refusal: no plan was produced, so no numbers are
// invented. It mirrors failedPlanBundle() in planContract.ts, which is what
// keeps a refusal on the gateway and a refusal in the browser identical.
func FailedBundle(engine, code, message, detail string) *PlanBundle {
	var detailPtr *string
	if detail != "" {
		detailPtr = &detail
	}
	return &PlanBundle{
		Status:          StatusFailed,
		Analyze:         false,
		TimingPrecision: TimingNone,
		Engine:          engine,
		Plan:            nil,
		Summary:         EmptySummary(),
		Environment:     EmptyEnvironment(engine),
		Warnings:        []PlanWarning{},
		DurationMS:      0,
		Statement:       nil,
		RequestID:       nil,
		Error:           &PlanError{Code: code, Message: message, Detail: detailPtr},
	}
}
