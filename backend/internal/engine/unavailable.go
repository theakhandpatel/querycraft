package engine

import (
	"context"
	"fmt"
)

// UnavailableExecutor is used when no execution database is configured. It lets
// the service run (health, capabilities, dataset introspection, in-memory
// progress) while honestly reporting that SQL execution is unavailable.
type UnavailableExecutor struct{ Reason string }

func (u UnavailableExecutor) err() error {
	reason := u.Reason
	if reason == "" {
		reason = "no execution database configured (set QC_DATABASE_URL)"
	}
	return fmt.Errorf("%w: %s", ErrEnvironmentUnavailable, reason)
}

func (u UnavailableExecutor) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	return nil, u.err()
}

func (u UnavailableExecutor) Ping(ctx context.Context) error { return u.err() }

func (u UnavailableExecutor) Version(ctx context.Context) (string, error) { return "", u.err() }

func (u UnavailableExecutor) Close() error { return nil }
