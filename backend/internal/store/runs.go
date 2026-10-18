package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The agent-run transcript statements, written once for both SQL backends the
 * way metadata.go writes the account statements: ? placeholders, %s tables, and
 * the dialect supplying what the engines spell differently (see stmt, stamp and
 * encodeJSON). One row per run: the conversation the model had, stored as one
 * JSON array with the run's attribution beside it.
 */

const (
	// SaveRun upserts: one run may be stored more than once (a building course
	// updates its record as it writes chapters). The conflict clause keeps the
	// run's original started_at rather than overwriting it with the moment of
	// the latest write, so the record still says when the run began.
	saveRunSQL = `INSERT INTO %s (id, kind, owner_id, status, entries, entry_count, bytes, truncated, error, started_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		     kind        = excluded.kind,
		     owner_id    = excluded.owner_id,
		     status      = excluded.status,
		     entries     = excluded.entries,
		     entry_count = excluded.entry_count,
		     bytes       = excluded.bytes,
		     truncated   = excluded.truncated,
		     error       = excluded.error,
		     finished_at = excluded.finished_at`

	runSQL = `SELECT id, kind, owner_id, status, entries, entry_count, bytes, truncated, error, started_at, finished_at
		   FROM %s WHERE id = ?`

	runsListSQL = `SELECT id, kind, owner_id, status, entry_count, bytes, truncated, error, started_at, finished_at
		   FROM %s`
)

// SaveRun implements schema.Depot.
func (s *sqlStore) SaveRun(ctx context.Context, rec schema.RunTranscript) error {
	if rec.ID == "" {
		return errors.New("run id is required")
	}
	kind := rec.Kind
	if kind == "" {
		kind = schema.RunKindChallenge
	}
	status := rec.Status
	if status == "" {
		status = schema.StatusFailed
	}
	started := rec.StartedAt
	if started.IsZero() {
		started = time.Now().UTC()
	}
	finished := rec.FinishedAt
	if finished.IsZero() {
		finished = started
	}
	entries := rec.Entries
	if entries == nil {
		entries = []schema.TranscriptEntry{}
	}
	raw, err := s.jsonArg(entries)
	if err != nil {
		return fmt.Errorf("encode run transcript: %w", err)
	}
	// The stored entry_count is what the recorder measured, falling back to
	// what was actually written. A run whose entries were all refused by the
	// bound still reports how many it produced.
	count := rec.EntryCount
	if count < len(entries) {
		count = len(entries)
	}
	var errJSON any
	if rec.Error != nil {
		errJSON, err = s.jsonArg(rec.Error)
		if err != nil {
			return fmt.Errorf("encode run error: %w", err)
		}
	}
	return s.exec(ctx, s.stmt(saveRunSQL, "runs"),
		rec.ID, kind, rec.OwnerID, status, raw, count, rec.Bytes, rec.Truncated,
		errJSON, s.dialect.stamp(started), s.dialect.stamp(finished))
}

// Run implements schema.Depot. It returns the whole record including its owner,
// because the store does not know which session is reading. The caller must
// compare rec.OwnerID with the session's account before it returns a transcript
// to a client.
func (s *sqlStore) Run(ctx context.Context, id string) (schema.RunTranscript, bool, error) {
	var (
		rec     schema.RunTranscript
		entries []byte
		errJSON []byte
	)
	err := s.queryRow(ctx, s.stmt(runSQL, "runs"), id).
		Scan(&rec.ID, &rec.Kind, &rec.OwnerID, &rec.Status, &entries, &rec.EntryCount, &rec.Bytes,
			&rec.Truncated, &errJSON, timeColumn{&rec.StartedAt}, timeColumn{&rec.FinishedAt})
	if s.dialect.noRows(err) {
		return schema.RunTranscript{}, false, nil
	}
	if err != nil {
		return schema.RunTranscript{}, false, err
	}
	if err := decodeRun(&rec, entries, errJSON); err != nil {
		return schema.RunTranscript{}, false, err
	}
	if rec.Entries == nil {
		rec.Entries = []schema.TranscriptEntry{}
	}
	return rec, true, nil
}

// RunsFor lists one owner's run summaries in SQL, newest first (see
// CoursesFor). Only the summaries are read: a list of every transcript's
// entries would be the whole database on one request.
func (s *sqlStore) RunsFor(ctx context.Context, ownerID string) ([]schema.RunSummary, error) {
	return s.runsWhere(ctx, ownerID)
}

// runsWhere runs the shared list query. A non-empty ownerID narrows the SQL to
// that owner; an empty one lists every run. The order matches
// runs_owner_started_key.
func (s *sqlStore) runsWhere(ctx context.Context, ownerID string) ([]schema.RunSummary, error) {
	query := runsListSQL
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY started_at DESC, id DESC`
	rows, err := s.query(ctx, s.stmt(query, "runs"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []schema.RunSummary{}
	for rows.Next() {
		var (
			summary schema.RunSummary
			errJSON []byte
		)
		if err := rows.Scan(&summary.ID, &summary.Kind, &summary.OwnerID, &summary.Status,
			&summary.EntryCount, &summary.Bytes, &summary.Truncated, &errJSON,
			timeColumn{&summary.StartedAt}, timeColumn{&summary.FinishedAt}); err != nil {
			return nil, err
		}
		if len(errJSON) > 0 {
			var e schema.Error
			if err := json.Unmarshal(errJSON, &e); err != nil {
				return nil, fmt.Errorf("decode run error: %w", err)
			}
			summary.Error = &e
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

// decodeRun turns the JSON columns back into the stored types.
func decodeRun(rec *schema.RunTranscript, entries, errJSON []byte) error {
	if len(entries) > 0 {
		if err := json.Unmarshal(entries, &rec.Entries); err != nil {
			return fmt.Errorf("decode run transcript: %w", err)
		}
	}
	if len(errJSON) > 0 {
		var e schema.Error
		if err := json.Unmarshal(errJSON, &e); err != nil {
			return fmt.Errorf("decode run error: %w", err)
		}
		rec.Error = &e
	}
	return nil
}
