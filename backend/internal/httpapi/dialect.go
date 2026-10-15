package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* Dialect routing (M6 step 4).
 *
 * POST /query, POST /explain and POST /answers/evaluate all take an optional
 * top-level `dialect`. Absent means PostgreSQL — the default dialect (ADR 0004)
 * — so every existing client keeps the behaviour it already had. A request for
 * a dialect this gateway serves is routed to that dialect's executor; a request
 * for one it does not serve is refused with DIALECT_MISMATCH naming the
 * dialects it *can* serve, because "switch the chapter, or switch the gateway"
 * is the whole fix and a silent fallback to the default engine would grade the
 * wrong language.
 */

// requestedDialect normalises the request field. The zero value is the default
// dialect, not an error: an old client that has never heard of `dialect` is
// asking for PostgreSQL by omission.
func requestedDialect(raw string) string {
	name := strings.ToLower(strings.TrimSpace(raw))
	if name == "" {
		return engine.DialectPostgres
	}
	return name
}

// statementExecutorFor returns the executor the caller's statement should run
// on, or a refusal built from the shared error taxonomy. The second return is
// set only when no executor could be chosen; the refusal is a whole QueryResult
// so /query and /answers/evaluate answer identically.
//
// The dataset is resolved before the dialect because it can make an otherwise
// served dialect unservable: the perf lab is a PostgreSQL lab.
func (s *Server) statementExecutorFor(ctx context.Context, r *http.Request, dialect, datasetID string) (engine.Executor, *engine.QueryResult) {
	switch datasetID {
	case "":
		// Commerce: the shared read-only dataset, or the learner's own
		// workspace when signed in. This is the path every client used before
		// the dataset field existed, and it is unchanged.
		return s.statementExecutorOnDialect(ctx, r, dialect)

	case dataset.PerfDatasetID:
		if dialect != engine.DialectPostgres {
			res := s.refusingPerfDialect(ctx, dialect)
			return nil, &res
		}
		return s.perfExecutor(ctx, r)

	default:
		res := s.refusingDataset(ctx, datasetID)
		return nil, &res
	}
}

// statementExecutorOnDialect routes a commerce statement to the executor that
// speaks the requested dialect.
func (s *Server) statementExecutorOnDialect(ctx context.Context, r *http.Request, dialect string) (engine.Executor, *engine.QueryResult) {
	switch dialect {
	case engine.DialectPostgres:
		exec, err := s.statementExecutor(ctx, r)
		if err != nil {
			res := workspaceRefusal(ctx, "The learner workspace is unavailable.", err)
			return nil, &res
		}
		return exec, nil

	case engine.DialectSQLite:
		// A registered store is what makes SQLite a served dialect; without one
		// this is a dialect the gateway cannot run, not a broken environment, so
		// the refusal is DIALECT_MISMATCH and names PostgreSQL.
		if s.sqlite == nil {
			res := s.refusingDialect(ctx, dialect)
			return nil, &res
		}
		exec, err := s.sqliteExecutor(ctx, r)
		if err != nil {
			res := workspaceRefusal(ctx, "The SQLite environment is unavailable.", err)
			return nil, &res
		}
		return exec, nil

	default:
		res := s.refusingDialect(ctx, dialect)
		return nil, &res
	}
}

// sqliteExecutor picks the shared read-only fixture for an anonymous caller and
// the learner's own writable file for a signed-in one — ADR 0003's split, with
// a file in place of a copied database.
func (s *Server) sqliteExecutor(ctx context.Context, r *http.Request) (engine.Executor, error) {
	if u, ok := userFrom(r.Context()); ok {
		return s.sqlite.ForAccount(ctx, u.ID)
	}
	return s.sqlite.Shared(ctx)
}

// refusingDialect builds the DIALECT_MISMATCH result. The message names both
// sides: the dialect the request asked for, and the dialects this gateway
// actually serves, because the pair is the whole fix and neither half alone
// tells the learner what to do.
func (s *Server) refusingDialect(ctx context.Context, requested string) engine.QueryResult {
	served := s.Dialects()
	running := engine.DialectPostgres
	if len(served) > 0 {
		// Dialects is kept sorted, so the first entry is stable across requests
		// and the result's `dialect` field does not flap.
		running = served[0]
	}
	message := fmt.Sprintf("This gateway runs %s, so it cannot serve a chapter written for %s.",
		humanList(served), requested)
	return engine.RefusingDialect(running, message, engine.RequestIDFromContext(ctx))
}

// humanList renders a list the way a sentence needs it: "a", "a and b",
// "a, b and c".
func humanList(items []string) string {
	switch len(items) {
	case 0:
		return "no dialects"
	case 1:
		return items[0]
	case 2:
		return items[0] + " and " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}
