package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL puts permission denied in SQLSTATE class 42, whose name is "syntax
// error or access rule violation". The access-rule half is not a syntax error:
// the learner's SQL is well formed and no edit to it would help. Calling it a
// syntax error sends them to fix the one thing that is not wrong.
func TestClassifyReportsDeniedPrivilegeAsRuntime(t *testing.T) {
	err := &pgconn.PgError{
		Code:    "42501",
		Message: "permission denied for function pg_read_file",
	}
	got := classify(context.Background(), err)
	if got.Code != CodeRuntime {
		t.Fatalf("42501 classified as %s, want %s", got.Code, CodeRuntime)
	}
	if got.Message != err.Message {
		t.Errorf("message = %q, want the server's own words", got.Message)
	}
}

// QUERY_SYNTAX_ERROR is the parser rejecting the statement — the one case where
// "look at your SQL" is the only useful advice. The other class-42 codes a
// lesson actually hits are errors about a *name*, not about syntax, and design
// §4.2 files them under QUERY_RUNTIME_ERROR. They used to be reported as syntax
// because the SQLSTATE class is *named* "syntax error or access rule
// violation", which is a historical label rather than a classification, and
// because the browser adapter already said runtime: the same mistake was
// described two ways depending on which adapter ran the lesson.
func TestClassifySeparatesParserRejectionsFromNameErrors(t *testing.T) {
	syntax := []*pgconn.PgError{
		{Code: "42601", Message: `syntax error at or near "SELEC"`},
		{Code: "42603", Message: "unterminated quoted string"},
		{Code: "42604", Message: "invalid number"},
	}
	for _, err := range syntax {
		if got := classify(context.Background(), err); got.Code != CodeSyntax {
			t.Errorf("%s classified as %s, want %s", err.Code, got.Code, CodeSyntax)
		}
	}
	nameErrors := []*pgconn.PgError{
		{Code: "42P01", Message: `relation "ordrs" does not exist`},
		{Code: "42703", Message: `column "statuss" does not exist`},
		{Code: "42883", Message: `function countt(text) does not exist`},
		{Code: "42804", Message: "argument of WHERE must be type boolean"},
		{Code: "42702", Message: `column reference "id" is ambiguous`},
		{Code: "42803", Message: `column "amount" must appear in the GROUP BY clause`},
		{Code: "22P02", Message: "invalid input syntax for type integer"},
	}
	for _, err := range nameErrors {
		if got := classify(context.Background(), err); got.Code != CodeRuntime {
			t.Errorf("%s classified as %s, want %s", err.Code, got.Code, CodeRuntime)
		}
	}
}

// The rest of the table is shared with the browser adapter, SQLSTATE by
// SQLSTATE (web/src/api/pglite.ts, mapError). Pinned here so that changing a
// code on the Go side is a deliberate act and not an unnoticed divergence: the
// learner is told the same thing whichever adapter runs the lesson.
func TestClassifyMirrorsTheBrowserAdapterTable(t *testing.T) {
	cases := []struct {
		code string
		want string
	}{
		{"57014", CodeTimeout},
		{"55P03", CodeTimeout},
		{"54000", CodeResultLimit},
		{"53100", CodeResultLimit},
		{"53400", CodeResultLimit},
		{"53300", CodeEnvironmentUnavailable},
		{"3D000", CodeEnvironmentUnavailable},
		{"08006", CodeEnvironmentUnavailable},
		{"08003", CodeEnvironmentUnavailable},
		{"42501", CodeRuntime},
		{"23505", CodeRuntime},
		{"40001", CodeRuntime},
	}
	for _, tc := range cases {
		got := classify(context.Background(), &pgconn.PgError{Code: tc.code, Message: "server said so"})
		if got.Code != tc.want {
			t.Errorf("%s classified as %s, want %s", tc.code, got.Code, tc.want)
		}
	}
}

// A cancelled or timed-out statement is a timeout, including one that never
// reached the database.
func TestClassifyReportsContextErrorsAsTimeout(t *testing.T) {
	for _, err := range []error{context.DeadlineExceeded, context.Canceled, fmt.Errorf("wrapped: %w", context.DeadlineExceeded)} {
		if got := classify(context.Background(), err); got.Code != CodeTimeout {
			t.Errorf("%v classified as %s, want %s", err, got.Code, CodeTimeout)
		}
	}
}

// Waiting for a connection until the deadline is a timeout, not an outage. The
// wrong answer here is expensive: the load test and the adversarial suite both
// read this code, and "environment unavailable" would make a saturated-but-
// healthy gateway look broken.
func TestClassifyReportsPoolWaitAsTimeout(t *testing.T) {
	// This is the shape PgxExecutor produces when the pool is saturated.
	err := connectErr(fmt.Errorf("acquire: %w", context.DeadlineExceeded))
	got := classify(context.Background(), err)
	if got.Code != CodeTimeout {
		t.Fatalf("pool wait classified as %s, want %s", got.Code, CodeTimeout)
	}
	if got.Message == "" || got.Message == "Query exceeded the time limit and was stopped." {
		t.Errorf("message = %q, want one that names the wait for a connection", got.Message)
	}
	if errors.Is(err, ErrEnvironmentUnavailable) {
		t.Error("a pool wait is also marked as environment unavailable")
	}
}

// A database that cannot be reached is still an outage, and must not be
// flattened into a timeout by the change above.
func TestClassifyKeepsOutagesAsUnavailable(t *testing.T) {
	err := connectErr(errors.New("dial tcp 127.0.0.1:54329: connect: connection refused"))
	if !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("a refused dial is not marked unavailable: %v", err)
	}
	got := classify(context.Background(), err)
	if got.Code != CodeEnvironmentUnavailable {
		t.Fatalf("refused dial classified as %s, want %s", got.Code, CodeEnvironmentUnavailable)
	}
}

// The two paths must not be interchangeable: connectErr is the only place that
// decides, and it decides on the cause rather than on the call site.
func TestConnectErrDistinguishesDeadlineFromRefusal(t *testing.T) {
	if !errors.Is(connectErr(context.Canceled), errPoolTimeout) {
		t.Error("a cancelled wait for a connection is not a pool timeout")
	}
	if !errors.Is(connectErr(context.DeadlineExceeded), errPoolTimeout) {
		t.Error("an expired wait for a connection is not a pool timeout")
	}
	if errors.Is(connectErr(errors.New("boom")), errPoolTimeout) {
		t.Error("an arbitrary failure is reported as a pool timeout")
	}
}
