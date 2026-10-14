// Package engine owns the SQL execution boundary for Querycraft's Adapter B.
//
// All database access goes through the small Executor interface so the gateway
// logic, evaluator and HTTP handlers can be unit-tested against a fake without
// a live PostgreSQL server. PgxExecutor is the real pgx-backed implementation.
package engine

import (
	"context"
	"errors"
	"math"
	"strings"
	"time"
)

// EngineName is reported in every QueryResult as the execution engine.
const EngineName = "postgresql"

// Stable error codes shared with contracts/query-result.schema.json.
const (
	CodeEmptyQuery             = "EMPTY_QUERY"
	CodeSyntax                 = "QUERY_SYNTAX_ERROR"
	CodeRuntime                = "QUERY_RUNTIME_ERROR"
	CodeTimeout                = "QUERY_TIMEOUT"
	CodeResultLimit            = "RESULT_LIMIT_EXCEEDED"
	CodeEnvironmentUnavailable = "ENVIRONMENT_UNAVAILABLE"
	CodePersistenceFailed      = "PERSISTENCE_FAILED"
	CodeEvaluationError        = "EVALUATION_ERROR"
)

// ErrEnvironmentUnavailable marks failures that mean the engine itself could
// not be reached, as opposed to a problem with the submitted statement.
var ErrEnvironmentUnavailable = errors.New("execution environment unavailable")

// Column is one result column: name plus the PostgreSQL type name.
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
	// Close releases resources and rolls back the query transaction.
	Close()
}

// Executor is the entire database surface the gateway depends on.
type Executor interface {
	Query(ctx context.Context, sql string, args ...any) (Rows, error)
	Ping(ctx context.Context) error
	Version(ctx context.Context) (string, error)
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
func NewError(code, message, detail string) *QueryError {
	e := &QueryError{Code: code, Message: message}
	if strings.TrimSpace(detail) != "" {
		d := detail
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
	}
}
