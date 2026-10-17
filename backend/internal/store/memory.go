package store

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// Memory is a concurrency-safe in-process Store. It is used when no metadata
// database is configured so the gateway still runs. Accounts and sessions live
// in the same process, which means a restart forgets who was signed in -- the
// honest behaviour for a store that has nowhere to persist them.
type Memory struct {
	mu         sync.RWMutex
	progress   map[string]json.RawMessage      // key: userID + "\x00" + course
	events     map[string]json.RawMessage      // key: userID + "\x00" + event id
	users      map[string]memoryUser           // key: account id
	sessions   map[string]memorySession        // key: token
	challenges map[string]schema.Record        // key: challenge id
	courses    map[string]schema.CourseRecord  // key: course id
	patches    map[string]memoryPatch          // key: patch id
	runs       map[string]schema.RunTranscript // key: run id
	settings   map[string]string               // key: setting key; value: raw JSON text
}

// memoryPatch is a stored proposal plus the approval that may follow it. An
// empty approvedBy means nobody has approved it yet, which is the only state a
// proposal starts in.
type memoryPatch struct {
	proposal   schema.PatchProposal
	approvedAt time.Time
	approvedBy string
}

type memoryUser struct {
	user User
	hash string
}

type memorySession struct {
	userID    string
	expiresAt time.Time
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{
		progress:   make(map[string]json.RawMessage),
		events:     make(map[string]json.RawMessage),
		users:      make(map[string]memoryUser),
		sessions:   make(map[string]memorySession),
		challenges: make(map[string]schema.Record),
		courses:    make(map[string]schema.CourseRecord),
		patches:    make(map[string]memoryPatch),
		runs:       make(map[string]schema.RunTranscript),
	}
}

/* ---------- generated challenges (M4) ---------- */

// SaveChallenge implements schema.Depot. The record is deep-copied so a caller
// cannot mutate what a later read returns.
func (m *Memory) SaveChallenge(_ context.Context, rec schema.Record) error {
	if rec.Artifact.ID == "" {
		return errors.New("challenge id is required")
	}
	if rec.Status == "" {
		rec.Status = schema.StatusActive
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.challenges[rec.Artifact.ID] = cloneRecord(rec)
	return nil
}

// Challenge implements schema.Depot.
func (m *Memory) Challenge(_ context.Context, id string) (schema.Record, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.challenges[id]
	if !ok {
		return schema.Record{}, false, nil
	}
	return cloneRecord(rec), true, nil
}

// Challenges implements schema.Depot, newest first, matching Postgres.
func (m *Memory) Challenges(_ context.Context) ([]schema.Record, error) {
	return m.challengesFor("")
}

// ChallengesFor filters to one owner, matching the Postgres store.
func (m *Memory) ChallengesFor(_ context.Context, ownerID string) ([]schema.Record, error) {
	return m.challengesFor(ownerID)
}

func (m *Memory) challengesFor(ownerID string) ([]schema.Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]schema.Record, 0, len(m.challenges))
	for _, rec := range m.challenges {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, cloneRecord(rec))
	}
	slices.SortFunc(out, func(a, b schema.Record) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.Artifact.ID, a.Artifact.ID))
	})
	return out, nil
}

// DeleteChallenge implements schema.Depot.
func (m *Memory) DeleteChallenge(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.challenges, id)
	return nil
}

/* ---------- generated courses (M7) ---------- */

// SaveCourse implements schema.Depot. The record is deep-copied so a caller cannot
// mutate what a later read returns.
func (m *Memory) SaveCourse(_ context.Context, rec schema.CourseRecord) error {
	if rec.Plan.ID == "" {
		return errors.New("course id is required")
	}
	if rec.Status == "" {
		rec.Status = schema.StatusActive
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.courses[rec.Plan.ID] = cloneCourse(rec)
	return nil
}

// Course implements schema.Depot.
func (m *Memory) Course(_ context.Context, id string) (schema.CourseRecord, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.courses[id]
	if !ok {
		return schema.CourseRecord{}, false, nil
	}
	return cloneCourse(rec), true, nil
}

// Courses implements schema.Depot, newest first, matching Postgres.
func (m *Memory) Courses(_ context.Context) ([]schema.CourseRecord, error) {
	return m.coursesFor("")
}

// CoursesFor filters to one owner, matching CoursesFor on the Postgres store.
func (m *Memory) CoursesFor(_ context.Context, ownerID string) ([]schema.CourseRecord, error) {
	return m.coursesFor(ownerID)
}

func (m *Memory) coursesFor(ownerID string) ([]schema.CourseRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]schema.CourseRecord, 0, len(m.courses))
	for _, rec := range m.courses {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, cloneCourse(rec))
	}
	slices.SortFunc(out, func(a, b schema.CourseRecord) int {
		return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), cmp.Compare(b.Plan.ID, a.Plan.ID))
	})
	return out, nil
}

// DeleteCourse implements schema.Depot.
func (m *Memory) DeleteCourse(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.courses, id)
	return nil
}

// cloneCourse copies every slice a caller could otherwise share with the store:
// the plan's chapters, their sections and exercises, each exercise's hints,
// objectives, contract columns, checks and expected rows, plus the reported
// per-chapter results. Rows are copied row-by-row because a [][]any sharing one
// inner slice would still alias.
func cloneCourse(rec schema.CourseRecord) schema.CourseRecord {
	out := rec
	/* The request is a pointer, so a shallow copy would let a caller mutate the
	   stored request through the copy it was handed. */
	if rec.Request != nil {
		req := *rec.Request
		req.Topics = append([]string(nil), rec.Request.Topics...)
		req.Constraints = append([]string(nil), rec.Request.Constraints...)
		req.Steps = append([]string(nil), rec.Request.Steps...)
		out.Request = &req
	}
	out.Plan.Objectives = append([]string(nil), rec.Plan.Objectives...)
	out.Plan.Setup.Statements = append([]string(nil), rec.Plan.Setup.Statements...)
	out.Plan.Chapters = make([]schema.CourseChapter, len(rec.Plan.Chapters))
	for i, ch := range rec.Plan.Chapters {
		cch := ch
		cch.Objectives = append([]string(nil), ch.Objectives...)
		cch.Sections = append([]schema.CourseSection(nil), ch.Sections...)
		cch.Exercises = make([]schema.CourseExercise, len(ch.Exercises))
		for j, ex := range ch.Exercises {
			cex := ex
			cex.Objectives = append([]string(nil), ex.Objectives...)
			cex.Hints = append([]string(nil), ex.Hints...)
			cex.Checks = append([]schema.Check(nil), ex.Checks...)
			cex.ResultContract.Columns = append([]string(nil), ex.ResultContract.Columns...)
			if ex.Expected != nil {
				exp := schema.ExpectedResult{Columns: append([]string(nil), ex.Expected.Columns...)}
				exp.Rows = make([][]any, len(ex.Expected.Rows))
				for k, row := range ex.Expected.Rows {
					exp.Rows[k] = append([]any(nil), row...)
				}
				cex.Expected = &exp
			}
			cch.Exercises[j] = cex
		}
		out.Plan.Chapters[i] = cch
	}
	out.Outcome.Chapters = make([]schema.ChapterResult, len(rec.Outcome.Chapters))
	for i, cr := range rec.Outcome.Chapters {
		ccr := cr
		ccr.Exercises = append([]schema.ExerciseResult(nil), cr.Exercises...)
		out.Outcome.Chapters[i] = ccr
	}
	out.Outcome.Stages = append([]schema.Stage(nil), rec.Outcome.Stages...)
	out.Outcome.Attempts = append([]schema.Attempt(nil), rec.Outcome.Attempts...)
	if rec.Outcome.Plan != nil {
		plan := cloneCourse(schema.CourseRecord{Plan: *rec.Outcome.Plan}).Plan
		out.Outcome.Plan = &plan
	}
	if rec.Outcome.Artifact != nil {
		art := cloneRecord(schema.Record{Artifact: *rec.Outcome.Artifact}).Artifact
		out.Outcome.Artifact = &art
	}
	return out
}

// SavePatch implements schema.Depot. Re-proposing a patch keeps any approval that
// already exists: an approval is a statement about an id, and re-storing the
// text must not silently clear it.
func (m *Memory) SavePatch(_ context.Context, proposal schema.PatchProposal) error {
	if proposal.ID == "" {
		return errors.New("patch id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing := m.patches[proposal.ID]
	m.patches[proposal.ID] = memoryPatch{
		proposal:   proposal,
		approvedAt: existing.approvedAt,
		approvedBy: existing.approvedBy,
	}
	return nil
}

// Patch implements schema.Depot.
func (m *Memory) Patch(_ context.Context, id string) (schema.PatchProposal, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	p, ok := m.patches[id]
	if !ok {
		return schema.PatchProposal{}, false, nil
	}
	return p.proposal, true, nil
}

// ApprovePatch implements schema.Depot. The first approver is kept.
func (m *Memory) ApprovePatch(_ context.Context, id, approvedBy string) error {
	if approvedBy == "" {
		return errors.New("approver is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.patches[id]
	if !ok {
		return errors.New("unknown patch: " + id)
	}
	if !p.approvedAt.IsZero() {
		return nil
	}
	p.approvedAt = time.Now().UTC()
	p.approvedBy = approvedBy
	m.patches[id] = p
	return nil
}

/* ---------- agent-run transcripts ---------- */

// SaveRun implements schema.Depot. The record is deep-copied so a caller cannot
// mutate what a later read returns, and the run's identity is kept: re-storing
// a run updates it rather than creating a second row, matching Postgres.
func (m *Memory) SaveRun(_ context.Context, rec schema.RunTranscript) error {
	if rec.ID == "" {
		return errors.New("run id is required")
	}
	if rec.Kind == "" {
		rec.Kind = schema.RunKindChallenge
	}
	if rec.Status == "" {
		rec.Status = schema.StatusFailed
	}
	if rec.StartedAt.IsZero() {
		rec.StartedAt = time.Now().UTC()
	}
	if rec.FinishedAt.IsZero() {
		rec.FinishedAt = rec.StartedAt
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.runs[rec.ID]
	if ok && !existing.StartedAt.IsZero() {
		// The first write is when the run started; a later write is when it
		// finished. Keeping the original start matches Postgres, which does not
		// update started_at on conflict.
		rec.StartedAt = existing.StartedAt
	}
	m.runs[rec.ID] = cloneRun(rec)
	return nil
}

// Run implements schema.Depot.
func (m *Memory) Run(_ context.Context, id string) (schema.RunTranscript, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	rec, ok := m.runs[id]
	if !ok {
		return schema.RunTranscript{}, false, nil
	}
	return cloneRun(rec), true, nil
}

// Runs implements schema.Depot, newest first, matching Postgres.
func (m *Memory) Runs(_ context.Context) ([]schema.RunSummary, error) {
	return m.runsFor("")
}

// RunsFor filters to one owner, matching the Postgres store.
func (m *Memory) RunsFor(_ context.Context, ownerID string) ([]schema.RunSummary, error) {
	return m.runsFor(ownerID)
}

func (m *Memory) runsFor(ownerID string) ([]schema.RunSummary, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]schema.RunSummary, 0, len(m.runs))
	for _, rec := range m.runs {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, rec.Summary())
	}
	slices.SortFunc(out, func(a, b schema.RunSummary) int {
		return cmp.Or(b.StartedAt.Compare(a.StartedAt), cmp.Compare(b.ID, a.ID))
	})
	return out, nil
}

// cloneRun deep-copies the entries a caller could otherwise share with the
// store.
func cloneRun(rec schema.RunTranscript) schema.RunTranscript {
	out := rec
	out.Entries = append([]schema.TranscriptEntry(nil), rec.Entries...)
	return out
}

// cloneRecord deep-copies the slices a caller could otherwise share with the
// store: the artifact's contracts, checks, hints and the outcome's stages.
func cloneRecord(rec schema.Record) schema.Record {
	out := rec
	out.Artifact.Objectives = append([]string(nil), rec.Artifact.Objectives...)
	out.Artifact.Hints = append([]string(nil), rec.Artifact.Hints...)
	out.Artifact.Setup.Statements = append([]string(nil), rec.Artifact.Setup.Statements...)
	out.Artifact.ResultContract.Columns = append([]string(nil), rec.Artifact.ResultContract.Columns...)
	out.Artifact.Checks = append([]schema.Check(nil), rec.Artifact.Checks...)
	out.Outcome.Stages = append([]schema.Stage(nil), rec.Outcome.Stages...)
	out.Outcome.Attempts = append([]schema.Attempt(nil), rec.Outcome.Attempts...)
	if rec.Outcome.Artifact != nil {
		art := cloneRecord(schema.Record{Artifact: *rec.Outcome.Artifact}).Artifact
		out.Outcome.Artifact = &art
	}
	return out
}

func scopeKey(userID, key string) string { return userID + "\x00" + key }

// SaveProgress implements Store.
func (m *Memory) SaveProgress(_ context.Context, userID, course string, snapshot json.RawMessage) error {
	if userID == "" {
		return errors.New("user is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.progress[scopeKey(userID, course)] = bytes.Clone(snapshot)
	return nil
}

// LoadProgress implements Store.
func (m *Memory) LoadProgress(_ context.Context, userID, course string) (json.RawMessage, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	snap, ok := m.progress[scopeKey(userID, course)]
	if !ok {
		return nil, false, nil
	}
	return bytes.Clone(snap), true, nil
}

// SaveEvent implements Store.
func (m *Memory) SaveEvent(_ context.Context, userID, id string, payload json.RawMessage) error {
	// Match Postgres: an event without an owner or an id is not storable, so the
	// degraded store reports the same failure instead of silently accepting it.
	if userID == "" {
		return errors.New("user is required")
	}
	if id == "" {
		return errors.New("event id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[scopeKey(userID, id)] = bytes.Clone(payload)
	return nil
}

// CreateUser implements Store.
func (m *Memory) CreateUser(_ context.Context, u User, passwordHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.user.Email, u.Email) {
			return ErrEmailTaken
		}
	}
	if u.Role == "" {
		u.Role = RoleLearner
	}
	if u.CreatedAt.IsZero() {
		u.CreatedAt = time.Now()
	}
	m.users[u.ID] = memoryUser{user: u, hash: passwordHash}
	return nil
}

// UserByEmail implements Store.
func (m *Memory) UserByEmail(_ context.Context, email string) (User, string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, existing := range m.users {
		if strings.EqualFold(existing.user.Email, email) {
			return existing.user, existing.hash, true, nil
		}
	}
	return User{}, "", false, nil
}

// UserByID implements Store.
func (m *Memory) UserByID(_ context.Context, id string) (User, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	u, ok := m.users[id]
	if !ok {
		return User{}, false, nil
	}
	return u.user, true, nil
}

// CountUsers implements Store.
func (m *Memory) CountUsers(_ context.Context) (int, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.users), nil
}

// UserIDs implements Store: every account id, for the environment reaper.
// Setting implements Store.
func (m *Memory) Setting(_ context.Context, key string) (string, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.settings == nil {
		return "", false, nil
	}
	v, ok := m.settings[key]
	return v, ok, nil
}

// SaveSetting implements Store.
func (m *Memory) SaveSetting(_ context.Context, key, value string) error {
	if key == "" {
		return errors.New("setting key is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.settings == nil {
		m.settings = make(map[string]string)
	}
	m.settings[key] = value
	return nil
}

func (m *Memory) UserIDs(_ context.Context) ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ids := make([]string, 0, len(m.users))
	for id := range m.users {
		ids = append(ids, id)
	}
	return ids, nil
}

// SetPasswordHash implements Store.
func (m *Memory) SetPasswordHash(_ context.Context, userID, hash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[userID]
	if !ok {
		return errors.New("unknown user")
	}
	u.hash = hash
	m.users[userID] = u
	return nil
}

// CreateSession implements Store.
func (m *Memory) CreateSession(_ context.Context, token, userID string, expiresAt time.Time) error {
	if userID == "" {
		return errors.New("user is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[token] = memorySession{userID: userID, expiresAt: expiresAt}
	return nil
}

// SessionUser implements Store.
func (m *Memory) SessionUser(_ context.Context, token string) (User, bool, error) {
	m.mu.RLock()
	sess, ok := m.sessions[token]
	u, uok := m.users[sess.userID]
	m.mu.RUnlock()
	if !ok || !uok || time.Now().After(sess.expiresAt) {
		return User{}, false, nil
	}
	return u.user, true, nil
}

// DeleteSession implements Store.
func (m *Memory) DeleteSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

// Kind implements Store.
func (m *Memory) Kind() string { return "memory" }

// Close implements Store.
func (m *Memory) Close() error { return nil }
