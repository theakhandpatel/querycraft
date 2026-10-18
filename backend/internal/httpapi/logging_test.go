package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/querycraft/sqlquery/backend/internal/engine"
	"github.com/querycraft/sqlquery/backend/internal/store"
)

func captureServer(t *testing.T, exec engine.Executor) (*Server, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return New(exec, store.NewMemory(), Config{MaxRows: 100, TimeoutMS: 1000, MaxConcurrent: 2}, log), &buf
}

// requestLogLines returns every access-log line that names operation=<op>.
func requestLogLines(t *testing.T, buf *bytes.Buffer, op string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(buf.String(), "\n") {
		if strings.Contains(line, "msg=request") && strings.Contains(line, "operation="+op) {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no access log line for operation %q in:\n%s", op, buf.String())
	}
	return out
}

// requestLogLine returns the first access-log line that names operation=<op>.
func requestLogLine(t *testing.T, buf *bytes.Buffer, op string) string {
	t.Helper()
	return requestLogLines(t, buf, op)[0]
}

// The access log is how an operator finds out what the gateway did. §13.1 asks
// for the operation, its duration, the status and the stable error code: a line
// with only method and path cannot tell a timeout from a syntax error.
func TestAccessLogCarriesOperationDurationStatusAndErrorCode(t *testing.T) {
	s, buf := captureServer(t, &fakeExecutor{})

	do(t, s, http.MethodPost, "/query", `{"sql":"   "}`)
	line := requestLogLine(t, buf, "query")
	for _, want := range []string{
		"operation=query",
		"route=/query",
		"method=POST",
		"path=/query",
		"status=400",
		"duration_ms=",
		"error_code=EMPTY_QUERY",
		"request_id=rq_",
	} {
		if !strings.Contains(line, want) {
			t.Errorf("access log line missing %q: %s", want, line)
		}
	}

	do(t, s, http.MethodGet, "/health", "")
	healthy := requestLogLine(t, buf, "health")
	if !strings.Contains(healthy, "status=200") {
		t.Errorf("health line missing status: %s", healthy)
	}
	if strings.Contains(healthy, "error_code=") {
		t.Errorf("a successful request carries an error_code: %s", healthy)
	}
}

// An AI route's failures used to log no error code at all: writeAIError writes
// the code inside a *schema.Error, a shape the old body-inspecting switch never
// knew, and an outcome's error is another shape it never knew. The writers now
// name the code, so an AI refusal is filterable like a statement refusal.
func TestAccessLogNamesTheAICode(t *testing.T) {
	s, buf := captureServer(t, &fakeExecutor{})
	token := signupUser(t, s, "log-ai@example.test")

	// No generation service is configured: /courses/{id}/build refuses through
	// writeAIError, whose body is an *schema.Error.
	doAs(t, s, token, http.MethodPost, "/courses/course-1/build", "")
	refusal := requestLogLines(t, buf, "build_course")[0]
	for _, want := range []string{"status=503", "error_code=AI_UNAVAILABLE"} {
		if !strings.Contains(refusal, want) {
			t.Errorf("access log line missing %q: %s", want, refusal)
		}
	}

	// An unreadable body on an AI route keeps the AI vocabulary from the route
	// table, not the query one.
	doAs(t, s, token, http.MethodPost, "/courses/generate", `{"goal":`)
	malformed := requestLogLines(t, buf, "generate_course")[0]
	if !strings.Contains(malformed, "error_code=AI_INVALID_REQUEST") {
		t.Errorf("the unreadable AI body is not named: %s", malformed)
	}
}

// Every route gets a name, including the ones answered before routing, so a log
// query never has to reason about an empty operation.
func TestAccessLogNamesEveryOperation(t *testing.T) {
	s, buf := captureServer(t, &fakeExecutor{})
	do(t, s, http.MethodGet, "/capabilities", "")
	do(t, s, http.MethodGet, "/progress/sql-analytics", "") // 401 before the mux
	do(t, s, http.MethodGet, "/definitely/not/a/route", "")

	for _, op := range []string{"capabilities", "progress"} {
		if line := requestLogLine(t, buf, op); line == "" {
			t.Fatalf("no line for %s", op)
		}
	}
	progress := requestLogLine(t, buf, "progress")
	if !strings.Contains(progress, "error_code=AUTH_REQUIRED") {
		t.Errorf("the 401 error code is missing: %s", progress)
	}
	if !strings.Contains(progress, "route=/progress/{course}") {
		t.Errorf("the route is not the pattern: %s", progress)
	}
	if !strings.Contains(buf.String(), "operation=unmatched") && !strings.Contains(buf.String(), "operation=spa") {
		t.Errorf("an unknown path was not named:\n%s", buf.String())
	}
}

// credentialExecutor fails every query with an error whose text carries a DSN
// and a password, which is the shape a proxied or wrapped driver error takes.
type credentialExecutor struct{ dsn string }

func (c credentialExecutor) Query(ctx context.Context, sql string, args ...any) (engine.Rows, error) {
	return nil, fmt.Errorf("%w: dial %s: connection refused", engine.ErrEnvironmentUnavailable, c.dsn)
}
func (c credentialExecutor) Ping(ctx context.Context) error              { return nil }
func (c credentialExecutor) Version(ctx context.Context) (string, error) { return "", nil }
func (c credentialExecutor) Stats() engine.PoolStats                     { return engine.PoolStats{} }
func (c credentialExecutor) Ready(ctx context.Context) (bool, string) {
	// The real executor redacts the driver's message; this mirrors it, so the
	// test checks the gateway's own surfaces rather than the helper.
	return false, engine.RedactSecrets("dial " + c.dsn + ": connection refused")
}
func (c credentialExecutor) Dialect() string                        { return engine.DialectPostgres }
func (c credentialExecutor) EngineLabel() string                    { return engine.EngineName }
func (c credentialExecutor) ClassifyError(err error) (string, bool) { return "", false }
func (c credentialExecutor) Diagnostics(ctx context.Context) map[string]any {
	// Nil keeps the learner block out of /diagnostics, as the old probe did for
	// an executor with no sandbox to describe.
	return nil
}
func (c credentialExecutor) Close() error { return nil }

// The log must not become the place a password leaks. It records the error code
// and never the body, and the envelope it writes is redacted as well, because
// both the log file and the HTTP response outlive the request.
func TestLogAndResponseNeverCarryCredentials(t *testing.T) {
	const password = "hunter2-should-never-appear"
	dsn := "postgres://querycraft_learner:" + password + "@127.0.0.1:54329/querycraft?sslmode=disable"

	s, buf := captureServer(t, credentialExecutor{dsn: dsn})
	w := do(t, s, http.MethodPost, "/query", `{"sql":"select 1"}`)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if strings.Contains(w.Body.String(), password) {
		t.Errorf("response body carries the password:\n%s", w.Body.String())
	}
	var qr engine.QueryResult
	mustDecode(t, w, &qr)
	if qr.Error == nil || qr.Error.Code != engine.CodeEnvironmentUnavailable {
		t.Fatalf("error = %+v, want %s", qr.Error, engine.CodeEnvironmentUnavailable)
	}
	if strings.Contains(qr.Error.Message, password) {
		t.Errorf("error message carries the password: %s", qr.Error.Message)
	}
	if qr.Error.Detail == nil || !strings.Contains(*qr.Error.Detail, "connection refused") {
		t.Errorf("detail lost the diagnostic value: %v", qr.Error.Detail)
	}

	for _, surface := range []string{buf.String(), w.Body.String()} {
		if strings.Contains(surface, password) {
			t.Errorf("credential leaked into a surface:\n%s", surface)
		}
	}

	// The diagnostics view is session-gated, so it must be safe too.
	token := signupUser(t, s, "diagnostics-leak@example.test")
	diag := doAs(t, s, token, http.MethodGet, "/diagnostics", "")
	if strings.Contains(diag.Body.String(), password) {
		t.Errorf("diagnostics leaks the password:\n%s", diag.Body.String())
	}
	if !strings.Contains(diag.Body.String(), "connection refused") {
		t.Errorf("diagnostics hid the reason entirely:\n%s", diag.Body.String())
	}
}
