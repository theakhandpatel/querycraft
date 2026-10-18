package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* Dataset routing (M5).
 *
 * Querycraft serves two fixtures: `commerce`, the shared dataset every lesson
 * starts from, and `perf`, the larger fixture the PostgreSQL query plans course
 * runs against. The perf fixture exists only as a per-account writable
 * environment, with its own sealed template, because the plan lessons ask the
 * learner to CREATE INDEX.
 *
 * All three statement routes resolve the dataset the same way, through
 * resolveDataset, so a plan lesson is graded against the rows its authored
 * results were written for. /explain, /query and /answers/evaluate must agree,
 * or an exercise in the plan course compares perf SQL against commerce results.
 */

const (
	// commerceDatasetID names the shared dataset. The empty string is the same
	// dataset by omission and stays the canonical form, so an anonymous request
	// that names no dataset keeps exactly the path it always had.
	commerceDatasetID = "commerce"

	// perfSignInRefusal is the sentence an anonymous perf request is refused
	// with. All three statement routes share one wording, because the fix is the
	// same on each: sign in.
	perfSignInRefusal = "The perf dataset lives in a signed-in learner's own environment. Sign in and run the lesson again."
)

// resolveDataset picks the dataset a statement request runs against.
//
// The explicit field wins. Otherwise an exercise authored for the perf fixture
// (exercise.perf) implies perf. Everything else is commerce, including "" and
// the literal "commerce": both resolve to "" so the anonymous commerce path is
// byte-for-byte the path it always was.
//
// An unrecognised value is returned untouched (lower-cased and trimmed):
// resolving must not silently swallow a typo, so the caller refuses it by name.
func resolveDataset(explicit string, exercise *planExercise) string {
	if id := strings.ToLower(strings.TrimSpace(explicit)); id != "" {
		if id == commerceDatasetID {
			return ""
		}
		return id
	}
	if exercise != nil && exercise.Perf != nil {
		return dataset.PerfDatasetID
	}
	return ""
}

// unknownDatasetMessage names both datasets this gateway serves. It is shared
// by /query, /answers/evaluate and /explain so the three refusals cannot drift.
func unknownDatasetMessage(requested string) string {
	return fmt.Sprintf("This gateway serves the %q and %q datasets; %q is not one of them.",
		commerceDatasetID, dataset.PerfDatasetID, requested)
}

// refusingDataset refuses a dataset this gateway does not serve, naming the two
// it does with the same code and shape the unknown-dataset refusal on /explain
// uses: ENVIRONMENT_UNAVAILABLE, because the row is not that there is something
// wrong with the request but that this gateway cannot provide that fixture.
func (s *Server) refusingDataset(ctx context.Context, requested string) engine.QueryResult {
	return engine.Failing(engine.CodeEnvironmentUnavailable,
		unknownDatasetMessage(requested), "", engine.RequestIDFromContext(ctx))
}

// refusingPerfDialect refuses a perf request in a dialect the perf lab cannot
// run. Perf is a PostgreSQL lab (ADR 0004: it needs EXPLAIN ANALYZE and a cost
// model), so the refusal is DIALECT_MISMATCH and names PostgreSQL, the dialect
// the perf dataset is served in, rather than the dialect this gateway runs
// first.
func (s *Server) refusingPerfDialect(ctx context.Context, requested string) engine.QueryResult {
	message := fmt.Sprintf("The %q dataset is a PostgreSQL lab, so it cannot serve a chapter written for %s.",
		dataset.PerfDatasetID, requested)
	return engine.RefusingDialect(engine.DialectPostgres, message, engine.RequestIDFromContext(ctx))
}

// perfWorkspace resolves the signed-in learner's perf environment, apart from
// their challenge workspace because a plan lesson asks the learner to CREATE
// INDEX. The error says why none could be provided; each caller keeps its own
// learner-facing sentence for it.
func (s *Server) perfWorkspace(ctx context.Context, r *http.Request) (engine.Executor, error) {
	u, signedIn := userFrom(r.Context())
	if !signedIn {
		return nil, errPerfAnonymous
	}
	if s.envs == nil {
		return nil, errPerfNoManager
	}
	return s.envs.EnsurePerf(ctx, u.ID)
}

// errPerfAnonymous and errPerfNoManager classify the two perf refusals that
// happen before EnsurePerf is reached, so a caller keeps the sentence that fits
// its shape.
var (
	errPerfAnonymous = errors.New("anonymous caller has no perf environment")
	errPerfNoManager = errors.New("no writable workspace manager is configured")
)

// perfExecutor returns the executor the perf dataset is served from. A refusal
// is returned whole rather than as an error because the anonymous case has a
// learner-facing sentence of its own and every statement route must answer it
// identically.
func (s *Server) perfExecutor(ctx context.Context, r *http.Request) (engine.Executor, *engine.QueryResult) {
	exec, err := s.perfWorkspace(ctx, r)
	if err != nil {
		res := workspaceRefusal(ctx, perfExecutorMessage(err), err)
		return nil, &res
	}
	return exec, nil
}

// perfExecutorMessage keeps the statement route's own sentence for each way the
// perf workspace can be missing.
func perfExecutorMessage(err error) string {
	switch {
	case errors.Is(err, errPerfAnonymous):
		return perfSignInRefusal
	case errors.Is(err, errPerfNoManager):
		return "The learner workspace is unavailable."
	default:
		return "The perf environment could not be prepared."
	}
}
