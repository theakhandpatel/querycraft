package envdb

import (
	"context"
	"errors"
	"strings"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
)

// ScratchPrefix namespaces the throwaway environments the M4 generation
// pipeline validates against. The colon keeps them out of the space of real
// account ids, so a scratch environment can never collide with a learner's
// workspace.
const ScratchPrefix = "gen:"

// Scratch implements ai.Runner: it hands the generation pipeline a throwaway
// environment and a release function that drops it again.
//
// The isolation is deliberately the same one a learner gets (ADR 0003): the
// pipeline runs the reference solution, the cross-check and the adversarial
// fixtures over the restricted learner connection, on a database copied from
// the sealed template. Validation therefore cannot touch the shared dataset,
// cannot see a learner's writes, and cannot run on the administrative
// connection — which is the whole reason the pipeline takes an Executor rather
// than a DSN.
func (m *Manager) Scratch(ctx context.Context, name string) (engine.Executor, func(), error) {
	account := ScratchPrefix + strings.TrimSpace(name)
	if strings.TrimSpace(name) == "" {
		return nil, nil, errors.New("envdb: empty scratch name")
	}
	exec, err := m.Ensure(ctx, account)
	if err != nil {
		return nil, nil, err
	}
	release := func() {
		// Best effort and detached: the pipeline calls this from a defer, and a
		// cancelled request context must not leave the database behind. A drop
		// that fails is logged by the reaper's own sweep rather than retried
		// here, which would block the caller.
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), dropTimeout)
		defer cancel()
		if err := m.Drop(dctx, account); err != nil {
			m.opts.Log.Warn("drop scratch environment", "account", account, "error", err)
		}
	}
	return exec, release, nil
}

// Drop removes an environment and forgets its pool. A learner's workspace is
// normally reset rather than dropped; this is for the generation pipeline's
// throwaway copies, which have no reason to outlive one run.
func (m *Manager) Drop(ctx context.Context, accountID string) error {
	if strings.TrimSpace(accountID) == "" {
		return errors.New("envdb: empty account id")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return errClosed()
	}
	key := digest(accountID)
	database := m.databaseName(accountID)
	if e, ok := m.envs[key]; ok {
		// Close the pool before the drop, so no pool outlives the database it
		// points at — the same order Close uses.
		e.exec.Close()
		delete(m.envs, key)
	}
	if err := dataset.DropDatabase(ctx, m.admin, database); err != nil {
		return envErr(err)
	}
	m.publishActiveLocked()
	return nil
}
