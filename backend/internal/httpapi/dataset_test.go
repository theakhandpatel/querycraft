package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dataset"
	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/perf"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* Dataset routing (M5 defect fix).
 *
 * The perf fixture is a second dataset served from a per-account writable
 * environment, and a plan lesson's authored result was written against its
 * 10 000 rows. These tests pin the one rule all three statement routes share:
 * an explicit `dataset` wins, `exercise.perf` implies perf, everything else is
 * the commerce dataset every existing client already got.
 */

// queryBody posts a raw body to /query and decodes the QueryResult, so a test
// can send a `dataset` field that queryOn's map does not carry.
func queryBody(t *testing.T, s *Server, token, body string) (int, engine.QueryResult) {
	t.Helper()
	w := doAs(t, s, token, http.MethodPost, "/query", body)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	return w.Code, qr
}

func TestResolveDataset(t *testing.T) {
	perfExercise := &planExercise{Perf: &perf.PerfContract{}}
	cases := []struct {
		name     string
		explicit string
		exercise *planExercise
		want     string
	}{
		{"absent is commerce", "", nil, ""},
		{"empty is commerce", "   ", nil, ""},
		{"commerce is commerce", "commerce", nil, ""},
		{"commerce is case-insensitive", "  Commerce ", nil, ""},
		{"explicit perf wins", "perf", nil, dataset.PerfDatasetID},
		{"perf is case-insensitive", "PERF", nil, dataset.PerfDatasetID},
		{"exercise.perf implies perf", "", perfExercise, dataset.PerfDatasetID},
		{"explicit commerce beats exercise.perf", "commerce", perfExercise, ""},
		{"explicit dataset beats exercise.perf", "perf", perfExercise, dataset.PerfDatasetID},
		{"unknown is returned for a named refusal", "warehouse", nil, "warehouse"},
		{"unknown beats exercise.perf", "warehouse", perfExercise, "warehouse"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := resolveDataset(c.explicit, c.exercise); got != c.want {
				t.Errorf("resolveDataset(%q, %v) = %q, want %q", c.explicit, c.exercise != nil, got, c.want)
			}
		})
	}
}

// An anonymous request for the perf dataset is refused with the same sentence
// /explain uses, because the fix is the same: the fixture lives in a signed-in
// learner's own environment.
func TestQueryRefusesAnonymousPerfDataset(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	status, qr := queryBody(t, s, "", `{"sql":"SELECT count(*) FROM orders","dataset":"perf"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeEnvironmentUnavailable)
	}
	if qr.Error.Message != perfSignInRefusal {
		t.Errorf("message = %q, want the /explain sign-in sentence %q", qr.Error.Message, perfSignInRefusal)
	}
}

// The same rule holds for grading: an anonymous perf evaluate is refused before
// any environment is touched, with the sign-in sentence.
func TestEvaluateRefusesAnonymousPerfDataset(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	w := do(t, s, http.MethodPost, "/answers/evaluate",
		`{"sql":"SELECT count(*) FROM orders","dataset":"perf","exercise":{"id":"ex.perf","version":1}}`)
	var resp struct {
		Passed bool `json:"passed"`
		QR     engine.QueryResult
	}
	mustDecode(t, w, &resp)
	if resp.Passed {
		t.Fatalf("a refused dataset must not pass")
	}
	if resp.QR.Error == nil || resp.QR.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("qr error = %+v, want %s", resp.QR.Error, engine.CodeEnvironmentUnavailable)
	}
	if resp.QR.Error.Message != perfSignInRefusal {
		t.Errorf("message = %q, want %q", resp.QR.Error.Message, perfSignInRefusal)
	}
}

// An exercise authored for the perf fixture routes without an explicit dataset,
// the fallback /explain already had — and the evaluator still reads the same
// "exercise" object, which is why /answers/evaluate decodes the body twice.
func TestEvaluateRoutesOnExercisePerf(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	w := do(t, s, http.MethodPost, "/answers/evaluate",
		`{"sql":"SELECT count(*) FROM orders","exercise":{"id":"ex.perf","version":1,"perf":{"engine":"postgresql"}}}`)
	var resp struct {
		Passed bool `json:"passed"`
		QR     engine.QueryResult
	}
	mustDecode(t, w, &resp)
	if resp.QR.Error == nil || resp.QR.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("qr error = %+v, want the perf sign-in refusal", resp.QR.Error)
	}
	if resp.QR.Error.Message != perfSignInRefusal {
		t.Errorf("message = %q, want %q", resp.QR.Error.Message, perfSignInRefusal)
	}
}

// Naming the commerce dataset explicitly is the same request as naming none:
// same executor, same rows, same status. Nothing that works today changes.
func TestQueryExplicitCommerceIsUnchanged(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	plainStatus, plain := queryBody(t, s, "", `{"sql":"SELECT count(*) AS n FROM orders"}`)
	namedStatus, named := queryBody(t, s, "", `{"sql":"SELECT count(*) AS n FROM orders","dataset":"commerce"}`)
	if plainStatus != http.StatusOK || namedStatus != http.StatusOK {
		t.Fatalf("statuses = %d and %d, want 200", plainStatus, namedStatus)
	}
	// request_id is per request by design, so it is the one field excluded.
	plain.RequestID, named.RequestID = nil, nil
	if plain.Status != named.Status || plain.RowCount != named.RowCount || plain.Engine != named.Engine || plain.Dialect != named.Dialect {
		t.Fatalf("explicit commerce = %+v, anonymous default = %+v", named, plain)
	}
	if !reflect.DeepEqual(named.Rows, plain.Rows) {
		t.Fatalf("explicit commerce rows = %v, anonymous default rows = %v", named.Rows, plain.Rows)
	}
}

// A dataset this gateway does not serve is refused by name, listing the two it
// does serve, with ENVIRONMENT_UNAVAILABLE — the same code /explain reports.
func TestQueryRefusesUnknownDataset(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	status, qr := queryBody(t, s, "", `{"sql":"SELECT 1","dataset":"warehouse"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeEnvironmentUnavailable)
	}
	for _, want := range []string{"commerce", "perf", "warehouse"} {
		if !strings.Contains(qr.Error.Message, want) {
			t.Errorf("message %q does not name %q", qr.Error.Message, want)
		}
	}
}

// The perf lab is a PostgreSQL lab, so a perf request in another dialect is
// DIALECT_MISMATCH naming postgresql rather than a run against the wrong rules.
func TestQueryRefusesPerfWithSQLite(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})

	status, qr := queryBody(t, s, "", `{"sql":"SELECT 1","dataset":"perf","dialect":"sqlite"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeDialectMismatch {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeDialectMismatch)
	}
	if !strings.Contains(strings.ToLower(qr.Error.Message), engine.DialectPostgres) {
		t.Errorf("message %q does not name postgresql", qr.Error.Message)
	}
}

// The same refusal, on the grading route.
func TestEvaluateRefusesPerfWithSQLite(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})

	w := do(t, s, http.MethodPost, "/answers/evaluate",
		`{"sql":"SELECT 1","dataset":"perf","dialect":"sqlite","exercise":{"id":"ex.perf","version":1}}`)
	var resp struct {
		Passed bool `json:"passed"`
		QR     engine.QueryResult
	}
	mustDecode(t, w, &resp)
	if resp.Passed {
		t.Fatalf("a refused dialect must not pass")
	}
	if resp.QR.Error == nil || resp.QR.Error.Code != engine.CodeDialectMismatch {
		t.Fatalf("qr error = %+v, want %s", resp.QR.Error, engine.CodeDialectMismatch)
	}
	if !strings.Contains(strings.ToLower(resp.QR.Error.Message), engine.DialectPostgres) {
		t.Errorf("message %q does not name postgresql", resp.QR.Error.Message)
	}
}

// /explain honours the explicit dataset too: "commerce" is the shared dataset,
// not an unknown id, and an explicit "perf" is read before the exercise marker.
func TestExplainHonoursAnExplicitDataset(t *testing.T) {
	s := newTestServer(&planHTTPFake{plan: httpSamplePlan})

	status, bundle := explainBody(t, s, `{"sql":"SELECT 1","dataset":"commerce"}`)
	if status != http.StatusOK || bundle.Status != "succeeded" {
		t.Fatalf("explicit commerce: status = %d, bundle = %+v", status, bundle)
	}

	status, bundle = explainBody(t, s, `{"sql":"SELECT 1","dataset":"PERF"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("explicit perf status = %d, want the anonymous refusal", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEnvironmentUnavailable || bundle.Error.Message != perfSignInRefusal {
		t.Fatalf("explicit perf error = %+v, want ENVIRONMENT_UNAVAILABLE with the sign-in sentence", bundle.Error)
	}
}

// The real seam: a signed-in learner's perf statement runs on the 10 000-row
// fixture, and their commerce workspace is still the 80-row one — the two
// datasets are different databases, which is what lets a plan lesson CREATE
// INDEX without touching the challenge tables.
func TestPerfDatasetIsServedToASignedInLearner(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	shared, err := engine.NewPgxExecutor(ctx, srv.LearnerDSN(t, srv.Database))
	if err != nil {
		t.Fatalf("open shared read-only executor: %v", err)
	}
	defer shared.Close()

	m, err := envdb.New(ctx, envdb.Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		MaxEnvironments: 4,
		IdleTTL:         time.Hour,
		Log:             pgtest.QuietLogger(),
	})
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	defer m.Close()

	s := New(shared, store.NewMemory(), Config{MaxRows: 200, TimeoutMS: 10_000, MaxConcurrent: 2}, pgtest.QuietLogger())
	s.UseEnvironments(m)
	token := signupUser(t, s, "perf-dataset@example.test")

	/* The perf workspace is provisioned on first use: the gateway copies the
	   large perf template inside the request. That copy runs on envdb's own
	   provisioning deadline, not this request's statement deadline, so one
	   request provisions and runs. The retry that used to live here papered
	   over the copy outliving the statement timeout, which cancelled the copy
	   and left the manager's admin connection closed ("conn closed"). */
	status, perf := queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM orders","dataset":"perf"}`)
	if status != http.StatusOK || perf.Status != "succeeded" {
		t.Fatalf("signed-in perf /query: status = %d, result = %+v", status, perf)
	}
	if got := rowInt(perf, 0, 0); got != dataset.PerfScaleFactorOrders {
		t.Errorf("perf orders = %d, want the %d-row fixture", got, dataset.PerfScaleFactorOrders)
	}

	// The same account, the commerce dataset: a separate environment, still the
	// small fixture.
	status, commerce := queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM orders"}`)
	if status != http.StatusOK || commerce.Status != "succeeded" {
		t.Fatalf("signed-in commerce /query: status = %d, result = %+v", status, commerce)
	}
	if got := rowInt(commerce, 0, 0); got != dataset.CommerceScaleFactor {
		t.Errorf("commerce orders = %d, want %d", got, dataset.CommerceScaleFactor)
	}
}

// TestEnvironmentResetServesBothDatasets pins `POST /environment/reset?dataset=`.
//
// "Restore my data" is a different database depending on the course the learner
// is looking at: the challenge workspace (commerce) or the plan lab (perf),
// which are separate databases precisely so a re-indexed perf table cannot
// decide the plan a challenge lesson is graded on. Before this the reset route
// only ever restored the challenge workspace, so the reset control on a plan
// lesson reported success and left the learner's re-indexed perf tables exactly
// as they were.
func TestEnvironmentResetServesBothDatasets(t *testing.T) {
	srv := pgtest.Shared(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	shared, err := engine.NewPgxExecutor(ctx, srv.LearnerDSN(t, srv.Database))
	if err != nil {
		t.Fatalf("open shared read-only executor: %v", err)
	}
	defer shared.Close()

	m, err := envdb.New(ctx, envdb.Options{
		AdminDSN:        srv.DSN,
		LearnerPassword: srv.Password(),
		MaxEnvironments: 4,
		IdleTTL:         time.Hour,
		Log:             pgtest.QuietLogger(),
	})
	if err != nil {
		t.Fatalf("new environment manager: %v", err)
	}
	defer m.Close()

	s := New(shared, store.NewMemory(), Config{MaxRows: 200, TimeoutMS: 10_000, MaxConcurrent: 2}, pgtest.QuietLogger())
	s.UseEnvironments(m)
	token := signupUser(t, s, "perf-reset@example.test")

	// Give the challenge workspace a change of its own, so "the perf reset did
	// not touch commerce" is a measurement rather than a claim.
	commerceChange := `INSERT INTO customers (id, name, email, city, signup_date)
	                   VALUES (9999, 'Reset Probe', 'reset@example.test', 'Test', '2024-01-01')`
	if w := doAs(t, s, token, http.MethodPost, "/query", `{"sql":`+jsonString(commerceChange)+`}`); w.Code != http.StatusOK {
		t.Fatalf("commerce write: status = %d, body = %s", w.Code, w.Body.String())
	}

	// And change the plan lab by emptying a relation — a destructive edit only
	// the learner's own copy allows, which is exactly what a reset has to undo.
	// `line_items` is the perf fixture's unreferenced relation, so it is the one
	// TRUNCATE PostgreSQL allows without a CASCADE.
	truncate := `TRUNCATE order_items`
	if w := doAs(t, s, token, http.MethodPost, "/query", `{"sql":`+jsonString(truncate)+`,"dataset":"perf"}`); w.Code != http.StatusOK {
		t.Fatalf("perf truncate: status = %d, body = %s", w.Code, w.Body.String())
	}
	if status, empty := queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM order_items","dataset":"perf"}`); status != http.StatusOK || rowInt(empty, 0, 0) != 0 {
		t.Fatalf("perf order_items after truncate = %d (status %d), want 0", rowInt(empty, 0, 0), status)
	}

	if w := doAs(t, s, token, http.MethodPost, "/environment/reset?dataset=perf", ""); w.Code != http.StatusOK {
		t.Fatalf("perf reset: status = %d, body = %s", w.Code, w.Body.String())
	}

	// The perf fixture is back...
	if status, restored := queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM order_items","dataset":"perf"}`); status != http.StatusOK || rowInt(restored, 0, 0) != 2*dataset.PerfScaleFactorOrders {
		t.Errorf("perf order_items after reset = %d (status %d), want %d", rowInt(restored, 0, 0), status, 2*dataset.PerfScaleFactorOrders)
	}
	// ...and the learner's commerce work survived a reset it did not ask for.
	status, commerce := queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM customers WHERE id = 9999"}`)
	if status != http.StatusOK || rowInt(commerce, 0, 0) != 1 {
		t.Errorf("commerce probe row after a perf reset = %d (status %d), want 1: the perf reset restored the challenge workspace too", rowInt(commerce, 0, 0), status)
	}

	// An unknown dataset is refused by name rather than silently resetting the
	// challenge workspace the learner did not ask about. The shape is the
	// route's own {ok:false,error} envelope (a reset is not a statement), not a
	// QueryResult.
	w := doAs(t, s, token, http.MethodPost, "/environment/reset?dataset=warehouse", "")
	var refusal struct {
		OK    bool `json:"ok"`
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	mustDecode(t, w, &refusal)
	if w.Code != http.StatusBadRequest || refusal.OK || refusal.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("unknown dataset reset: status = %d, body = %s", w.Code, w.Body.String())
	}
	if !strings.Contains(refusal.Error.Message, "commerce") || !strings.Contains(refusal.Error.Message, dataset.PerfDatasetID) {
		t.Errorf("refusal message %q does not name the datasets the gateway serves", refusal.Error.Message)
	}
	// The commerce probe row is still there: the refusal reset nothing.
	status, commerce = queryBody(t, s, token, `{"sql":"SELECT count(*) AS n FROM customers WHERE id = 9999"}`)
	if status != http.StatusOK || rowInt(commerce, 0, 0) != 1 {
		t.Errorf("commerce probe row after a refused reset = %d (status %d), want 1", rowInt(commerce, 0, 0), status)
	}
}

// jsonString quotes a SQL string for a hand-written request body. The bodies
// here are literals, not data, so encoding them is clearer than building maps.
func jsonString(sql string) string {
	out, err := json.Marshal(sql)
	if err != nil {
		panic(err)
	}
	return string(out)
}
