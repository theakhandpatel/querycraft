package engine

import (
	"strings"
	"testing"
)

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // substring that must be gone
		keep string // substring that must survive
	}{
		{"url with password", "failed to dial postgres://learner:hunter2@db:5432/qc", "hunter2", "postgres://learner:xxxxx@db:5432/qc"},
		{"url without password", "dial postgres://learner@db:5432/qc", "", "postgres://learner@db:5432/qc"},
		{"keyword password", "host=db user=learner password=hunter2 dbname=qc", "hunter2", "password=xxxxx"},
		{"spaced keyword password", "password = hunter2", "hunter2", "password=xxxxx"},
		{"sslpassword", "sslpassword=hunter2 sslmode=require", "hunter2", "sslpassword=xxxxx"},
		{"uppercase keyword", "PASSWORD=hunter2", "hunter2", "PASSWORD=xxxxx"},
		{"no credentials", "dial tcp 127.0.0.1:54329: connection refused", "", "connection refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := RedactSecrets(tc.in)
			if tc.want != "" && strings.Contains(got, tc.want) {
				t.Fatalf("RedactSecrets(%q) = %q, still contains %q", tc.in, got, tc.want)
			}
			if !strings.Contains(got, tc.keep) {
				t.Fatalf("RedactSecrets(%q) = %q, lost %q", tc.in, got, tc.keep)
			}
		})
	}
}

// Every QueryError is built through NewError, so redaction there covers the HTTP
// response, the access log context and the lesson UI at once.
func TestNewErrorRedactsCredentials(t *testing.T) {
	const password = "hunter2-should-never-appear"
	e := NewError(CodeEnvironmentUnavailable,
		"connection to postgres://learner:"+password+"@127.0.0.1:5432/qc failed",
		"dsn=postgres://learner:"+password+"@127.0.0.1:5432/qc sslmode=disable")
	if strings.Contains(e.Message, password) || strings.Contains(*e.Detail, password) {
		t.Fatalf("password survived NewError: message=%q detail=%q", e.Message, *e.Detail)
	}
	if !strings.Contains(e.Message, "failed") || !strings.Contains(*e.Detail, "sslmode=disable") {
		t.Fatalf("redaction threw away the diagnostic: message=%q detail=%q", e.Message, *e.Detail)
	}
}

// The executor that cannot reach its database reports a redacted reason, and it
// is the same sentence the diagnostics view shows.
func TestUnavailableExecutorReasonIsRedacted(t *testing.T) {
	const password = "hunter2-should-never-appear"
	u := UnavailableExecutor{Reason: "dial postgres://learner:" + password + "@127.0.0.1:5432/qc: refused"}
	ready, reason := u.Ready(t.Context())
	if ready {
		t.Fatal("UnavailableExecutor reported ready")
	}
	if strings.Contains(reason, password) {
		t.Fatalf("reason carries the password: %s", reason)
	}
	if !strings.Contains(reason, "refused") {
		t.Fatalf("reason lost the cause: %s", reason)
	}
	if d := u.Diagnostics(t.Context()); strings.Contains(d["reason"].(string), password) {
		t.Fatalf("diagnostics carry the password: %v", d)
	}
}
