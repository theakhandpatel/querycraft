// Package engine owns the SQL execution boundary for Querycraft's Adapter B.
//
// All database access goes through the small Executor interface so the gateway
// logic, evaluator and HTTP handlers can be unit-tested against a fake without
// a live server. PgxExecutor backs PostgreSQL with pgx and SQLiteExecutor backs
// SQLite with this package's own driver (sqlite_raw.go).
package engine

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"

	"github.com/querycraft/sqlquery/backend/internal/dialect"
)

// EngineName is reported in every QueryResult as the execution engine. It is
// the human label for the default dialect, so it shares that dialect's name.
const EngineName = DialectPostgres

// Dialect names, shared with contracts/query-result.schema.json.
//
// `Engine` is the human string a learner reads ("PostgreSQL 18.3", "SQLite
// 3.45"); `Dialect` is the machine value a chapter is compared against, so the
// UI can refuse to grade a SQLite chapter on PostgreSQL instead of silently
// marking engine-specific values wrong.
const (
	DialectPostgres = "postgresql"
	DialectSQLite   = "sqlite"
)

// The stable error codes of contracts/query-result.schema.json, aliased to
// dialect's declaration so one declaration serves both packages. dialect owns
// the list because the profiles name a code and this package already imports
// dialect; a second copy here could drift from the profiles.
const (
	CodeEmptyQuery             = dialect.CodeEmptyQuery
	CodeSyntax                 = dialect.CodeSyntaxError
	CodeRuntime                = dialect.CodeRuntimeError
	CodeTimeout                = dialect.CodeTimeout
	CodeResultLimit            = dialect.CodeResultLimitExceeded
	CodeEnvironmentUnavailable = dialect.CodeEnvironmentUnavailable
	// CodeDialectMismatch is a product refusal, not an engine failure: the
	// statement is fine and the engine is fine, but the chapter was written for
	// a different dialect. Reusing ENVIRONMENT_UNAVAILABLE would hide a mistake
	// the learner can fix in one action behind a fault they cannot.
	CodeDialectMismatch = dialect.CodeDialectMismatch
	CodeEvaluationError = dialect.CodeEvaluationError
)

// ErrEnvironmentUnavailable marks failures that mean the engine itself could
// not be reached, as opposed to a problem with the submitted statement.
var ErrEnvironmentUnavailable = errors.New("execution environment unavailable")

// errPoolTimeout marks a request that spent its whole deadline waiting for a
// connection from the pool.
//
// The two conditions this separates are easy to confuse and mean opposite
// things: a saturated gateway is working correctly and should tell the learner
// their statement timed out, while an unreachable database is a fault. Wrapping
// a pool-acquire deadline as ErrEnvironmentUnavailable reports a healthy
// service as broken and points the learner at a problem that is not theirs.
var errPoolTimeout = errors.New("no execution connection became available in time")

// Column is one result column: name plus the type name the engine reports.
// PostgreSQL names a pg type; SQLite reports the declared type (sqlite.go).
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryError is the stable error envelope described by the QueryResult schema.
type QueryError struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Detail  *string `json:"detail,omitempty"`
}

// QueryResult matches contracts/query-result.schema.json exactly.
type QueryResult struct {
	Status     string      `json:"status"`
	Columns    []Column    `json:"columns"`
	Rows       [][]any     `json:"rows"`
	RowCount   int         `json:"row_count"`
	Truncated  bool        `json:"truncated"`
	DurationMS float64     `json:"duration_ms"`
	Error      *QueryError `json:"error"`
	RequestID  *string     `json:"request_id"`
	Engine     string      `json:"engine"`
	Dialect    string      `json:"dialect"`
}

// Limits are the per-request caps. Zero values fall back to defaults.
type Limits struct {
	MaxRows   int `json:"max_rows"`
	MaxBytes  int `json:"max_bytes"`
	TimeoutMS int `json:"timeout_ms"`
}

// Default values used when a caller leaves a limit unset.
const (
	DefaultMaxRows   = 500
	DefaultMaxBytes  = 1 << 20 // 1 MiB of serialized rows
	DefaultTimeoutMS = 5000    // 5 s hard timeout
)

// DefaultLimits returns the built-in caps.
func DefaultLimits() Limits {
	return Limits{MaxRows: DefaultMaxRows, MaxBytes: DefaultMaxBytes, TimeoutMS: DefaultTimeoutMS}
}

// Merge fills any unset field from fallback and never lets a caller raise a cap
// above it. A per-request limit may ask for less (a tighter row/byte/time
// budget) but not more: the fallback is the server's ceiling, so a request body
// cannot talk the gateway into unbounded work.
func (l Limits) Merge(fallback Limits) Limits {
	if l.MaxRows <= 0 || l.MaxRows > fallback.MaxRows {
		l.MaxRows = fallback.MaxRows
	}
	if l.MaxBytes <= 0 || l.MaxBytes > fallback.MaxBytes {
		l.MaxBytes = fallback.MaxBytes
	}
	if l.TimeoutMS <= 0 || l.TimeoutMS > fallback.TimeoutMS {
		l.TimeoutMS = fallback.TimeoutMS
	}
	return l
}

// Fill sets any UNSET field from fallback and never lowers a set one. It is the
// right merge when the caller's value is itself a ceiling (the operator's
// configured caps) rather than a request body that must not exceed the server's
// ceiling (that is Merge). Execute uses this: its caller has already resolved
// request-vs-server through Merge, so re-clamping to the compiled default here
// would drop a higher QC_MAX_ROWS.
func (l Limits) Fill(fallback Limits) Limits {
	if l.MaxRows <= 0 {
		l.MaxRows = fallback.MaxRows
	}
	if l.MaxBytes <= 0 {
		l.MaxBytes = fallback.MaxBytes
	}
	if l.TimeoutMS <= 0 {
		l.TimeoutMS = fallback.TimeoutMS
	}
	return l
}

// Timeout is the execution deadline derived from TimeoutMS.
func (l Limits) Timeout() time.Duration {
	return time.Duration(l.TimeoutMS) * time.Millisecond
}

// Rows is a forward-only cursor over a result set.
type Rows interface {
	// Columns returns column metadata. It is valid before the first Next.
	Columns() []Column
	// Next advances the cursor, reporting whether a row is available.
	Next() bool
	// Values returns the current row's values.
	Values() ([]any, error)
	// Err returns any error encountered while iterating.
	Err() error
	// Close releases resources and ends the query transaction. A read-only
	// transaction is rolled back; a writable one commits if the result was
	// fully read without error, and rolls back otherwise.
	Close()
}

// PoolStats is a point-in-time view of the connection pool learner statements
// run on. It is exported so the load test can publish saturation as measured,
// and so /metrics can show the same number the pool enforces.
type PoolStats struct {
	MaxConns          int `json:"max_conns"`
	TotalConns        int `json:"total_conns"`
	IdleConns         int `json:"idle_conns"`
	AcquiredConns     int `json:"acquired_conns"`
	ConstructingConns int `json:"constructing_conns"`
}

// Executor is the entire database surface the gateway depends on.
//
// The four capability methods (Dialect, EngineLabel, ClassifyError,
// Diagnostics) are on the interface rather than on optional interfaces beside
// it. Only PgxExecutor and SQLiteExecutor run statements, and
// UnavailableExecutor reports that nothing can. A probe that falls back to a
// default when an executor is silent hides a missing answer, and every answer
// here is one line.
type Executor interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Ping(ctx context.Context) error
	Version(ctx context.Context) (string, error)
	// Stats reports pool saturation. An executor with no pool reports zeroes
	// rather than a plausible-looking maximum.
	Stats() PoolStats
	// Ready reports whether a statement can run right now, and why not when it
	// cannot. It probes the server rather than trusting a cached flag, so a
	// database that went away is reported as not ready instead of healthy.
	Ready(ctx context.Context) (bool, string)
	// Dialect names the SQL dialect this executor runs: DialectPostgres or
	// DialectSQLite. It is the machine value a chapter is compared against, so
	// the UI can refuse to grade a SQLite chapter on PostgreSQL.
	Dialect() string
	// EngineLabel names the running engine in the words a learner reads
	// ("PostgreSQL 16.3", "SQLite 3.45").
	EngineLabel() string
	// ClassifyError names this engine's error in the contract's code set.
	//
	// The second return is why the contract is (code, ok) rather than a bare
	// string: "this executor has no opinion" is a real answer, and it must not
	// be confused with a classification that happens to be empty. An executor
	// that declines leaves the decision to the shared table in execute.go.
	//
	// SQLite needs this hook. It has no SQLSTATE, so the PostgreSQL table would
	// report a SQLite syntax error as a runtime error, while the dialect
	// profile already knows the same message is QUERY_SYNTAX_ERROR.
	ClassifyError(err error) (code string, ok bool)
	// Diagnostics describes the sandbox learner statements run in, read over
	// the learner's own connection. The values are the server answering for
	// the session (current_user, search_path) and the sandbox check performed
	// with the learner's own privileges rather than assumed. An executor with
	// no database to describe returns nil.
	Diagnostics(ctx context.Context) map[string]any
	Close() error
}

type ctxKey int

const requestIDKey ctxKey = iota

// WithRequestID attaches a correlation id to the context.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestIDFromContext returns the correlation id, if any.
func RequestIDFromContext(ctx context.Context) *string {
	if v, ok := ctx.Value(requestIDKey).(string); ok && v != "" {
		return &v
	}
	return nil
}

// NewError builds a QueryError from a code and message, optionally with detail.
//
// Both strings are redacted: a QueryError is rendered into an HTTP response, a
// log line and the lesson UI, and an underlying driver or proxy error can quote
// the connection string. Every QueryError in the gateway is built here, so the
// redaction cannot be forgotten at one call site.
func NewError(code, message, detail string) *QueryError {
	e := &QueryError{Code: code, Message: RedactSecrets(message)}
	if strings.TrimSpace(detail) != "" {
		d := RedactSecrets(detail)
		e.Detail = &d
	}
	return e
}

// RoundMS rounds a duration to one decimal place of milliseconds.
func RoundMS(d time.Duration) float64 {
	ms := float64(d.Nanoseconds()) / 1e6
	return math.Round(ms*10) / 10
}

// Failing builds a failed QueryResult without running anything.
func Failing(code, message, detail string, requestID *string) QueryResult {
	return QueryResult{
		Status:    "failed",
		Columns:   []Column{},
		Rows:      [][]any{},
		RowCount:  0,
		Truncated: false,
		Error:     NewError(code, message, detail),
		RequestID: requestID,
		Engine:    EngineName,
		Dialect:   DialectPostgres,
	}
}

// RefusingDialect builds the failed result for a request the running engine
// will not serve. The result reports the dialect that answered (the one that
// ran nothing and refused), while the message names both — the chapter's
// dialect and the engine's — because that pair is the whole fix: switch the
// engine, or pick a chapter written for this one.
func RefusingDialect(running, message string, requestID *string) QueryResult {
	res := Failing(CodeDialectMismatch, message, "", requestID)
	if d := strings.TrimSpace(running); d != "" {
		res.Dialect = d
	}
	return res
}
