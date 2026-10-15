package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
)

/* Dialect routing (M6 step 4): the gateway picks the executor that speaks the
   requested dialect, refuses the ones it cannot serve, and keeps PostgreSQL the
   default so an existing client changes nothing. These tests use the real
   SQLite store over a temporary data root — the same seeding path the gateway
   uses — rather than a stand-in, because the isolation claim is about files. */

// sqliteTestServer is a test server with the SQLite dialect attached. The
// temp root means each test gets fresh files, so one test's INSERT cannot leak
// into another's.
func sqliteTestServer(t *testing.T, exec engine.Executor) *Server {
	t.Helper()
	envs, err := NewSQLiteEnvironments(t.TempDir())
	if err != nil {
		t.Fatalf("NewSQLiteEnvironments: %v", err)
	}
	t.Cleanup(func() { _ = envs.Close() })
	s := newTestServer(exec)
	s.UseSQLite(envs)
	return s
}

// queryOn posts one statement and decodes the QueryResult, so the assertions
// below read the same shape the client does.
func queryOn(t *testing.T, s *Server, token, dialect, sql string) (int, engine.QueryResult) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"sql": sql, "dialect": dialect})
	if err != nil {
		t.Fatalf("marshal body: %v", err)
	}
	w := doAs(t, s, token, http.MethodPost, "/query", string(body))
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	return w.Code, qr
}

// scalar reads the single number a count query returns.
func scalar(t *testing.T, qr engine.QueryResult) float64 {
	t.Helper()
	if len(qr.Rows) != 1 || len(qr.Rows[0]) != 1 {
		t.Fatalf("rows = %v, want one row of one column", qr.Rows)
	}
	n, ok := qr.Rows[0][0].(float64)
	if !ok {
		t.Fatalf("value = %#v, want a JSON number", qr.Rows[0][0])
	}
	return n
}

func TestQueryOnSQLiteRunsTheCommittedFixture(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})

	status, qr := queryOn(t, s, "", "sqlite", "SELECT count(*) AS n FROM customers")
	if status != http.StatusOK {
		t.Fatalf("status = %d, error = %+v", status, qr.Error)
	}
	if qr.Status != "succeeded" {
		t.Fatalf("status = %q, error = %+v", qr.Status, qr.Error)
	}
	if got := scalar(t, qr); got != 30 {
		t.Errorf("customer count = %v, want 30 from the committed SQLite seed", got)
	}
	if qr.Dialect != engine.DialectSQLite {
		t.Errorf("dialect = %q, want %q", qr.Dialect, engine.DialectSQLite)
	}
	if !strings.HasPrefix(qr.Engine, "SQLite ") {
		t.Errorf("engine = %q, want the SQLite adapter's own version label", qr.Engine)
	}
}

func TestTheSameStatementNamesItsOwnDialectAndEngine(t *testing.T) {
	// One server, one statement, two dialects: the result must say which engine
	// answered, because a learner looking at a wrong value needs to know which
	// engine's rules produced it.
	s := sqliteTestServer(t, &fakeExecutor{})

	pgStatus, pg := queryOn(t, s, "", "postgresql", "SELECT count(*) AS n FROM orders")
	if pgStatus != http.StatusOK {
		t.Fatalf("postgresql status = %d, error = %+v", pgStatus, pg.Error)
	}
	if pg.Dialect != engine.DialectPostgres {
		t.Errorf("postgresql dialect = %q, want %q", pg.Dialect, engine.DialectPostgres)
	}
	// The fake stands in for PostgreSQL, and an executor with no label of its
	// own keeps EngineName; the real adapter's "PostgreSQL 18.3" is asserted in
	// internal/engine.
	if pg.Engine != engine.EngineName {
		t.Errorf("postgresql engine = %q, want %q", pg.Engine, engine.EngineName)
	}

	sqliteStatus, lite := queryOn(t, s, "", "sqlite", "SELECT count(*) AS n FROM orders")
	if sqliteStatus != http.StatusOK {
		t.Fatalf("sqlite status = %d, error = %+v", sqliteStatus, lite.Error)
	}
	if lite.Dialect != engine.DialectSQLite {
		t.Errorf("sqlite dialect = %q, want %q", lite.Dialect, engine.DialectSQLite)
	}
	if !strings.HasPrefix(lite.Engine, "SQLite ") {
		t.Errorf("sqlite engine = %q, want the SQLite label", lite.Engine)
	}
	if got := scalar(t, lite); got != 80 {
		t.Errorf("order count = %v, want 80 from the committed SQLite seed", got)
	}
}

func TestAbsentDialectStillMeansPostgreSQL(t *testing.T) {
	// The field is optional on purpose (ADR 0004): a client that has never
	// heard of it keeps the behaviour it already had, even on a gateway that
	// serves SQLite.
	s := sqliteTestServer(t, &fakeExecutor{})
	w := do(t, s, http.MethodPost, "/query", `{"sql":"SELECT 1"}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Dialect != engine.DialectPostgres {
		t.Fatalf("dialect = %q, want %q when the request omits the field", qr.Dialect, engine.DialectPostgres)
	}
	if qr.Engine != engine.EngineName {
		t.Fatalf("engine = %q, want %q", qr.Engine, engine.EngineName)
	}
}

func TestQueryRefusesADialectTheGatewayCannotServe(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})

	status, qr := queryOn(t, s, "", "mysql", "SELECT 1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeDialectMismatch {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeDialectMismatch)
	}
	// The message has to name both sides: the dialect that was asked for and
	// the dialects this gateway actually serves. Either half alone leaves the
	// learner without an action.
	if !strings.Contains(qr.Error.Message, "mysql") {
		t.Errorf("message %q does not name the requested dialect", qr.Error.Message)
	}
	if !strings.Contains(qr.Error.Message, "postgresql") {
		t.Errorf("message %q does not name a dialect the gateway can serve", qr.Error.Message)
	}
	// Nothing ran, so nothing is invented.
	if len(qr.Rows) != 0 || len(qr.Columns) != 0 {
		t.Errorf("refusal carried rows/columns: %+v", qr)
	}
	if qr.Status != "failed" {
		t.Errorf("status = %q, want failed", qr.Status)
	}
}

func TestSQLiteIsRefusedWhenNoStoreIsRegistered(t *testing.T) {
	// A gateway without a SQLite store cannot serve the dialect, so it must say
	// DIALECT_MISMATCH — a fact the client can act on — and not
	// ENVIRONMENT_UNAVAILABLE, which would suggest retrying.
	s := newTestServer(&fakeExecutor{})
	if got := s.Dialects(); len(got) != 1 || got[0] != engine.DialectPostgres {
		t.Fatalf("dialects = %v, want postgresql alone", got)
	}
	status, qr := queryOn(t, s, "", "sqlite", "SELECT 1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeDialectMismatch {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeDialectMismatch)
	}
	if !strings.Contains(qr.Error.Message, "postgresql") {
		t.Errorf("message %q does not name the dialect the gateway can serve", qr.Error.Message)
	}
}

func TestSQLiteSyntaxErrorIsASyntaxErrorOverHTTP(t *testing.T) {
	// The whole point of the executor-first classifier: a SQLite parse failure
	// is advice about the learner's SQL, not a runtime error.
	s := sqliteTestServer(t, &fakeExecutor{})

	status, qr := queryOn(t, s, "", "sqlite", "SELEC 1")
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if qr.Error == nil || qr.Error.Code != engine.CodeSyntax {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeSyntax)
	}
	if qr.Dialect != engine.DialectSQLite {
		t.Errorf("dialect = %q, want %q even on a refusal", qr.Dialect, engine.DialectSQLite)
	}
}

func TestSQLiteAccountsCannotSeeEachOther(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})
	alice := signupUser(t, s, "alice@example.com")
	bob := signupUser(t, s, "bob@example.com")

	// Alice's write succeeds in her own file...
	status, wrote := queryOn(t, s, alice,
		"sqlite", "INSERT INTO customers (id, name, signup_date) VALUES (999, 'Alice Only', '2026-01-01')")
	if status != http.StatusOK || wrote.Status != "succeeded" {
		t.Fatalf("alice insert: status = %d, error = %+v", status, wrote.Error)
	}
	_, aliceCount := queryOn(t, s, alice, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, aliceCount); got != 31 {
		t.Fatalf("alice sees %v customers, want 31 after her insert", got)
	}

	// ...and Bob's file does not have it, by count or by name.
	_, bobCount := queryOn(t, s, bob, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, bobCount); got != 30 {
		t.Errorf("bob sees %v customers, want the untouched 30", got)
	}
	_, bobNamed := queryOn(t, s, bob, "sqlite", "SELECT count(*) AS n FROM customers WHERE name = 'Alice Only'")
	if got := scalar(t, bobNamed); got != 0 {
		t.Errorf("bob can see alice's row (%v matches); the accounts share a file", got)
	}

	// Anonymous callers read the shared fixture, and it is read-only.
	_, anon := queryOn(t, s, "", "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, anon); got != 30 {
		t.Errorf("anonymous sees %v customers, want the shared 30", got)
	}
	status, refused := queryOn(t, s, "", "sqlite", "DELETE FROM customers")
	if status != http.StatusBadRequest || refused.Error == nil {
		t.Fatalf("anonymous write: status = %d, error = %+v; want a refusal", status, refused.Error)
	}
}

func TestEvaluateGradesOnTheRequestedDialect(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})
	body := `{"sql":"SELECT count(*) AS n FROM customers","dialect":"sqlite","exercise":{
		"id":"ex.sqlite","version":1,
		"result_contract":{"columns":["n"],"column_order":"exact","row_order":"exact","duplicates":"preserved","numeric_tolerance":0},
		"result":{"columns":["n"],"rows":[[30]]}}}`

	w := do(t, s, http.MethodPost, "/answers/evaluate", body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", w.Code, w.Body.String())
	}
	var resp struct {
		Passed bool `json:"passed"`
		QR     engine.QueryResult
	}
	mustDecode(t, w, &resp)
	if resp.QR.Dialect != engine.DialectSQLite {
		t.Fatalf("qr dialect = %q, want %q", resp.QR.Dialect, engine.DialectSQLite)
	}
	if !strings.HasPrefix(resp.QR.Engine, "SQLite ") {
		t.Errorf("qr engine = %q, want the SQLite label", resp.QR.Engine)
	}
	if !resp.Passed {
		t.Errorf("expected the 30-customer answer to pass; body = %s", w.Body.String())
	}
}

func TestEvaluateRefusesAnUnservedDialect(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})
	w := do(t, s, http.MethodPost, "/answers/evaluate",
		`{"sql":"SELECT 1","dialect":"mysql","exercise":{"id":"ex.1","version":1}}`)
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
}

func TestExplainRefusesANonPostgresDialect(t *testing.T) {
	// The plan lab is PostgreSQL-only: SQLite has no EXPLAIN ANALYZE and no
	// cost model of this shape (ADR 0004), so the refusal names PostgreSQL
	// rather than running PostgreSQL's EXPLAIN on a SQLite request.
	s := sqliteTestServer(t, &planHTTPFake{plan: httpSamplePlan})

	status, bundle := explainBody(t, s, `{"sql":"SELECT 1","dialect":"sqlite"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if bundle.Error == nil || bundle.Error.Code != engine.CodeEvaluationError {
		t.Fatalf("error = %+v, want %s", bundle.Error, engine.CodeEvaluationError)
	}
	if !strings.Contains(bundle.Error.Message, "postgresql") {
		t.Errorf("message %q does not name the dialect the plan lab can capture", bundle.Error.Message)
	}
	if bundle.Plan != nil {
		t.Errorf("plan = %v, want null on a refusal", bundle.Plan)
	}
}

/* The dialect-aware reset (architecture review finding F2).
 *
 * "Restore my data" restores the workspace the learner is looking at. On a
 * SQLite chapter that workspace is a file, not a database, and the HTTP adapter
 * used to drop the dialect and reset PostgreSQL under the learner — returning
 * `ok` for a restore that did not happen. These tests are about files, so they
 * use the real SQLite store over a temp root, and they assert by querying the
 * engine afterwards rather than by trusting the response body. */

// resetSQLite posts a dialect-aware workspace reset as a signed-in learner.
func resetSQLite(t *testing.T, s *Server, token, query string) (int, map[string]any) {
	t.Helper()
	w := doAs(t, s, token, http.MethodPost, "/environment/reset"+query, "")
	var out map[string]any
	mustDecode(t, w, &out)
	return w.Code, out
}

func TestSQLiteResetDiscardsTheLearnersWrites(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})
	alice := signupUser(t, s, "reset-alice@example.com")

	// Alice writes into her own workspace file.
	status, wrote := queryOn(t, s, alice,
		"sqlite", "INSERT INTO customers (id, name, signup_date) VALUES (999, 'Reset Me', '2026-01-01')")
	if status != http.StatusOK || wrote.Status != "succeeded" {
		t.Fatalf("alice insert: status = %d, error = %+v", status, wrote.Error)
	}
	_, before := queryOn(t, s, alice, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, before); got != 31 {
		t.Fatalf("alice sees %v customers before reset, want 31", got)
	}

	// A dialect-aware reset restores the committed fixture.
	code, body := resetSQLite(t, s, alice, "?dialect=sqlite")
	if code != http.StatusOK {
		t.Fatalf("reset status = %d body = %v", code, body)
	}
	if ok, _ := body["ok"].(bool); !ok {
		t.Fatalf("reset body = %v, want ok:true", body)
	}
	_, after := queryOn(t, s, alice, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, after); got != 30 {
		t.Errorf("alice sees %v customers after reset, want the restored 30; her write survived", got)
	}
	_, named := queryOn(t, s, alice, "sqlite", "SELECT count(*) AS n FROM customers WHERE name = 'Reset Me'")
	if got := scalar(t, named); got != 0 {
		t.Errorf("%v rows named 'Reset Me' survived the reset", got)
	}
}

func TestSQLiteResetDoesNotTouchAnotherAccount(t *testing.T) {
	s := sqliteTestServer(t, &fakeExecutor{})
	alice := signupUser(t, s, "reset-scope-alice@example.com")
	bob := signupUser(t, s, "reset-scope-bob@example.com")

	if _, w := queryOn(t, s, alice, "sqlite",
		"INSERT INTO customers (id, name, signup_date) VALUES (999, 'Alice Only', '2026-01-01')"); w.Status != "succeeded" {
		t.Fatalf("alice insert failed: %+v", w.Error)
	}
	if _, w := queryOn(t, s, bob, "sqlite",
		"INSERT INTO customers (id, name, signup_date) VALUES (998, 'Bob Only', '2026-01-01')"); w.Status != "succeeded" {
		t.Fatalf("bob insert failed: %+v", w.Error)
	}

	if code, body := resetSQLite(t, s, alice, "?dialect=sqlite"); code != http.StatusOK {
		t.Fatalf("alice reset: status = %d body = %v", code, body)
	}

	// Alice's file is restored; Bob's is untouched: one learner's reset is not
	// another's.
	_, aCount := queryOn(t, s, alice, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, aCount); got != 30 {
		t.Errorf("alice sees %v customers after her reset, want 30", got)
	}
	_, bCount := queryOn(t, s, bob, "sqlite", "SELECT count(*) AS n FROM customers")
	if got := scalar(t, bCount); got != 31 {
		t.Errorf("bob sees %v customers, want his 31 — alice's reset touched his file", got)
	}
	_, bNamed := queryOn(t, s, bob, "sqlite", "SELECT count(*) AS n FROM customers WHERE name = 'Bob Only'")
	if got := scalar(t, bNamed); got != 1 {
		t.Errorf("bob's own row is gone (%v matches) after alice's reset", got)
	}
}

func TestSQLiteResetRequiresAnAccount(t *testing.T) {
	// An anonymous caller has no file to restore, exactly as it has no database
	// on the PostgreSQL path.
	s := sqliteTestServer(t, &fakeExecutor{})
	w := do(t, s, http.MethodPost, "/environment/reset?dialect=sqlite", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous reset status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
}

func TestHealthReportsEveryServedDialect(t *testing.T) {
	var plain map[string]any
	mustDecode(t, do(t, newTestServer(&fakeExecutor{}), http.MethodGet, "/health", ""), &plain)
	if plain["engine"] != engine.DialectPostgres {
		t.Errorf("engine = %v, want %q", plain["engine"], engine.DialectPostgres)
	}
	if got := stringList(plain["dialects"]); len(got) != 1 || got[0] != engine.DialectPostgres {
		t.Errorf("dialects = %v, want postgresql alone", got)
	}

	var withSQLite map[string]any
	mustDecode(t, do(t, sqliteTestServer(t, &fakeExecutor{}), http.MethodGet, "/health", ""), &withSQLite)
	got := stringList(withSQLite["dialects"])
	if len(got) != 2 || got[0] != engine.DialectPostgres || got[1] != engine.DialectSQLite {
		t.Fatalf("dialects = %v, want [postgresql sqlite]", got)
	}
	if withSQLite["engine"] != engine.DialectPostgres {
		t.Errorf("engine = %v, want the default dialect %q", withSQLite["engine"], engine.DialectPostgres)
	}
}
