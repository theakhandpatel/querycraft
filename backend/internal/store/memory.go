package store

import (
	"context"
	"fmt"
)

/* Memory is the degraded-mode file of package store: the gateway opens NewMemory
 * when no metadata database is configured. It runs the SQLite backend over an
 * in-memory database, so degraded mode exercises the same statements as the
 * persistent backends and only the persistence differs.
 */

// Memory is the in-process metadata store. It wraps the SQLite backend over an
// in-memory database; Kind stays "memory" so diagnostics can say the data is
// not kept.
type Memory struct {
	*SQLite
}

// NewMemory opens an empty in-memory store.
func NewMemory() *Memory {
	st, err := NewSQLite(context.Background(), ":memory:")
	if err != nil {
		// Opening an in-memory SQLite database has no environmental input to
		// fail on; a failure here is a broken build, and there is no lower
		// gear for a gateway with no metadata database to drop to.
		panic(fmt.Sprintf("store: open the in-memory metadata store: %v", err))
	}
	return &Memory{SQLite: st}
}

// Kind implements Store. It is the one answer degraded mode changes: the
// diagnostics view reads it to say the data is not persisted, and the
// environment reaper skips the boot reaper for it.
func (m *Memory) Kind() string { return "memory" }

var _ Store = (*Memory)(nil)
