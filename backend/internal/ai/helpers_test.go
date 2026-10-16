package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/ai/provider"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/schema"
)

// Test seams.
//
// The database seam is real: every test boots the embedded PostgreSQL the
// gateway ships (pgtest) and runs the pipeline over an executor connected to
// it. There is no fake Executor anywhere in this package's tests, because the
// thing under test is a policy that decides what real SQL may run.
//
// The Depot is in-process. That is not a fake database; it is the persistence
// port, and importing internal/store here would be an import cycle, because
// store implements this package's Depot.

// bootPG starts a cluster and returns a writable executor. The pipeline applies
// setup statements and the evaluator runs learner SQL, so the executor must be
// writable; whether the SQL is real PostgreSQL does not depend on the role.
func bootPG(t *testing.T) engine.Executor {
	t.Helper()
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	ex, err := engine.NewPgxExecutorWithConfig(ctx, srv.DSN, engine.PgxConfig{MaxConns: 2, MinConns: 1, Writable: true})
	if err != nil {
		t.Fatalf("open the test executor: %v", err)
	}
	t.Cleanup(func() { _ = ex.Close() })
	return ex
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// testDepot is an in-process Depot.
type testDepot struct {
	mu         sync.Mutex
	challenges map[string]schema.Record
	courses    map[string]schema.CourseRecord
	patches    map[string]schema.PatchProposal
	approved   map[string]string
	runs       map[string]schema.RunTranscript
	saves      int
}

func newTestDepot() *testDepot {
	return &testDepot{
		challenges: map[string]schema.Record{},
		courses:    map[string]schema.CourseRecord{},
		patches:    map[string]schema.PatchProposal{},
		approved:   map[string]string{},
		runs:       map[string]schema.RunTranscript{},
	}
}

func (d *testDepot) SaveRun(_ context.Context, rec schema.RunTranscript) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.runs[rec.ID] = rec
	return nil
}

func (d *testDepot) Run(_ context.Context, id string) (schema.RunTranscript, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.runs[id]
	return rec, ok, nil
}

func (d *testDepot) Runs(_ context.Context) ([]schema.RunSummary, error) {
	return d.runsFor("")
}

func (d *testDepot) RunsFor(_ context.Context, ownerID string) ([]schema.RunSummary, error) {
	return d.runsFor(ownerID)
}

func (d *testDepot) runsFor(ownerID string) ([]schema.RunSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]schema.RunSummary, 0, len(d.runs))
	for _, rec := range d.runs {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, rec.Summary())
	}
	return out, nil
}

// runCount is how many transcripts the depot holds.
func (d *testDepot) runCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.runs)
}

// runByID returns the stored transcript for a run id.
func (d *testDepot) runByID(id string) (schema.RunTranscript, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.runs[id]
	return rec, ok
}

func (d *testDepot) SaveChallenge(_ context.Context, rec schema.Record) error {
	if rec.Artifact.ID == "" {
		return fmt.Errorf("challenge id is required")
	}
	if rec.Status == "" {
		rec.Status = schema.StatusActive
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.saves++
	d.challenges[rec.Artifact.ID] = rec
	return nil
}

func (d *testDepot) Challenge(_ context.Context, id string) (schema.Record, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.challenges[id]
	return rec, ok, nil
}

func (d *testDepot) Challenges(_ context.Context) ([]schema.Record, error) {
	return d.challengesFor("")
}

func (d *testDepot) ChallengesFor(_ context.Context, ownerID string) ([]schema.Record, error) {
	return d.challengesFor(ownerID)
}

func (d *testDepot) challengesFor(ownerID string) ([]schema.Record, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]schema.Record, 0, len(d.challenges))
	for _, rec := range d.challenges {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *testDepot) DeleteChallenge(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.challenges[id]; !ok {
		return fmt.Errorf("no such challenge: %s", id)
	}
	delete(d.challenges, id)
	return nil
}

func (d *testDepot) SaveCourse(_ context.Context, rec schema.CourseRecord) error {
	if rec.Plan.ID == "" {
		return fmt.Errorf("course id is required")
	}
	if rec.Status == "" {
		rec.Status = schema.StatusActive
	}
	if rec.CreatedAt.IsZero() {
		rec.CreatedAt = time.Now().UTC()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.courses[rec.Plan.ID] = rec
	return nil
}

func (d *testDepot) Course(_ context.Context, id string) (schema.CourseRecord, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	rec, ok := d.courses[id]
	return rec, ok, nil
}

func (d *testDepot) Courses(_ context.Context) ([]schema.CourseRecord, error) {
	return d.coursesFor("")
}

func (d *testDepot) CoursesFor(_ context.Context, ownerID string) ([]schema.CourseRecord, error) {
	return d.coursesFor(ownerID)
}

func (d *testDepot) coursesFor(ownerID string) ([]schema.CourseRecord, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]schema.CourseRecord, 0, len(d.courses))
	for _, rec := range d.courses {
		if ownerID != "" && rec.OwnerID != ownerID {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

func (d *testDepot) DeleteCourse(_ context.Context, id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.courses[id]; !ok {
		return fmt.Errorf("no such course: %s", id)
	}
	delete(d.courses, id)
	return nil
}

func (d *testDepot) SavePatch(_ context.Context, p schema.PatchProposal) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.patches[p.ID] = p
	return nil
}

func (d *testDepot) Patch(_ context.Context, id string) (schema.PatchProposal, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	p, ok := d.patches[id]
	return p, ok, nil
}

func (d *testDepot) ApprovePatch(_ context.Context, id, approvedBy string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.approved[id] = approvedBy
	return nil
}

func (d *testDepot) activeCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := 0
	for _, rec := range d.challenges {
		if rec.Status == schema.StatusActive {
			n++
		}
	}
	return n
}

func (d *testDepot) courseCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.courses)
}

func (d *testDepot) patchCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.patches)
}

// mutateProvider rewrites each JSON reply, so a test can inject one kind of
// broken bundle without writing a second generator. Plain-text kinds (tutor,
// critique) pass through untouched.
type mutateProvider struct {
	base   provider.Provider
	mutate func(*schema.Artifact)
}

func (m mutateProvider) Descriptor() provider.ProviderDescriptor { return m.base.Descriptor() }

func (m mutateProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	resp, err := m.base.Complete(ctx, req)
	if err != nil {
		return resp, err
	}
	if req.Kind == "tutor" || req.Kind == "critique" {
		return resp, nil
	}
	var art schema.Artifact
	if uerr := json.Unmarshal([]byte(resp.Text), &art); uerr != nil {
		return resp, fmt.Errorf("mutate provider: %w", uerr)
	}
	m.mutate(&art)
	raw, merr := json.Marshal(art)
	if merr != nil {
		return resp, fmt.Errorf("mutate provider: %w", merr)
	}
	resp.Text = string(raw)
	return resp, nil
}

// countingProvider counts completions, so a repair-budget test can assert the
// loop stopped where it claims to have stopped.
type countingProvider struct {
	base  provider.Provider
	calls *int32
}

func (c countingProvider) Descriptor() provider.ProviderDescriptor { return c.base.Descriptor() }

func (c countingProvider) Complete(ctx context.Context, req provider.ProviderRequest) (provider.ProviderResponse, error) {
	atomic.AddInt32(c.calls, 1)
	return c.base.Complete(ctx, req)
}

// fixedProvider returns one text for every request. It is how a hostile or
// broken provider is expressed in a test.
type fixedProvider struct {
	text string
}

func (f fixedProvider) Descriptor() provider.ProviderDescriptor {
	return provider.ProviderDescriptor{Name: "fixed", Model: "fixed-v1", Version: "1", Configured: true}
}

func (f fixedProvider) Complete(context.Context, provider.ProviderRequest) (provider.ProviderResponse, error) {
	return provider.ProviderResponse{Text: f.text, Provider: "fixed", Model: "fixed-v1"}, nil
}

// failingRunner is a Scratch implementation that cannot produce an environment,
// so tests can exercise the environment-down path without stopping the cluster.
type failingRunner struct{}

func (failingRunner) Scratch(context.Context, string) (engine.Executor, func(), error) {
	return nil, nil, engine.ErrEnvironmentUnavailable
}

// service wires a Service for a test. Runners are always ProvidedRunner, which
// is the documented test seam.
func testService(p provider.Provider, d schema.Depot, ex engine.Executor) *Service {
	return NewService(Config{
		Provider:       p,
		Depot:          d,
		Runner:         ProvidedRunner{Exec: ex},
		DatasetVersion: 1,
	})
}

// activeID fails the test unless exactly one active challenge exists.
func activeID(t *testing.T, d *testDepot) string {
	t.Helper()
	if n := d.activeCount(); n != 1 {
		t.Fatalf("active challenges = %d, want 1", n)
	}
	for id, rec := range d.challenges {
		if rec.Status == schema.StatusActive {
			return id
		}
	}
	t.Fatal("no active challenge in the depot")
	return ""
}

func stageDetail(out schema.Outcome, name string) string {
	i := out.StageIndex(name)
	if i < 0 {
		return ""
	}
	return out.Stages[i].Detail
}

func stageState(out schema.Outcome, name string) string {
	i := out.StageIndex(name)
	if i < 0 {
		return ""
	}
	return out.Stages[i].State
}
