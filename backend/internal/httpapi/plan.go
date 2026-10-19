package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/perf"
)

/* POST /explain: the M5 plan surface (design section 14).
 *
 * A plan is captured under the same deadline, semaphore and row/byte caps as a
 * query, because EXPLAIN ANALYZE executes the learner's statement and a runaway
 * analyze has to be stopped by the host exactly like a runaway query. Two rules
 * shape the route:
 *
 *   - EXPLAIN ANALYZE executes the statement, so it runs only in a writable
 *     environment; an anonymous request is refused with ENVIRONMENT_UNAVAILABLE
 *     rather than silently downgraded to a plain EXPLAIN.
 *   - With no database the route reports ENVIRONMENT_UNAVAILABLE. It never
 *     invents a plan.
 *
 * The body may carry the exercise's PerfContract. The gateway does not grade it
 * (the browser and backend/internal/perf both read PlanBundle.summary and must
 * agree, M5 step 5), but it does read the dialect the exercise was authored
 * for. A SQLite chapter is refused with an actionable message instead of being
 * graded against PostgreSQL facts.
 */

// planRequest is the POST /explain body.
type planRequest struct {
	statementRequest
	SQL      string        `json:"sql"`
	Analyze  bool          `json:"analyze"`
	Exercise *planExercise `json:"exercise"`
	Limits   engine.Limits `json:"limits"`
}

type planExercise struct {
	Perf *perf.PerfContract `json:"perf"`
}

func (s *Server) handleExplain(w http.ResponseWriter, r *http.Request) {
	var req planRequest
	if !readJSON(w, r, &req) {
		return
	}
	start := time.Now()
	limits := s.limits(req.Limits)
	// The concurrency budget is spent out of the same deadline the plan gets, so
	// a gateway at capacity answers QUERY_TIMEOUT instead of queueing unbounded.
	admission, cancelAdmission := context.WithTimeout(r.Context(), limits.Timeout())
	defer cancelAdmission()

	release, admitted := s.enter(admission)
	if !admitted {
		bundle := s.refusedPlan(admission, start, req.SQL, engine.CodeTimeout, s.busyResult(admission, start).Error.Message, "")
		s.recordPlan(bundle, req.Analyze)
		writePlan(w, http.StatusBadRequest, bundle)
		return
	}
	defer release()

	// The plan lab is PostgreSQL-only (ADR 0004: SQLite has neither
	// EXPLAIN ANALYZE nor a cost model of this shape), so a request for another
	// dialect is refused with a sentence that names the one that works.
	// DIALECT_MISMATCH is in QueryResult's enum, not this contract's, so the
	// refusal is EVALUATION_ERROR, the same code a chapter authored for another
	// dialect gets.
	if dialect := requestedDialect(req.Dialect); dialect != engine.DialectPostgres {
		bundle := s.refusedPlan(admission, start, req.SQL, engine.CodeEvaluationError,
			s.planDialectRefusal(dialect), "")
		s.recordPlan(bundle, req.Analyze)
		writePlan(w, http.StatusBadRequest, bundle)
		return
	}

	if req.Exercise != nil && req.Exercise.Perf != nil {
		if dialect := strings.TrimSpace(req.Exercise.Perf.Engine); dialect != "" && dialect != perf.EnginePostgres {
			bundle := s.refusedPlan(admission, start, req.SQL, engine.CodeEvaluationError,
				s.planDialectRefusal(dialect), "")
			s.recordPlan(bundle, req.Analyze)
			writePlan(w, http.StatusBadRequest, bundle)
			return
		}
	}

	// The dataset is resolved by the same helper /query and /answers/evaluate
	// use: an explicit field wins, exercise.perf implies the perf fixture, and
	// everything else is commerce. An explicit "commerce" is the commerce
	// dataset, not an unknown id.
	datasetID := resolveDataset(req.Dataset, req.Exercise)

	exec, reason, err := s.planExecutor(r.Context(), r, datasetID, req.Analyze)
	if err != nil {
		bundle := s.refusedPlan(admission, start, req.SQL, engine.CodeEnvironmentUnavailable,
			reason, engine.RedactSecrets(err.Error()))
		s.recordPlan(bundle, req.Analyze)
		writePlan(w, http.StatusBadRequest, bundle)
		return
	}

	// The plan gets its full deadline from here: choosing the executor above
	// may have provisioned the perf workspace, which the environment manager
	// bounds on its own provisioning deadline rather than this one.
	ctx, cancel := context.WithTimeout(r.Context(), limits.Timeout())
	defer cancel()
	bundle := perf.Explain(ctx, exec, req.SQL, perf.ExplainOptions{
		Analyze: req.Analyze,
		Limits:  limits,
		Dataset: datasetID,
		// The dataset's declared scale, not a constant: the environment block is
		// what tells a learner how much data a plan was chosen against.
		ScaleFactor: dataset.FactRowCount(datasetID),
	})
	s.recordPlan(bundle, req.Analyze)
	status := http.StatusOK
	if bundle.Status != perf.StatusSucceeded {
		status = http.StatusBadRequest
	}
	writePlan(w, status, bundle)
}

// planExecutor returns the executor the plan must be captured on.
//
// The perf dataset exists only as a per-account writable environment (the
// lessons ask the learner to CREATE INDEX), and EXPLAIN ANALYZE executes the
// statement, so both need a workspace. A plain EXPLAIN still plans, so an
// anonymous caller may plan the shared read-only dataset, but not analyze it.
// The first return is the executor; the second is the learner-facing sentence a
// refusal shows (the error's own text is the machine detail underneath it).
func (s *Server) planExecutor(ctx context.Context, r *http.Request, datasetID string, analyze bool) (engine.Executor, string, error) {
	_, signedIn := userFrom(r.Context())

	const needsWorkspace = "EXPLAIN ANALYZE executes the statement, so it needs a writable learner environment. Sign in to analyze; a plain EXPLAIN still works anonymously."

	switch datasetID {
	case "":
		// The shared dataset. An anonymous plan is read-only; a plan of a
		// signed-in learner runs in their own environment so that an index they
		// added is the one the plan reflects.
		if !signedIn {
			if analyze {
				return nil, needsWorkspace, fmt.Errorf("%w: anonymous caller cannot analyze", engine.ErrEnvironmentUnavailable)
			}
			return s.exec, "", nil
		}
		exec, err := s.statementExecutor(ctx, r)
		if err != nil {
			return nil, "The learner workspace is unavailable, so the plan cannot be captured.", err
		}
		return exec, "", nil

	case dataset.PerfDatasetID:
		exec, err := s.perfWorkspace(ctx, r)
		if err != nil {
			return nil, planPerfMessage(err), err
		}
		return exec, "", nil

	default:
		return nil, unknownDatasetMessage(datasetID), fmt.Errorf("%w: unknown dataset %q", engine.ErrEnvironmentUnavailable, datasetID)
	}
}

// planPerfMessage keeps the plan route's own sentence for each way the perf
// workspace can be missing; the resolver owns only the resolution.
func planPerfMessage(err error) string {
	switch {
	case errors.Is(err, errPerfAnonymous):
		return perfSignInRefusal
	case errors.Is(err, errPerfNoManager):
		return "No writable workspace manager is configured, so the perf dataset cannot be provided."
	default:
		return "The perf environment could not be prepared."
	}
}

// writePlan writes a plan bundle and, when the capture failed, names the
// bundle's error code for the access log.
func writePlan(w http.ResponseWriter, status int, bundle *perf.PlanBundle) {
	if bundle != nil && bundle.Error != nil {
		noteError(w, bundle.Error.Code)
	}
	writeJSON(w, status, bundle)
}

// refusedPlan builds the honest refusal: no plan, no numbers, one stable code
// from the shared taxonomy, and the statement and correlation id so the request
// is not anonymous in the logs.
func (s *Server) refusedPlan(ctx context.Context, start time.Time, statement, code, message, detail string) *perf.PlanBundle {
	bundle := perf.FailedBundle(perf.EnginePostgres, code, message, detail)
	bundle.DurationMS = engine.RoundMS(time.Since(start))
	bundle.RequestID = engine.RequestIDFromContext(ctx)
	if strings.TrimSpace(statement) != "" {
		bundle.Statement = &statement
	}
	return bundle
}

// planDialectRefusal names both sides of a plan-lab dialect mismatch: the one
// dialect the lab can capture, and the one the request asked for.
func (s *Server) planDialectRefusal(requested string) string {
	return fmt.Sprintf("The plan lab captures PostgreSQL plans only, so it cannot serve %s. This gateway runs %s.",
		requested, humanList(s.Dialects()))
}

// planCapability describes the plan lab without probing the database. The route
// is always served; a request against a gateway with no database gets the
// ENVIRONMENT_UNAVAILABLE bundle rather than a fabricated plan.
func (s *Server) planCapability() map[string]any {
	available, reason := true, ""
	if _, unavailable := s.exec.(engine.UnavailableExecutor); unavailable {
		available = false
		reason = "The execution database is unavailable, so no plan can be captured."
	}
	return map[string]any{
		"available":             available,
		"reason":                reason,
		"datasets":              []string{commerceDatasetID, dataset.PerfDatasetID},
		"analyze_requires_auth": true,
		"timing_precision":      []string{perf.TimingAnalyze, perf.TimingEstimate, perf.TimingNone},
		"node_cap":              perf.DefaultPlanNodeCap,
		"seq_scan_large_rows":   perf.SeqScanLargeRows,
	}
}
