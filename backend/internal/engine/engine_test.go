package engine

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type fakeRows struct {
	cols   []Column
	rows   [][]any
	i      int
	cur    []any
	err    error
	closed bool
}

func newFakeRows(cols []Column, rows [][]any) *fakeRows {
	return &fakeRows{cols: cols, rows: rows, i: -1}
}

func (r *fakeRows) Columns() []Column { return r.cols }
func (r *fakeRows) Next() bool {
	r.i++
	if r.i >= len(r.rows) {
		return false
	}
	r.cur = r.rows[r.i]
	return true
}
func (r *fakeRows) Values() ([]any, error) { return r.cur, nil }
func (r *fakeRows) Err() error             { return r.err }
func (r *fakeRows) Close()                 { r.closed = true }

type fakeExecutor struct {
	rows     *fakeRows
	err      error
	bySQL    map[string]*fakeRows
	errBySQL map[string]error
	lastSQL  string
	version  string
	pingErr  error
}

func (e *fakeExecutor) Query(ctx context.Context, sql string, args ...any) (Rows, error) {
	e.lastSQL = sql
	if e.errBySQL != nil {
		if err, ok := e.errBySQL[sql]; ok {
			return nil, err
		}
	}
	if e.bySQL != nil {
		if r, ok := e.bySQL[sql]; ok {
			return r, nil
		}
	}
	if e.err != nil {
		return nil, e.err
	}
	if e.rows == nil {
		return nil, nil
	}
	return e.rows, nil
}
func (e *fakeExecutor) Ping(ctx context.Context) error { return e.pingErr }
func (e *fakeExecutor) Version(ctx context.Context) (string, error) {
	return e.version, nil
}
func (e *fakeExecutor) Close() error { return nil }

func cols(names ...string) []Column {
	out := make([]Column, len(names))
	for i, n := range names {
		out[i] = Column{Name: n, Type: "text"}
	}
	return out
}

func TestExecuteEmptyQuery(t *testing.T) {
	ex := &fakeExecutor{}
	qr := Execute(context.Background(), ex, "   \n -- nothing", Limits{})
	if qr.Status != "failed" {
		t.Fatalf("status = %q, want failed", qr.Status)
	}
	if qr.Error == nil || qr.Error.Code != CodeEmptyQuery {
		t.Fatalf("error = %+v, want %s", qr.Error, CodeEmptyQuery)
	}
	if qr.Columns == nil || qr.Rows == nil {
		t.Fatalf("columns/rows must be non-nil empty slices")
	}
	if ex.lastSQL != "" {
		t.Fatalf("executor was called for blank SQL")
	}
}

func TestExecuteSuccess(t *testing.T) {
	ex := &fakeExecutor{rows: newFakeRows(cols("status", "n"), [][]any{{"completed", int64(36)}, {"pending", int64(13)}})}
	qr := Execute(context.Background(), ex, "select 1", Limits{})
	if qr.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", qr.Status)
	}
	if qr.RowCount != 2 || len(qr.Rows) != 2 {
		t.Fatalf("row_count=%d rows=%d, want 2", qr.RowCount, len(qr.Rows))
	}
	if qr.Truncated {
		t.Fatalf("truncated = true, want false")
	}
	if qr.Error != nil {
		t.Fatalf("error = %+v, want nil", qr.Error)
	}
	if qr.Engine != EngineName {
		t.Fatalf("engine = %q, want %q", qr.Engine, EngineName)
	}
	if len(qr.Columns) != 2 || qr.Columns[0].Name != "status" {
		t.Fatalf("columns = %+v", qr.Columns)
	}
}

func TestExecuteRowTruncation(t *testing.T) {
	rows := [][]any{{1}, {2}, {3}, {4}, {5}}
	ex := &fakeExecutor{rows: newFakeRows(cols("n"), rows)}
	qr := Execute(context.Background(), ex, "select n from t", Limits{MaxRows: 2})
	if qr.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded (row cap is a soft cap)", qr.Status)
	}
	if !qr.Truncated {
		t.Fatalf("truncated = false, want true")
	}
	if qr.RowCount != 2 || len(qr.Rows) != 2 {
		t.Fatalf("row_count=%d, want 2", qr.RowCount)
	}
	if qr.Error != nil {
		t.Fatalf("error = %+v, want nil", qr.Error)
	}
}

func TestExecuteByteTruncation(t *testing.T) {
	// json.Marshal([]any{"a"}) == `["a"]` == 5 bytes; second row is much larger.
	rows := [][]any{{"a"}, {"bbbbbbbbbbbbbbbbbbbb"}}
	fr := newFakeRows(cols("v"), rows)
	ex := &fakeExecutor{rows: fr}
	qr := Execute(context.Background(), ex, "select v from t", Limits{MaxBytes: 10})
	if qr.Status != "failed" {
		t.Fatalf("status = %q, want failed", qr.Status)
	}
	if qr.Error == nil || qr.Error.Code != CodeResultLimit {
		t.Fatalf("error = %+v, want %s", qr.Error, CodeResultLimit)
	}
	if !qr.Truncated {
		t.Fatalf("truncated = false, want true")
	}
	if qr.RowCount != 1 {
		t.Fatalf("row_count = %d, want 1 partial row", qr.RowCount)
	}
	if !fr.closed {
		t.Fatalf("rows were not closed on truncation")
	}
}

func TestExecuteErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"deadline", context.DeadlineExceeded, CodeTimeout},
		{"canceled", context.Canceled, CodeTimeout},
		{"syntax", &pgconn.PgError{Code: "42601", Message: "syntax error at or near \"selct\""}, CodeSyntax},
		{"class42 undefined table", &pgconn.PgError{Code: "42P01", Message: "relation \"x\" does not exist"}, CodeSyntax},
		{"runtime", &pgconn.PgError{Code: "22P02", Message: "invalid input syntax for type integer"}, CodeRuntime},
		{"other runtime", &pgconn.PgError{Code: "23505", Message: "duplicate key value"}, CodeRuntime},
		{"environment", errors.Join(ErrEnvironmentUnavailable, errors.New("dial tcp refused")), CodeEnvironmentUnavailable},
		{"generic", errors.New("boom"), CodeRuntime},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ex := &fakeExecutor{err: tc.err}
			qr := Execute(context.Background(), ex, "select 1", Limits{})
			if qr.Status != "failed" {
				t.Fatalf("status = %q, want failed", qr.Status)
			}
			if qr.Error == nil || qr.Error.Code != tc.want {
				t.Fatalf("code = %v, want %s", qr.Error, tc.want)
			}
			if strings.TrimSpace(qr.Error.Message) == "" {
				t.Fatalf("empty message")
			}
		})
	}
}

func TestExecuteRequestIDFromContext(t *testing.T) {
	ex := &fakeExecutor{rows: newFakeRows(cols("n"), [][]any{{1}})}
	ctx := WithRequestID(context.Background(), "rq_abc123")
	qr := Execute(ctx, ex, "select 1", Limits{})
	if qr.RequestID == nil || *qr.RequestID != "rq_abc123" {
		t.Fatalf("request_id = %v, want rq_abc123", qr.RequestID)
	}
}

func TestLimitsMerge(t *testing.T) {
	got := Limits{}.Merge(Limits{MaxRows: 7, MaxBytes: 9, TimeoutMS: 11})
	if got.MaxRows != 7 || got.MaxBytes != 9 || got.TimeoutMS != 11 {
		t.Fatalf("merge defaults failed: %+v", got)
	}
	got = Limits{MaxRows: 3}.Merge(Limits{MaxRows: 7, MaxBytes: 9, TimeoutMS: 11})
	if got.MaxRows != 3 || got.MaxBytes != 9 || got.TimeoutMS != 11 {
		t.Fatalf("merge override failed: %+v", got)
	}
	if DefaultLimits().Timeout() != 5*time.Second {
		t.Fatalf("default timeout != 5s")
	}
}

// A per-request limit may lower a cap but never raise it above the server's:
// otherwise a request body could ask for unbounded rows, bytes or time.
func TestLimitsMergeClampsAboveFallback(t *testing.T) {
	got := Limits{MaxRows: 1_000_000, MaxBytes: 1 << 40, TimeoutMS: 10_000_000_000}.
		Merge(Limits{MaxRows: 7, MaxBytes: 9, TimeoutMS: 11})
	if got.MaxRows != 7 || got.MaxBytes != 9 || got.TimeoutMS != 11 {
		t.Fatalf("merge did not clamp to the fallback ceiling: %+v", got)
	}
}

// NaN and ±Inf have no JSON encoding. PgxExecutor.Values funnels every value
// through normalizeValue, so leaving them as float64 made the whole QueryResult
// unencodable and the HTTP layer emitted a 200 with an empty body.
func TestNormalizeValueNonFiniteFloats(t *testing.T) {
	cases := []struct {
		in   any
		want any
	}{
		{math.NaN(), "NaN"},
		{math.Inf(1), "Infinity"},
		{math.Inf(-1), "-Infinity"},
		{float32(math.NaN()), "NaN"},
		{float32(math.Inf(1)), "Infinity"},
		{1.5, 1.5},
	}
	for _, tc := range cases {
		got := normalizeValue(tc.in)
		if got != tc.want {
			t.Fatalf("normalizeValue(%v) = %#v, want %#v", tc.in, got, tc.want)
		}
		if _, err := json.Marshal([]any{got}); err != nil {
			t.Fatalf("normalized value %#v is not JSON-encodable: %v", got, err)
		}
	}
}

// An executor value that cannot be encoded at all must fail the query loudly
// instead of producing a result the HTTP layer cannot serialize.
func TestExecuteUnencodableValueFails(t *testing.T) {
	ex := &fakeExecutor{rows: newFakeRows(cols("v"), [][]any{{make(chan int)}})}
	qr := Execute(context.Background(), ex, "select v from t", Limits{})
	if qr.Status != "failed" {
		t.Fatalf("status = %q, want failed", qr.Status)
	}
	if qr.Error == nil || qr.Error.Code != CodeRuntime {
		t.Fatalf("error = %+v, want %s", qr.Error, CodeRuntime)
	}
}

func TestUnavailableExecutor(t *testing.T) {
	var ex Executor = UnavailableExecutor{}
	if _, err := ex.Query(context.Background(), "select 1"); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("Query err = %v, want ErrEnvironmentUnavailable", err)
	}
	if err := ex.Ping(context.Background()); !errors.Is(err, ErrEnvironmentUnavailable) {
		t.Fatalf("Ping err = %v, want ErrEnvironmentUnavailable", err)
	}
	qr := Execute(context.Background(), ex, "select 1", Limits{})
	if qr.Error == nil || qr.Error.Code != CodeEnvironmentUnavailable {
		t.Fatalf("code = %v, want %s", qr.Error, CodeEnvironmentUnavailable)
	}
}

func TestNormalizeValue(t *testing.T) {
	var num pgtype.Numeric
	if err := num.Scan("12.50"); err != nil {
		t.Fatalf("scan numeric: %v", err)
	}
	when := time.Date(2026, 1, 4, 10, 12, 0, 0, time.UTC)
	uuid := [16]byte{0x0a, 0x1b, 0x2c, 0x3d, 0x4e, 0x5f, 0x60, 0x71, 0x82, 0x93, 0xa4, 0xb5, 0xc6, 0xd7, 0xe8, 0xf9}

	cases := []struct {
		name string
		in   any
		want any
	}{
		{"nil", nil, nil},
		{"int", int64(7), int64(7)},
		{"float", float64(1.5), float64(1.5)},
		{"bool", true, true},
		{"string", "x", "x"},
		{"bytes", []byte("hi"), "hi"},
		{"time", when, "2026-01-04T10:12:00Z"},
		{"numeric", num, float64(12.5)},
		{"uuid", uuid, "0a1b2c3d-4e5f-6071-8293-a4b5c6d7e8f9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeValue(tc.in); got != tc.want {
				t.Fatalf("normalizeValue(%v) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}
