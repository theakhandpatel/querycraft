package engine

import (
	"context"
	"fmt"
)

// UnavailableExecutor is used when no execution database is configured. It lets
// the service run (health, capabilities, dataset introspection, in-memory
// progress) while honestly reporting that SQL execution is unavailable.
type UnavailableExecutor struct{ Reason string }

// reason is the human-readable half of err(), reused by Ready and
// Diagnostics so the same sentence appears in the log, the error and the
// diagnostics view.
func (u UnavailableExecutor) reason() string {
	if u.Reason != "" {
		return RedactSecrets(u.Reason)
	}
	return "no execution database configured (set QC_DATABASE_URL)"
}

func (u UnavailableExecutor) err() error {
	return fmt.Errorf("%w: %s", ErrEnvironmentUnavailable, u.reason())
}

func (u UnavailableExecutor) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return nil, u.err()
}

func (u UnavailableExecutor) Ping(ctx context.Context) error { return u.err() }

func (u UnavailableExecutor) Version(ctx context.Context) (string, error) { return "", u.err() }

// Stats implements Executor: there is no pool, so the honest report is zero
// rather than a maximum that would suggest capacity.
func (u UnavailableExecutor) Stats() PoolStats { return PoolStats{} }

// Ready implements Executor.
func (u UnavailableExecutor) Ready(ctx context.Context) (bool, string) {
	return false, u.reason()
}

// Diagnostics implements Diagnostician.
func (u UnavailableExecutor) Diagnostics(ctx context.Context) map[string]any {
	return map[string]any{"unavailable": true, "reason": u.reason()}
}

func (u UnavailableExecutor) Close() error { return nil }
