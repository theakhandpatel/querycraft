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

/* ---------- generated courses (M7) ---------- */

// SaveCourse implements ai.Depot. The plan and the outcome that admitted it are
// one row, so the evidence cannot drift from the course it justified — the same
// rule app.challenges follows, for the same reason.
func (p *Postgres) SaveCourse(ctx context.Context, rec ai.CourseRecord) error {
	if rec.Plan.ID == "" {
		return errors.New("course id is required")
	}
	status := rec.Status
	if status == "" {
		status = ai.StatusActive
	}
	created := rec.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	plan, err := json.Marshal(rec.Plan)
	if err != nil {
		return fmt.Errorf("encode course plan: %w", err)
	}
	outcome, err := json.Marshal(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode course outcome: %w", err)
	}
	_, err = p.pool.Exec(ctx,
		`INSERT INTO app.courses (id, owner_id, status, plan, outcome, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (id) DO UPDATE SET
		     owner_id = EXCLUDED.owner_id,
		     status   = EXCLUDED.status,
		     plan     = EXCLUDED.plan,
		     outcome  = EXCLUDED.outcome`,
		rec.Plan.ID, rec.OwnerID, status, plan, outcome, created)
	return err
}

// Course implements ai.Depot.
func (p *Postgres) Course(ctx context.Context, id string) (ai.CourseRecord, bool, error) {
	var (
		rec     ai.CourseRecord
		plan    []byte
		outcome []byte
	)
	err := p.pool.QueryRow(ctx,
		`SELECT owner_id, status, plan, outcome, created_at FROM app.courses WHERE id = $1`, id).
		Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ai.CourseRecord{}, false, nil
	}
	if err != nil {
		return ai.CourseRecord{}, false, err
	}
	if err := decodeCourse(&rec, plan, outcome); err != nil {
		return ai.CourseRecord{}, false, err
	}
	return rec, true, nil
}

// Courses implements ai.Depot, newest first.
func (p *Postgres) Courses(ctx context.Context) ([]ai.CourseRecord, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT owner_id, status, plan, outcome, created_at
		   FROM app.courses ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ai.CourseRecord{}
	for rows.Next() {
		var (
			rec     ai.CourseRecord
			plan    []byte
			outcome []byte
		)
		if err := rows.Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if err := decodeCourse(&rec, plan, outcome); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteCourse implements ai.Depot. Learner attempts are the learner's data and
// are not touched here; only the generated course definition goes.
func (p *Postgres) DeleteCourse(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM app.courses WHERE id = $1`, id)
	return err
}

// decodeCourse turns the two jsonb columns back into the ai types.
func decodeCourse(rec *ai.CourseRecord, plan, outcome []byte) error {
	if err := json.Unmarshal(plan, &rec.Plan); err != nil {
		return fmt.Errorf("decode course plan: %w", err)
	}
	if err := json.Unmarshal(outcome, &rec.Outcome); err != nil {
		return fmt.Errorf("decode course outcome: %w", err)
	}
	return nil
}
