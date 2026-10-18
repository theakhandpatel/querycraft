package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The challenge and patch statements, written once for both SQL backends the
 * way metadata.go writes the account statements: ? placeholders, %s tables,
 * and the dialect supplying what the engines spell differently (see stmt,
 * stamp and encodeJSON). One text per operation, so the two backends cannot
 * drift apart in ordering, NULL handling or timestamp format.
 */

const (
	// SaveChallenge upserts: the artifact and the outcome that admitted it are
	// one row, so the evidence cannot drift from the challenge it justified.
	saveChallengeSQL = `INSERT INTO %s (id, owner_id, status, artifact, outcome, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		     owner_id = excluded.owner_id,
		     status   = excluded.status,
		     artifact = excluded.artifact,
		     outcome  = excluded.outcome`

	challengeSQL = `SELECT owner_id, status, artifact, outcome, created_at FROM %s WHERE id = ?`

	deleteChallengeSQL = `DELETE FROM %s WHERE id = ?`

	// SavePatch stores a proposal the moment it is proposed; approved_at stays
	// null until a learner approves it, and re-storing the text must not clear
	// an approval that already exists.
	savePatchSQL = `INSERT INTO %s (id, challenge_id, proposal, created_at)
		 VALUES (?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET challenge_id = excluded.challenge_id, proposal = excluded.proposal`

	patchSQL = `SELECT proposal FROM %s WHERE id = ?`

	// ApprovePatch records the first approval only: the WHERE clause leaves an
	// already-approved row untouched, so the record says who actually said yes.
	approvePatchSQL = `UPDATE %s SET approved_at = ?, approved_by = ? WHERE id = ? AND approved_at IS NULL`
)

// jsonArg marshals v and prepares it for this dialect's JSON column.
func (s *sqlStore) jsonArg(v any) (any, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.dialect.encodeJSON(raw)
}

// SaveChallenge implements schema.Depot.
func (s *sqlStore) SaveChallenge(ctx context.Context, rec schema.Record) error {
	if rec.Artifact.ID == "" {
		return errors.New("challenge id is required")
	}
	status := rec.Status
	if status == "" {
		status = schema.StatusActive
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	artifact, err := s.jsonArg(rec.Artifact)
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	outcome, err := s.jsonArg(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	return s.exec(ctx, s.stmt(saveChallengeSQL, "challenges"),
		rec.Artifact.ID, rec.OwnerID, status, artifact, outcome, s.dialect.stamp(created))
}

// Challenge implements schema.Depot.
func (s *sqlStore) Challenge(ctx context.Context, id string) (schema.Record, bool, error) {
	var (
		rec      schema.Record
		artifact []byte
		outcome  []byte
	)
	err := s.queryRow(ctx, s.stmt(challengeSQL, "challenges"), id).
		Scan(&rec.OwnerID, &rec.Status, &artifact, &outcome, timeColumn{&rec.CreatedAt})
	if s.dialect.noRows(err) {
		return schema.Record{}, false, nil
	}
	if err != nil {
		return schema.Record{}, false, err
	}
	if err := decodeRecord(&rec, artifact, outcome); err != nil {
		return schema.Record{}, false, err
	}
	return rec, true, nil
}

// ChallengesFor lists one owner's challenges in SQL, newest first, so the
// owner-scoped list route does not load every learner's challenges and discard
// them in the handler.
func (s *sqlStore) ChallengesFor(ctx context.Context, ownerID string) ([]schema.Record, error) {
	return s.challengesWhere(ctx, ownerID)
}

// challengesWhere runs the shared list query. A non-empty ownerID narrows the
// SQL to that owner; an empty one lists every challenge. The order matches
// challenges_created_key so the index serves it.
func (s *sqlStore) challengesWhere(ctx context.Context, ownerID string) ([]schema.Record, error) {
	query := `SELECT owner_id, status, artifact, outcome, created_at FROM %s`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.query(ctx, s.stmt(query, "challenges"), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []schema.Record{}
	for rows.Next() {
		var (
			rec      schema.Record
			artifact []byte
			outcome  []byte
		)
		if err := rows.Scan(&rec.OwnerID, &rec.Status, &artifact, &outcome, timeColumn{&rec.CreatedAt}); err != nil {
			return nil, err
		}
		if err := decodeRecord(&rec, artifact, outcome); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteChallenge implements schema.Depot. Learner attempts are the learner's
// data and are not touched here; only the generated definition goes.
func (s *sqlStore) DeleteChallenge(ctx context.Context, id string) error {
	return s.exec(ctx, s.stmt(deleteChallengeSQL, "challenges"), id)
}

// SavePatch implements schema.Depot.
func (s *sqlStore) SavePatch(ctx context.Context, proposal schema.PatchProposal) error {
	if proposal.ID == "" {
		return errors.New("patch id is required")
	}
	raw, err := s.jsonArg(proposal)
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	return s.exec(ctx, s.stmt(savePatchSQL, "patches"),
		proposal.ID, proposal.ChallengeID, raw, s.dialect.stamp(time.Now()))
}

// Patch implements schema.Depot.
func (s *sqlStore) Patch(ctx context.Context, id string) (schema.PatchProposal, bool, error) {
	var raw []byte
	err := s.queryRow(ctx, s.stmt(patchSQL, "patches"), id).Scan(&raw)
	if s.dialect.noRows(err) {
		return schema.PatchProposal{}, false, nil
	}
	if err != nil {
		return schema.PatchProposal{}, false, err
	}
	var proposal schema.PatchProposal
	if err := json.Unmarshal(raw, &proposal); err != nil {
		return schema.PatchProposal{}, false, fmt.Errorf("decode patch: %w", err)
	}
	return proposal, true, nil
}

// ApprovePatch implements schema.Depot.
func (s *sqlStore) ApprovePatch(ctx context.Context, id, approvedBy string) error {
	if approvedBy == "" {
		return errors.New("approver is required")
	}
	return s.exec(ctx, s.stmt(approvePatchSQL, "patches"),
		s.dialect.stamp(time.Now()), approvedBy, id)
}

// decodeRecord turns the two JSON columns back into the stored types. A row
// that cannot be decoded is a storage fault, not a missing challenge, so it is
// reported rather than skipped.
func decodeRecord(rec *schema.Record, artifact, outcome []byte) error {
	if err := json.Unmarshal(artifact, &rec.Artifact); err != nil {
		return fmt.Errorf("decode challenge artifact: %w", err)
	}
	if err := json.Unmarshal(outcome, &rec.Outcome); err != nil {
		return fmt.Errorf("decode challenge outcome: %w", err)
	}
	return nil
}
