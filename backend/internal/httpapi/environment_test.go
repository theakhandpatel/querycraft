package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/envdb"
	"github.com/querycraft/sqlquery/backend/internal/pgtest"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

/* ADR 0003: the writable learner workspace over HTTP.
 *
 * A signed-in learner's SQL runs in their own database, copied from the sealed
 * template; the write survives later requests and is gone after a reset.
 * Anonymous callers stay on the shared, read-only dataset and are told, not
 * shown a fabricated workspace. A gateway with no database keeps saying
 * ENVIRONMENT_UNAVAILABLE. */

// envEnvelope is the shape both environment routes answer with.
type envEnvelope struct {
	OK          bool    `json:"ok"`
	DurationMS  float64 `json:"duration_ms"`
	Environment struct {
		Account   string `json:"account"`
		Database  string `json:"database"`
		Exists    bool   `json:"exists"`
		Writable  bool   `json:"writable"`
		Tables    int    `json:"tables"`
		CreatedAt string `json:"created_at"`
		ResetAt   string `json:"reset_at"`
		IdleUntil string `json:"idle_until"`
	} `json:"environment"`
}

// gatewayErrEnvelope is the standard error envelope.
type gatewayErrEnvelope struct {
	OK    bool `json:"ok"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func TestEnvironmentAnonymousHasNoWorkspace(t *testing.T) {
	s := newTestServer(&fakeExecutor{})

	w := do(t, s, http.MethodGet, "/environment", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /environment status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var out envEnvelope
	mustDecode(t, w, &out)
	t.Logf("anonymous GET /environment -> %s", w.Body.String())
	if !out.OK {
		t.Fatalf("ok = false, want true")
	}
	if out.Environment.Exists || out.Environment.Writable {
		t.Fatalf("anonymous environment = %+v, want exists=false writable=false", out.Environment)
	}

	// Reset is destructive and there is no anonymous workspace to reset, so the
	// refusal names the missing account instead of inventing one.
	w = do(t, s, http.MethodPost, "/environment/reset", "")
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous POST /environment/reset status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	var e gatewayErrEnvelope
	mustDecode(t, w, &e)
	if e.Error.Code != "AUTH_REQUIRED" {
		t.Fatalf("error.code = %q, want AUTH_REQUIRED", e.Error.Code)
	}
}

func TestEnvironmentNoDatabaseReportsUnavailable(t *testing.T) {
	s := newTestServer(engine.UnavailableExecutor{})

	// Anonymous still has no workspace, and that is a fact about identity, not a
	// database outage: it is reported, not hidden behind an error.
	w := do(t, s, http.MethodGet, "/environment", "")
	if w.Code != http.StatusOK {
		t.Fatalf("anonymous GET /environment status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var anon envEnvelope
	mustDecode(t, w, &anon)
	if anon.Environment.Exists || anon.Environment.Writable {
		t.Fatalf("anonymous environment = %+v, want no workspace", anon.Environment)
	}

	token := signupUser(t, s, "no-database@example.test")

	w = doAs(t, s, token, http.MethodGet, "/environment", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("signed-in GET /environment status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	var e gatewayErrEnvelope
	mustDecode(t, w, &e)
	if e.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("GET /environment error.code = %q, want %s", e.Error.Code, engine.CodeEnvironmentUnavailable)
	}

	w = doAs(t, s, token, http.MethodPost, "/environment/reset", "")
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("signed-in POST /environment/reset status = %d, want 503; body = %s", w.Code, w.Body.String())
	}
	mustDecode(t, w, &e)
	if e.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("POST /environment/reset error.code = %q, want %s", e.Error.Code, engine.CodeEnvironmentUnavailable)
	}

	// A signed-in statement is refused with the same honest code, not routed to
	// a shared read-only connection that would answer a write with a permission
	// error about the wrong database.
	w = doAs(t, s, token, http.MethodPost, "/query", `{"sql":"SELECT 1"}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("signed-in /query error = %+v, want %s", qr.Error, engine.CodeEnvironmentUnavailable)
	}
}

// TestEnvironmentWorkspacePersistsAndResets is the claim of ADR 0003 over the
// real seam: a learner writes into their own database, the write survives the
// next request, anonymous stays read-only on the shared dataset, and a reset
// restores the sealed data and discards the write.
func TestEnvironmentWorkspacePersistsAndResets(t *testing.T) {
	srv := pgtest.Start(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	// The shared dataset the anonymous path uses, as the restricted role.
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
	token := signupUser(t, s, "workspace@example.test")

	t.Run("anonymous stays read-only on the shared dataset", func(t *testing.T) {
		if res := querySQL(t, s, "", "SELECT count(*) FROM customers"); res.Status != "succeeded" {
			t.Fatalf("anonymous SELECT failed: %+v", res.Error)
		}
		res := querySQL(t, s, "", "CREATE TABLE anon_probe (n int)")
		if res.Status != "failed" || res.Error == nil {
			t.Fatalf("anonymous CREATE TABLE = %+v, want a refusal", res)
		}
	})

	t.Run("signed-in workspace is created lazily and described", func(t *testing.T) {
		w := doAs(t, s, token, http.MethodGet, "/environment", "")
		if w.Code != http.StatusOK {
			t.Fatalf("GET /environment status = %d, body = %s", w.Code, w.Body.String())
		}
		t.Logf("signed-in GET /environment -> %s", w.Body.String())
		var out envEnvelope
		mustDecode(t, w, &out)
		if !out.OK || !out.Environment.Exists || !out.Environment.Writable {
			t.Fatalf("environment = %+v, want exists and writable", out.Environment)
		}
		if out.Environment.Database == "" {
			t.Fatalf("environment.database is empty")
		}
		if out.Environment.Tables < 4 {
			t.Fatalf("environment.tables = %d, want the commerce tables", out.Environment.Tables)
		}
	})

	t.Run("a write persists across requests", func(t *testing.T) {
		if res := querySQL(t, s, token, "CREATE TABLE learner_notes (n text)"); res.Status != "succeeded" {
			t.Fatalf("CREATE TABLE failed: %+v", res.Error)
		}
		if res := querySQL(t, s, token, "INSERT INTO learner_notes VALUES ('kept')"); res.Status != "succeeded" {
			t.Fatalf("INSERT failed: %+v", res.Error)
		}
		// A later, separate request sees the row: the write is the learner's and
		// it lives in their database, not in a rolled-back transaction.
		res := querySQL(t, s, token, "SELECT n FROM learner_notes")
		if res.Status != "succeeded" || len(res.Rows) != 1 || res.Rows[0][0] != "kept" {
			t.Fatalf("SELECT after INSERT = %+v, want the persisted row", res)
		}
	})

	t.Run("reset restores the sealed dataset and discards the write", func(t *testing.T) {
		w := doAs(t, s, token, http.MethodPost, "/environment/reset", "")
		if w.Code != http.StatusOK {
			t.Fatalf("POST /environment/reset status = %d, body = %s", w.Code, w.Body.String())
		}
		t.Logf("signed-in POST /environment/reset -> %s", w.Body.String())
		var out envEnvelope
		mustDecode(t, w, &out)
		if !out.OK || out.DurationMS <= 0 {
			t.Fatalf("reset = %+v, want ok and a positive duration", out)
		}
		if !out.Environment.Exists || !out.Environment.Writable {
			t.Fatalf("post-reset environment = %+v, want exists and writable", out.Environment)
		}
		if out.Environment.ResetAt == "" {
			t.Fatalf("post-reset environment.reset_at is empty")
		}

		if res := querySQL(t, s, token, "SELECT count(*) FROM customers"); res.Status != "succeeded" || rowInt(res, 0, 0) != 30 {
			t.Fatalf("after reset customers = %+v, want the seeded 30", res)
		}
		if res := querySQL(t, s, token, "SELECT n FROM learner_notes"); res.Status != "failed" {
			t.Fatalf("after reset the learner table still exists: %+v", res)
		}
	})
}

// querySQL runs one statement as the caller and returns the typed result.
func querySQL(t *testing.T, s *Server, token, sql string) engine.QueryResult {
	t.Helper()
	w := doAs(t, s, token, http.MethodPost, "/query", `{"sql":`+strconv.Quote(sql)+`}`)
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	return qr
}

// rowInt reads an integer cell whatever width the driver reports it at and
// whatever the JSON round trip did to it: the engine normalises PostgreSQL int8
// to a Go integer, and json.Unmarshal reads it back as a float64.
func rowInt(res engine.QueryResult, row, col int) int {
	t := 0
	switch v := res.Rows[row][col].(type) {
	case int:
		t = v
	case int32:
		t = int(v)
	case int64:
		t = int(v)
	case float64:
		t = int(v)
	}
	return t
}
