package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// Execute runs one statement through ex, enforcing the row, byte and time caps
// and mapping engine failures to the stable error codes.
//
// Truncation policy: hitting the row cap yields a successful, truncated result
// (the first rows are useful and the caller is told via truncated:true). Hitting
// the serialized-byte cap is a hard failure coded RESULT_LIMIT_EXCEEDED, because
// the whole result cannot be represented within the budget; partial rows are
// still returned alongside the error.
func Execute(ctx context.Context, ex Executor, sql string, limits Limits) QueryResult {
	/* Fill, not Merge: the caller has already resolved request-vs-server with
	   Merge (where the server's configured caps are the ceiling). Re-merging the
	   compiled DefaultLimits() here clamped DOWN, so a gateway configured above
	   the default (QC_MAX_ROWS=2000) silently ran at the default 500. */
	limits = limits.Fill(DefaultLimits())
	requestID := RequestIDFromContext(ctx)
	if isBlankSQL(sql) {
		res := Failing(CodeEmptyQuery, "Blank SQL. Write a statement, then Run.", "", requestID)
		/* Both the dialect AND the engine are properties of the engine that
		   would have run the statement, so even a refusal names them: the UI and
		   the contract must never show a PostgreSQL result on a SQLite chapter.
		   Dialect alone was fixed before; the engine field was left hardcoded to
		   "postgresql" by Failing, so the pair disagreed in the JSON. */
		res.Dialect = DialectOf(ex)
		res.Engine = EngineLabelOf(ex)
		return res
	}

	start := time.Now()
	qctx, cancel := context.WithTimeout(ctx, limits.Timeout())
	defer cancel()

	res := QueryResult{
		Status:    "succeeded",
		Columns:   []Column{},
		Rows:      [][]any{},
		Engine:    EngineLabelOf(ex),
		RequestID: requestID,
		Dialect:   DialectOf(ex),
	}

	rows, err := ex.Query(qctx, sql)
	if err != nil {
		return res.failed(classifyFor(qctx, ex, err), start)
	}
	if rows == nil {
		res.DurationMS = RoundMS(time.Since(start))
		return res
	}
	defer rows.Close()

	res.Columns = rows.Columns()
	if res.Columns == nil {
		res.Columns = []Column{}
	}

	byteCount := 0
	rowCapped, byteCapped := false, false
	for rows.Next() {
		if len(res.Rows) >= limits.MaxRows {
			rowCapped = true
			break
		}
		vals, verr := rows.Values()
		if verr != nil {
			return res.failed(classifyFor(qctx, ex, verr), start)
		}
		if vals == nil {
			vals = []any{}
		}
		b, merr := json.Marshal(vals)
		if merr != nil {
			// Never let an unencodable value reach the HTTP layer: encoding is
			// best-effort there too, and a failure would emit an empty body.
			return res.failed(NewError(CodeRuntime,
				"The result contains a value that cannot be encoded as JSON.",
				merr.Error()), start)
		}
		if byteCount+len(b) > limits.MaxBytes {
			byteCapped = true
			break
		}
		byteCount += len(b)
		res.Rows = append(res.Rows, vals)
	}
	if !rowCapped && !byteCapped {
		if rerr := rows.Err(); rerr != nil {
			return res.failed(classifyFor(qctx, ex, rerr), start)
		}
	} else {
		// A cap was hit, so the rest of the result is unwanted. Cancelling the
		// query now makes the server abort it; without this, closing a partially
		// read result makes the driver drain every remaining row to leave the
		// connection usable, and a capped read of `generate_series(1, 1e9)` then
		// spends the whole deadline discarding rows nobody asked for (measured:
		// 4003 ms to return 300 rows). The cancellation is also why the pool
		// connection is free immediately instead of at the timeout.
		cancel()
	}

	res.Truncated = rowCapped || byteCapped
	if byteCapped {
		res.Status = "failed"
		res.Error = NewError(
			CodeResultLimit,
			fmt.Sprintf("result exceeded the %d-byte cap; returning the first %d row(s)", limits.MaxBytes, len(res.Rows)),
			"",
		)
	}
	res.RowCount = len(res.Rows)
	res.DurationMS = RoundMS(time.Since(start))
	return res
}

// isBlankSQL reports whether the statement is empty once whitespace and SQL
// comments are removed. Comment-only submissions are EMPTY_QUERY, matching the
// browsers' stripComments (web/src/api/pglite.ts), which strips BOTH line and
// block comments. This used to strip only `--`, so `/* nothing */` was
// EMPTY_QUERY in the browser and a syntax error over HTTP — an adapter-parity
// break in the one place the repo relies on the two agreeing.
func isBlankSQL(sql string) bool {
	var b strings.Builder
	inSingle, inDouble, inBlock := false, false, false
	for i := 0; i < len(sql); i++ {
		c := sql[i]
		switch {
		case inBlock:
			if c == '*' && i+1 < len(sql) && sql[i+1] == '/' {
				inBlock = false
				i++
			}
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
		case c == '\'':
			inSingle = true
			b.WriteByte(c)
		case c == '"':
			inDouble = true
			b.WriteByte(c)
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-':
			for i < len(sql) && sql[i] != '\n' {
				i++
			}
			if i < len(sql) {
				b.WriteByte('\n')
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			inBlock = true
			i++
		default:
			b.WriteByte(c)
		}
	}
	return strings.TrimSpace(b.String()) == ""
}

func (res QueryResult) failed(qerr *QueryError, start time.Time) QueryResult {
	res.Status = "failed"
	res.Error = qerr
	res.Truncated = false
	res.RowCount = len(res.Rows)
	res.DurationMS = RoundMS(time.Since(start))
	return res
}

// classify maps an engine error onto the contract's closed error-code set
// (design §4.2). The table is shared, code by code, with the browser adapter
// (web/src/api/pglite.ts, mapError): one lesson runs against both adapters, and
// the learner must not see the same mistake described two different ways.
//
// The distinction that carries the most weight is syntax versus runtime.
// QUERY_SYNTAX_ERROR means the *parser* rejected the statement, so "look at
// your SQL" is the only useful advice. Everything the engine rejects after
// parsing is a runtime error, and that explicitly includes SQLSTATE class 42's
// "undefined table" and "undefined column": those are errors about a *name*
// the learner can see in the tables drawer, not about the shape of their SQL.
// Class 42's name ("syntax error or access rule violation") is a historical
// label, not a classification, which is why 42501 (permission denied) is also
// a runtime error here.
//
// Deadline misses and server-side cancellations map to QUERY_TIMEOUT, and
// connectivity problems map to ENVIRONMENT_UNAVAILABLE.
func classify(ctx context.Context, err error) *QueryError {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPoolTimeout) {
		return NewError(CodeTimeout, "The gateway was busy; no execution connection became available within the time limit.", "")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return NewError(CodeTimeout, "Query exceeded the time limit and was stopped.", "")
	}
	// The context's own state is consulted too: the deadline this process
	// imposed is the fact, and a driver may return its own error shape rather
	// than wrapping context.DeadlineExceeded. Without this the parameter was
	// accepted and ignored — a deadline miss that arrived in another dialect
	// was reported as a runtime error.
	if ctx != nil && ctx.Err() != nil {
		return NewError(CodeTimeout, "Query exceeded the time limit and was stopped.", "")
	}
	if errors.Is(err, ErrEnvironmentUnavailable) {
		return NewError(CodeEnvironmentUnavailable, "The execution environment is unavailable.", err.Error())
	}
	if code, ok := pgCodeFor(err); ok {
		return engineWordsFor(code, err)
	}
	return engineWordsFor(CodeRuntime, err)
}

// classifyFor asks the executor to classify its own engine's error before the
// shared table does, so a dialect with its own vocabulary (SQLite's message
// rules) is not described in PostgreSQL's terms. The host's own facts are
// decided first and are not the executor's to rename: a deadline, a busy
// gateway and an unreachable environment mean the same thing in every dialect,
// and SQLite's profile would otherwise be asked to recognise this process's
// sentinels.
func classifyFor(ctx context.Context, ex Executor, err error) *QueryError {
	if err == nil {
		return nil
	}
	if errors.Is(err, ErrPoolTimeout) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) || errors.Is(err, ErrEnvironmentUnavailable) {
		return classify(ctx, err)
	}
	if classifier, ok := ex.(ErrorClassifier); ok {
		if code, ok := classifier.ClassifyError(err); ok && strings.TrimSpace(code) != "" {
			return engineWordsFor(code, err)
		}
	}
	return classify(ctx, err)
}

// pgCodeFor maps a PostgreSQL error onto the contract's closed code set. It is
// separate from classify because PgxExecutor answers ErrorClassifier with it:
// the same rules must serve both the executor's own hook and the fallback, or a
// result could be classified one way in one path and another way in the other.
func pgCodeFor(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "42601", "42603", "42604":
			// The parser rejected the statement and reported where.
			return CodeSyntax, true
		case "57014", "55P03":
			return CodeTimeout, true
		case "54000", "53100", "53400":
			// The server stopped itself before producing a result: out of
			// memory, disk or configured limits. The contract calls this
			// RESULT_LIMIT_EXCEEDED, and the server's own words are the detail.
			return CodeResultLimit, true
		case "53300", "3D000", "08006", "08003":
			return CodeEnvironmentUnavailable, true
		default:
			return CodeRuntime, true
		}
	}
	return "", false
}

// engineWordsFor builds the QueryError for an already-decided code, taking the
// engine's own words from the error so the learner reads the engine's message
// and not a paraphrase. A PostgreSQL error also carries a detail/hint field
// that a driver message does not, which is why the two shapes differ.
func engineWordsFor(code string, err error) *QueryError {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		detail := pgErr.Detail
		if strings.TrimSpace(detail) == "" {
			detail = pgErr.Hint
		}
		// The server's "canceling statement due to statement timeout" and this
		// sentence say the same thing; the sentence is the one the lesson copy
		// was written against, so a canceled statement keeps it.
		if code == CodeTimeout {
			return NewError(code, "Query was canceled, likely because it exceeded the time limit.", detail)
		}
		return NewError(code, pgErr.Message, detail)
	}
	msg := err.Error()
	if strings.TrimSpace(msg) == "" {
		msg = "query failed"
	}
	return NewError(code, msg, "")
}
