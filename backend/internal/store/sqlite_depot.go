package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai"
)

func (s *SQLite) SaveChallenge(ctx context.Context, rec ai.Record) error {
	if rec.Artifact.ID == "" {
		return errors.New("challenge id is required")
	}
	status, created := rec.Status, rec.CreatedAt
	if status == "" {
		status = ai.StatusActive
	}
	if created.IsZero() {
		created = time.Now().UTC()
	}
	artifact, err := jsonText(rec.Artifact)
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	outcome, err := jsonText(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	artifact, err = normalizeJSON(json.RawMessage(artifact))
	if err != nil {
		return fmt.Errorf("encode artifact: %w", err)
	}
	outcome, err = normalizeJSON(json.RawMessage(outcome))
	if err != nil {
		return fmt.Errorf("encode outcome: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO challenges(id,owner_id,status,artifact,outcome,created_at) VALUES(?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET owner_id=excluded.owner_id,status=excluded.status,artifact=excluded.artifact,outcome=excluded.outcome`, rec.Artifact.ID, rec.OwnerID, status, artifact, outcome, timestamp(created))
	return err
}

func (s *SQLite) Challenge(ctx context.Context, id string) (ai.Record, bool, error) {
	var rec ai.Record
	var artifact, outcome, created string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id,status,artifact,outcome,created_at FROM challenges WHERE id=?`, id).Scan(&rec.OwnerID, &rec.Status, &artifact, &outcome, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.Record{}, false, nil
	}
	if err != nil {
		return ai.Record{}, false, err
	}
	if err = decodeRecord(&rec, []byte(artifact), []byte(outcome)); err != nil {
		return ai.Record{}, false, err
	}
	rec.CreatedAt, err = parseTimestamp(created)
	return rec, err == nil, err
}

func (s *SQLite) Challenges(ctx context.Context) ([]ai.Record, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,status,artifact,outcome,created_at FROM challenges ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ai.Record{}
	for rows.Next() {
		var rec ai.Record
		var id, artifact, outcome, created string
		if err := rows.Scan(&id, &rec.OwnerID, &rec.Status, &artifact, &outcome, &created); err != nil {
			return nil, err
		}
		if err := decodeRecord(&rec, []byte(artifact), []byte(outcome)); err != nil {
			return nil, err
		}
		rec.CreatedAt, err = parseTimestamp(created)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteChallenge(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM challenges WHERE id=?`, id)
	return err
}

func (s *SQLite) SaveCourse(ctx context.Context, rec ai.CourseRecord) error {
	if rec.Plan.ID == "" {
		return errors.New("course id is required")
	}
	status, created := rec.Status, rec.CreatedAt
	if status == "" {
		status = ai.StatusActive
	}
	if created.IsZero() {
		created = time.Now().UTC()
	}
	plan, err := jsonText(rec.Plan)
	if err != nil {
		return fmt.Errorf("encode course plan: %w", err)
	}
	outcome, err := jsonText(rec.Outcome)
	if err != nil {
		return fmt.Errorf("encode course outcome: %w", err)
	}
	plan, err = normalizeJSON(json.RawMessage(plan))
	if err != nil {
		return fmt.Errorf("encode course plan: %w", err)
	}
	outcome, err = normalizeJSON(json.RawMessage(outcome))
	if err != nil {
		return fmt.Errorf("encode course outcome: %w", err)
	}
	var request any
	if rec.Request != nil {
		var requestJSON string
		requestJSON, err = jsonText(rec.Request)
		if err != nil {
			return fmt.Errorf("encode course request: %w", err)
		}
		requestJSON, err = normalizeJSON(json.RawMessage(requestJSON))
		if err != nil {
			return fmt.Errorf("encode course request: %w", err)
		}
		request = requestJSON
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO courses(id,owner_id,status,plan,outcome,request,created_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET owner_id=excluded.owner_id,status=excluded.status,plan=excluded.plan,outcome=excluded.outcome,request=excluded.request`, rec.Plan.ID, rec.OwnerID, status, plan, outcome, request, timestamp(created))
	return err
}

func (s *SQLite) Course(ctx context.Context, id string) (ai.CourseRecord, bool, error) {
	var rec ai.CourseRecord
	var plan, outcome string
	var request sql.NullString
	var created string
	err := s.db.QueryRowContext(ctx, `SELECT owner_id,status,plan,outcome,request,created_at FROM courses WHERE id=?`, id).Scan(&rec.OwnerID, &rec.Status, &plan, &outcome, &request, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.CourseRecord{}, false, nil
	}
	if err != nil {
		return ai.CourseRecord{}, false, err
	}
	var requestBytes []byte
	if request.Valid {
		requestBytes = []byte(request.String)
	}
	if err = decodeCourse(&rec, []byte(plan), []byte(outcome), requestBytes); err != nil {
		return ai.CourseRecord{}, false, err
	}
	rec.CreatedAt, err = parseTimestamp(created)
	return rec, err == nil, err
}

func (s *SQLite) Courses(ctx context.Context) ([]ai.CourseRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,owner_id,status,plan,outcome,request,created_at FROM courses ORDER BY created_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ai.CourseRecord{}
	for rows.Next() {
		var rec ai.CourseRecord
		var id, plan, outcome, created string
		var request sql.NullString
		if err := rows.Scan(&id, &rec.OwnerID, &rec.Status, &plan, &outcome, &request, &created); err != nil {
			return nil, err
		}
		var requestBytes []byte
		if request.Valid {
			requestBytes = []byte(request.String)
		}
		if err := decodeCourse(&rec, []byte(plan), []byte(outcome), requestBytes); err != nil {
			return nil, err
		}
		rec.CreatedAt, err = parseTimestamp(created)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *SQLite) DeleteCourse(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM courses WHERE id=?`, id)
	return err
}

func (s *SQLite) SavePatch(ctx context.Context, proposal ai.PatchProposal) error {
	if proposal.ID == "" {
		return errors.New("patch id is required")
	}
	raw, err := jsonText(proposal)
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	raw, err = normalizeJSON(json.RawMessage(raw))
	if err != nil {
		return fmt.Errorf("encode patch: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO patches(id,challenge_id,proposal,created_at) VALUES(?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET challenge_id=excluded.challenge_id,proposal=excluded.proposal`, proposal.ID, proposal.ChallengeID, raw, timestamp(time.Now()))
	return err
}

func (s *SQLite) Patch(ctx context.Context, id string) (ai.PatchProposal, bool, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `SELECT proposal FROM patches WHERE id=?`, id).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.PatchProposal{}, false, nil
	}
	if err != nil {
		return ai.PatchProposal{}, false, err
	}
	var proposal ai.PatchProposal
	if err := decodeJSON(raw, &proposal, "patch"); err != nil {
		return ai.PatchProposal{}, false, err
	}
	return proposal, true, nil
}

func (s *SQLite) ApprovePatch(ctx context.Context, id, approvedBy string) error {
	if approvedBy == "" {
		return errors.New("approver is required")
	}
	_, err := s.db.ExecContext(ctx, `UPDATE patches SET approved_at=?,approved_by=? WHERE id=? AND approved_at IS NULL`, timestamp(time.Now()), approvedBy, id)
	return err
}

func (s *SQLite) SaveRun(ctx context.Context, rec ai.RunTranscript) error {
	if rec.ID == "" {
		return errors.New("run id is required")
	}
	kind, status, started, finished := rec.Kind, rec.Status, rec.StartedAt, rec.FinishedAt
	if kind == "" {
		kind = ai.RunKindChallenge
	}
	if status == "" {
		status = ai.StatusFailed
	}
	if started.IsZero() {
		started = time.Now().UTC()
	}
	if finished.IsZero() {
		finished = started
	}
	entries := rec.Entries
	if entries == nil {
		entries = []ai.TranscriptEntry{}
	}
	raw, err := jsonText(entries)
	if err != nil {
		return fmt.Errorf("encode run transcript: %w", err)
	}
	raw, err = normalizeJSON(json.RawMessage(raw))
	if err != nil {
		return fmt.Errorf("encode run transcript: %w", err)
	}
	count := rec.EntryCount
	if count < len(entries) {
		count = len(entries)
	}
	var errJSON any
	if rec.Error != nil {
		var errorJSON string
		errorJSON, err = jsonText(rec.Error)
		if err != nil {
			return fmt.Errorf("encode run error: %w", err)
		}
		errorJSON, err = normalizeJSON(json.RawMessage(errorJSON))
		if err != nil {
			return fmt.Errorf("encode run error: %w", err)
		}
		errJSON = errorJSON
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO runs(id,kind,owner_id,status,entries,entry_count,bytes,truncated,error,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET kind=excluded.kind,owner_id=excluded.owner_id,status=excluded.status,entries=excluded.entries,entry_count=excluded.entry_count,bytes=excluded.bytes,truncated=excluded.truncated,error=excluded.error,finished_at=excluded.finished_at`, rec.ID, kind, rec.OwnerID, status, raw, count, rec.Bytes, rec.Truncated, errJSON, timestamp(started), timestamp(finished))
	return err
}

func (s *SQLite) Run(ctx context.Context, id string) (ai.RunTranscript, bool, error) {
	var rec ai.RunTranscript
	var entries, started, finished string
	var errJSON sql.NullString
	var truncated int
	err := s.db.QueryRowContext(ctx, `SELECT id,kind,owner_id,status,entries,entry_count,bytes,truncated,error,started_at,finished_at FROM runs WHERE id=?`, id).Scan(&rec.ID, &rec.Kind, &rec.OwnerID, &rec.Status, &entries, &rec.EntryCount, &rec.Bytes, &truncated, &errJSON, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return ai.RunTranscript{}, false, nil
	}
	if err != nil {
		return ai.RunTranscript{}, false, err
	}
	rec.Truncated = truncated != 0
	var errorBytes []byte
	if errJSON.Valid {
		errorBytes = []byte(errJSON.String)
	}
	if err = decodeRun(&rec, []byte(entries), errorBytes); err != nil {
		return ai.RunTranscript{}, false, err
	}
	if rec.Entries == nil {
		rec.Entries = []ai.TranscriptEntry{}
	}
	if rec.StartedAt, err = parseTimestamp(started); err != nil {
		return ai.RunTranscript{}, false, err
	}
	if rec.FinishedAt, err = parseTimestamp(finished); err != nil {
		return ai.RunTranscript{}, false, err
	}
	return rec, true, nil
}

func (s *SQLite) Runs(ctx context.Context) ([]ai.RunSummary, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,kind,owner_id,status,entry_count,bytes,truncated,error,started_at,finished_at FROM runs ORDER BY started_at DESC,id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ai.RunSummary{}
	for rows.Next() {
		var summary ai.RunSummary
		var truncated int
		var raw sql.NullString
		var started, finished string
		if err := rows.Scan(&summary.ID, &summary.Kind, &summary.OwnerID, &summary.Status, &summary.EntryCount, &summary.Bytes, &truncated, &raw, &started, &finished); err != nil {
			return nil, err
		}
		summary.Truncated = truncated != 0
		if raw.Valid {
			var e ai.Error
			if err := decodeJSON(raw.String, &e, "run error"); err != nil {
				return nil, err
			}
			summary.Error = &e
		}
		if summary.StartedAt, err = parseTimestamp(started); err != nil {
			return nil, err
		}
		if summary.FinishedAt, err = parseTimestamp(finished); err != nil {
			return nil, err
		}
		out = append(out, summary)
	}
	return out, rows.Err()
}

var _ ai.Depot = (*SQLite)(nil)
