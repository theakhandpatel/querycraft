package dataset

// A dataset's scale, as reported in PlanBundle.environment.scale_factor.
//
// The exit gate says a plan result must report engine version, scale and
// indexes. A plan shown without its scale is unreadable, because the whole point
// of the lesson is that the right access path depends on how much data there is.
// A constant would report a 10,000-row plan as `scale_factor: 1`.
//
// The number is a *declaration* rather than a measurement: a plan has to name
// the scale it was authored against even before statistics exist, and
// `reltuples` is an estimate that goes stale. It is the row count of the
// dataset's fact relation (`orders`), and TestScaleFactorMatchesTheSeed counts
// the real rows so the declaration cannot drift from the seeds.

const (
	// CommerceScaleFactor is the shared dataset's order count (seed.sql).
	CommerceScaleFactor = 80
)

// FactRowCount returns the declared scale of a dataset: the number of rows in
// its fact relation. An empty id is the default commerce dataset, which is what
// a plan request that names no dataset plans.
func FactRowCount(id string) int {
	if id == PerfDatasetID {
		return PerfScaleFactorOrders
	}
	return CommerceScaleFactor
}
