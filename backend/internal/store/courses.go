package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

/* The course statements, written once for both SQL backends the way
 * metadata.go writes the account statements: ? placeholders, %s tables, and the
 * dialect supplying what the engines spell differently (see stmt, stamp and
 * encodeJSON). The plan and the outcome that admitted it are one row, so the
 * evidence cannot drift from the course it justified.
 */

const (
	saveCourseSQL = `INSERT INTO %s (id, owner_id, status, plan, outcome, request, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO UPDATE SET
		     owner_id = excluded.owner_id,
		     status   = excluded.status,
		     plan     = excluded.plan,
		     outcome  = excluded.outcome,
		     request  = excluded.request`

	courseSQL = `SELECT owner_id, status, plan, outcome, request, created_at FROM %s WHERE id = ?`

	deleteCourseSQL = `DELETE FROM %s WHERE id = ?`
)

// SaveCourse implements schema.Depot.
func (s *sqlStore) SaveCourse(ctx context.Context, rec schema.CourseRecord) error {
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
	plan, err := s.jsonArg(rec.Plan)
	if err != nil {
		return fmt.Errorf("encode course plan: %w", err)
	}
	outcome, err := s.jsonArg(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode course outcome: %w", err)
	}
	/* The request is nullable: a record with none (a row written before it was
	 * kept) stores NULL, and a read hands back a nil pointer, which the resume
	 * path reads as "recover what the plan knows". */
	var request any
	if rec.Request != nil {
		request, err = s.jsonArg(rec.Request)
		if err != nil {
			return fmt.Errorf("encode course request: %w", err)
		}
	}
	return s.exec(ctx, s.stmt(saveCourseSQL, "courses"),
		rec.Plan.ID, rec.OwnerID, status, plan, outcome, request, s.dialect.stamp(created))
}

// Course implements schema.Depot.
func (s *sqlStore) Course(ctx context.Context, id string) (schema.CourseRecord, bool, error) {
	var (
		rec     schema.CourseRecord
		plan    []byte
		outcome []byte
		request []byte
	)
	err := s.queryRow(ctx, s.stmt(courseSQL, "courses"), id).
		Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &request, timeColumn{&rec.CreatedAt})
	if s.dialect.noRows(err) {
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
func (s *sqlStore) Courses(ctx context.Context) ([]schema.CourseRecord, error) {
	return s.coursesWhere(ctx, "")
}

// CoursesFor is Courses filtered to one owner in SQL, so the owner-scoped list
// route does not load every learner's courses and discard them in the handler.
func (s *sqlStore) CoursesFor(ctx context.Context, ownerID string) ([]schema.CourseRecord, error) {
	return s.coursesWhere(ctx, ownerID)
}

// coursesWhere runs the shared list query. A non-empty ownerID narrows the SQL
// to that owner; an empty one lists every course. The order matches
// courses_created_key so the index serves it.
func (s *sqlStore) coursesWhere(ctx context.Context, ownerID string) ([]schema.CourseRecord, error) {
	query := `SELECT owner_id, status, plan, outcome, request, created_at FROM %s`
	args := []any{}
	if ownerID != "" {
		query += ` WHERE owner_id = ?`
		args = append(args, ownerID)
	}
	query += ` ORDER BY created_at DESC, id DESC`
	rows, err := s.query(ctx, s.stmt(query, "courses"), args...)
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
		if err := rows.Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &request, timeColumn{&rec.CreatedAt}); err != nil {
			return nil, err
		}
		if err := decodeCourse(&rec, plan, outcome, request); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

// DeleteCourse implements schema.Depot. Learner attempts are the learner's data
// and are not touched here; only the generated course definition goes.
func (s *sqlStore) DeleteCourse(ctx context.Context, id string) error {
	return s.exec(ctx, s.stmt(deleteCourseSQL, "courses"), id)
}

// decodeCourse turns the JSON columns back into the stored types. request is
// nullable: a NULL column leaves the record's Request nil rather than failing
// the read, and the resume path reads that as "recover what the plan knows".
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
