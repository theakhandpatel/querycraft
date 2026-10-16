package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* ---------- generated courses (M7) ---------- */

// SaveCourse implements schema.Depot. The plan and the outcome that admitted it are
// one row, so the evidence cannot drift from the course it justified — the same
// rule app.challenges follows, for the same reason.
func (p *Postgres) SaveCourse(ctx context.Context, rec schema.CourseRecord) error {
	if rec.Plan.ID == "" {
		return errors.New("course id is required")
	}
	status := rec.Status
	if status == "" {
		status = schema.StatusActive
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
	/* The request is nullable: a record with none (a row written before it was
	 * kept) stores NULL, and a read hands back a nil pointer, which the resume
	 * path reads as "recover what the plan knows". */
	var request []byte
	if rec.Request != nil {
		request, err = json.Marshal(rec.Request)
		if err != nil {
			return fmt.Errorf("encode course request: %w", err)
		}
	}
	_, err = p.pool.Exec(ctx,
		`INSERT INTO app.courses (id, owner_id, status, plan, outcome, request, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (id) DO UPDATE SET
		     owner_id = EXCLUDED.owner_id,
		     status   = EXCLUDED.status,
		     plan     = EXCLUDED.plan,
		     outcome  = EXCLUDED.outcome,
		     request  = EXCLUDED.request`,
		rec.Plan.ID, rec.OwnerID, status, plan, outcome, request, created)
	return err
}

// Course implements schema.Depot.
func (p *Postgres) Course(ctx context.Context, id string) (schema.CourseRecord, bool, error) {
	var (
		rec     schema.CourseRecord
		plan    []byte
		outcome []byte
		request []byte
	)
	err := p.pool.QueryRow(ctx,
		`SELECT owner_id, status, plan, outcome, request, created_at FROM app.courses WHERE id = $1`, id).
		Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &request, &rec.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return schema.CourseRecord{}, false, nil
	}
	if err != nil {
		return schema.CourseRecord{}, false, err
	}
	if err := decodeCourse(&rec, plan, outcome, request); err != nil {
		return schema.CourseRecord{}, false, err
	}
	return rec, true, nil
}

// Courses implements schema.Depot, newest first.
func (p *Postgres) Courses(ctx context.Context) ([]schema.CourseRecord, error) {
	return p.coursesWhere(ctx, "")
}

// CoursesFor is Courses filtered to one owner in SQL, so the owner-scoped list
// route does not load every learner's courses and discard them in the handler.
func (p *Postgres) CoursesFor(ctx context.Context, ownerID string) ([]schema.CourseRecord, error) {
	return p.coursesWhere(ctx, ownerID)
}

func (p *Postgres) coursesWhere(ctx context.Context, ownerID string) ([]schema.CourseRecord, error) {
	query := `SELECT owner_id, status, plan, outcome, request, created_at
		   FROM app.courses`
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

	out := []schema.CourseRecord{}
	for rows.Next() {
		var (
			rec     schema.CourseRecord
			plan    []byte
			outcome []byte
			request []byte
		)
		if err := rows.Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &request, &rec.CreatedAt); err != nil {
			return nil, err
		}
		if err := decodeCourse(&rec, plan, outcome, request); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteCourse implements schema.Depot. Learner attempts are the learner's data and
// are not touched here; only the generated course definition goes.
func (p *Postgres) DeleteCourse(ctx context.Context, id string) error {
	_, err := p.pool.Exec(ctx, `DELETE FROM app.courses WHERE id = $1`, id)
	return err
}

// decodeCourse turns the jsonb columns back into the ai types. request is
// nullable, so a NULL column leaves the record's Request nil rather than
// failing the read — a course stored before the request was kept is still a
// course.
func decodeCourse(rec *schema.CourseRecord, plan, outcome, request []byte) error {
	if err := json.Unmarshal(plan, &rec.Plan); err != nil {
		return fmt.Errorf("decode course plan: %w", err)
	}
	if err := json.Unmarshal(outcome, &rec.Outcome); err != nil {
		return fmt.Errorf("decode course outcome: %w", err)
	}
	if len(request) > 0 {
		var req schema.GenerationRequest
		if err := json.Unmarshal(request, &req); err != nil {
			return fmt.Errorf("decode course request: %w", err)
		}
		rec.Request = &req
	}
	return nil
}
