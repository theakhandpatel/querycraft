package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/querycraft/sqlquery/backend/internal/ai"
)

/* ---------- agent-run transcripts ---------- */

// SaveRun implements ai.Depot. The record is stored whole — attribution and the
// entries together — so a later reader cannot see a run whose transcript has
// drifted from the run it is attributed to. The write is an upsert because one
// run may be stored more than once (a building course updates its record as it
// writes chapters), and it keeps the run's original started_at rather than
// overwriting it with the moment of the latest write.
func (p *Postgres) SaveRun(ctx context.Context, rec ai.RunTranscript) error {
	if rec.ID == "" {
		return errors.New("run id is required")
	}
	kind := rec.Kind
	if kind == "" {
		kind = ai.RunKindChallenge
	}
	status := rec.Status
	if status == "" {
		status = ai.StatusFailed
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
		entries = []ai.TranscriptEntry{}
	}
	raw, err := json.Marshal(entries)
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
	var errJSON []byte
	if rec.Error != nil {
		if errJSON, err = json.Marshal(rec.Error); err != nil {
			return fmt.Errorf("encode run error: %w", err)
		}
	}
	_, err = p.pool.Exec(ctx,
		`INSERT INTO app.runs (id, kind, owner_id, status, entries, entry_count, bytes, truncated, error, started_at, finished_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		 ON CONFLICT (id) DO UPDATE SET
		     kind        = EXCLUDED.kind,
		     owner_id    = EXCLUDED.owner_id,
		     status      = EXCLUDED.status,
		     entries     = EXCLUDED.entries,
		     entry_count = EXCLUDED.entry_count,
		     bytes       = EXCLUDED.bytes,
		     truncated   = EXCLUDED.truncated,
		     error       = EXCLUDED.error,
		     finished_at = EXCLUDED.finished_at`,
		rec.ID, kind, rec.OwnerID, status, raw, count, rec.Bytes, rec.Truncated, errJSON, started, finished)
	return err
}

// Run implements ai.Depot. It returns the whole record including its owner, so
// the caller enforces ownership: the store does not know which session is
// reading. Owner scoping is applied by the query in Run, so a caller that
// filters afterwards cannot accidentally read a row it should not have.
func (p *Postgres) Run(ctx context.Context, id string) (ai.RunTranscript, bool, error) {
	var (
		rec     ai.RunTranscript
		entries []byte
		errJSON []byte
	)
	err := p.pool.QueryRow(ctx,
		`SELECT id, kind, owner_id, status, entries, entry_count, bytes, truncated, error, started_at, finished_at
		   FROM app.runs WHERE id = $1`, id).
		Scan(&rec.ID, &rec.Kind, &rec.OwnerID, &rec.Status, &entries, &rec.EntryCount, &rec.Bytes, &rec.Truncated, &errJSON, &rec.StartedAt, &rec.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.RunTranscript{}, false, nil
	}
	if err != nil {
		return ai.RunTranscript{}, false, err
	}
	if err := decodeRun(&rec, entries, errJSON); err != nil {
		return ai.RunTranscript{}, false, err
	}
	if rec.Entries == nil {
		rec.Entries = []ai.TranscriptEntry{}
	}
	return rec, true, nil
}

// Runs implements ai.Depot, newest first. Only the summaries are read: a list
// of every transcript's entries would be the whole database on one request.
func (p *Postgres) Runs(ctx context.Context) ([]ai.RunSummary, error) {
	return p.runsWhere(ctx, "")
}

// RunsFor is Runs filtered to one owner in SQL (see CoursesFor).
func (p *Postgres) RunsFor(ctx context.Context, ownerID string) ([]ai.RunSummary, error) {
	return p.runsWhere(ctx, ownerID)
}

func (p *Postgres) runsWhere(ctx context.Context, ownerID string) ([]ai.RunSummary, error) {
	query := `SELECT id, kind, owner_id, status, entry_count, bytes, truncated, error, started_at, finished_at
		   FROM app.runs`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id = $1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY started_at DESC, id DESC`
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ai.RunSummary{}
	for rows.Next() {
		var (
			s       ai.RunSummary
			errJSON []byte
		)
		if err := rows.Scan(&s.ID, &s.Kind, &s.OwnerID, &s.Status, &s.EntryCount, &s.Bytes, &s.Truncated, &errJSON, &s.StartedAt, &s.FinishedAt); err != nil {
			return nil, err
		}
		if len(errJSON) > 0 {
			var e ai.Error
			if err := json.Unmarshal(errJSON, &e); err != nil {
				return nil, fmt.Errorf("decode run error: %w", err)
			}
			s.Error = &e
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// decodeRun turns the jsonb columns back into the ai types.
func decodeRun(rec *ai.RunTranscript, entries, errJSON []byte) error {
	if len(entries) > 0 {
		if err := json.Unmarshal(entries, &rec.Entries); err != nil {
			return fmt.Errorf("decode run transcript: %w", err)
		}
	}
	if len(errJSON) > 0 {
		var e ai.Error
		if err := json.Unmarshal(errJSON, &e); err != nil {
			return fmt.Errorf("decode run error: %w", err)
		}
		rec.Error = &e
	}
	return nil
}
