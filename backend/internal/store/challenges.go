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

/* ---------- generated challenges (M4) ---------- */

// SaveChallenge implements ai.Depot. The artifact and its outcome are stored as
// one row: the evidence that admitted a challenge is not a separate lifecycle
// that can drift from it.
func (p *Postgres) SaveChallenge(ctx context.Context, rec ai.Record) error {
	if rec.Artifact.ID == "" {
		return errors.New("challenge id is required")
	}
	status := rec.Status
	if status == "" {
		status = ai.StatusActive
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	artifact, err := json.Marshal(rec.Artifact)
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	outcome, err := json.Marshal(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	_, err = p.pool.Exec(ctx,
		`INSERT INTO app.challenges (id, owner_id, status, artifact, outcome, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE SET
		     owner_id = EXCLUDED.owner_id,
		     status   = EXCLUDED.status,
		     artifact = EXCLUDED.artifact,
		     outcome  = EXCLUDED.outcome`,
		rec.Artifact.ID, rec.OwnerID, status, artifact, outcome, created)
	return err
}

// Challenge implements ai.Depot.
func (p *Postgres) Challenge(ctx context.Context, id string) (ai.Record, bool, error) {
	var (
		rec      ai.Record
		artifact []byte
		outcome  []byte
	)
	err := p.pool.QueryRow(ctx,
		`SELECT owner_id, status, artifact, outcome, created_at FROM app.challenges WHERE id = $1`, id).
		Scan(&rec.OwnerID, &rec.Status, &artifact, &outcome, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.Record{}, false, nil
	}
	if err != nil {
		return ai.Record{}, false, err
	}
	if err := decodeRecord(&rec, artifact, outcome); err != nil {
		return ai.Record{}, false, err
	}
	return rec, true, nil
}

// Challenges implements ai.Depot. Newest first, because the lab lists what was
// just generated at the top.
func (p *Postgres) Challenges(ctx context.Context) ([]ai.Record, error) {
	return p.challengesWhere(ctx, "")
}

// ChallengesFor is Challenges filtered to one owner in SQL (see CoursesFor).
func (p *Postgres) ChallengesFor(ctx context.Context, ownerID string) ([]ai.Record, error) {
	return p.challengesWhere(ctx, ownerID)
}

func (p *Postgres) challengesWhere(ctx context.Context, ownerID string) ([]ai.Record, error) {
	query := `SELECT owner_id, status, artifact, outcome, created_at
		   FROM app.challenges`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id = $1`
		args = append(args, ownerID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := p.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ai.Record{}
	for rows.Next() {
		var (
			rec      ai.Record
			artifact []byte
			outcome  []byte
		)
		if err := rows.Scan(&rec.OwnerID, &rec.Status, &artifact, &outcome, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if err := decodeRecord(&rec, artifact, outcome); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteChallenge implements ai.Depot. Learner attempts are the learner's data
// and are not touched here; only the generated definition goes.
func (p *Postgres) DeleteChallenge(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM app.challenges WHERE id = $1`, id)
	return err
}

// SavePatch implements ai.Depot. A patch is stored the moment it is proposed,
// but `approved_at` stays null until a learner approves it, so "proposed" and
// "applied" cannot be confused by a later reader.
func (p *Postgres) SavePatch(ctx context.Context, proposal ai.PatchProposal) error {
	if proposal.ID == "" {
		return errors.New("patch id is required")
	}
	raw, err := json.Marshal(proposal)
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	_, err = p.pool.Exec(ctx,
		`INSERT INTO app.patches (id, challenge_id, proposal) VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET challenge_id = EXCLUDED.challenge_id, proposal = EXCLUDED.proposal`,
		proposal.ID, proposal.ChallengeID, raw)
	return err
}

// Patch implements ai.Depot.
func (p *Postgres) Patch(ctx context.Context, id string) (ai.PatchProposal, bool, error) {
	var (
		raw      []byte
		approved *time.Time
	)
	err := p.pool.QueryRow(ctx, `SELECT proposal, approved_at FROM app.patches WHERE id = $1`, id).
		Scan(&raw, &approved)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.PatchProposal{}, false, nil
	}
	if err != nil {
		return ai.PatchProposal{}, false, err
	}
	var proposal ai.PatchProposal
	if err := json.Unmarshal(raw, &proposal); err != nil {
		return ai.PatchProposal{}, false, fmt.Errorf("decode patch: %w", err)
	}
	return proposal, true, nil
}

// ApprovePatch implements ai.Depot. Approval is recorded once; approving an
// already-approved patch keeps the first approver, so the record says who
// actually said yes.
func (p *Postgres) ApprovePatch(ctx context.Context, id, approvedBy string) error {
	if approvedBy == "" {
		return errors.New("approver is required")
	}
	_, err := p.pool.Exec(ctx,
		`UPDATE app.patches SET approved_at = now(), approved_by = $2
		  WHERE id = $1 AND approved_at IS NULL`, id, approvedBy)
	return err
}

// decodeRecord turns the two jsonb columns back into the ai types. A row that
// cannot be decoded is a storage fault, not a missing challenge, so it is
// reported rather than skipped.
func decodeRecord(rec *ai.Record, artifact, outcome []byte) error {
	if err := json.Unmarshal(artifact, &rec.Artifact); err != nil {
		return fmt.Errorf("decode challenge artifact: %w", err)
	}
	if err := json.Unmarshal(outcome, &rec.Outcome); err != nil {
		return fmt.Errorf("decode challenge outcome: %w", err)
	}
	return nil
}
