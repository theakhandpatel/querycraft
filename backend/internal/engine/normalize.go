package engine

import (
	"database/sql/driver"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// float64Valuer is implemented by pgtype.Numeric (and friends).
type float64Valuer interface {
	Float64Value() (pgtype.Float8, error)
}

// normalizeValue converts pgx/driver values into JSON-friendly Go values so the
// QueryResult serializes predictably: time.Time -> RFC3339 string, []byte ->
// string, pgtype.Numeric -> float64, NULLs -> nil.
func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case float64:
		if s, ok := nonFiniteFloatText(t); ok {
			return s
		}
		return t
	case float32:
		if s, ok := nonFiniteFloatText(float64(t)); ok {
			return s
		}
		return t
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return t
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case []byte:
		return string(t)
	case [16]byte:
		return uuidString(t)
	}
	if fv, ok := v.(float64Valuer); ok {
		if f, err := fv.Float64Value(); err == nil && f.Valid {
			return f.Float64
		}
	}
	if val, ok := v.(driver.Valuer); ok {
		if dv, err := val.Value(); err == nil && dv != nil {
			switch x := dv.(type) {
			case string:
				return x
			case []byte:
				return string(x)
			case int64, float64, bool:
				return x
			}
		}
	}
	return v
}

// nonFiniteFloatText renders NaN and ±Inf the way PostgreSQL prints them.
// encoding/json refuses to encode these floats, so leaving them as float64
// makes the whole QueryResult unencodable and the HTTP layer emits a headers-
// only 200 with an empty body. Returning the PostgreSQL text form keeps the
// value JSON-encodable and lossless.
func nonFiniteFloatText(f float64) (string, bool) {
	switch {
	case math.IsNaN(f):
		return "NaN", true
	case math.IsInf(f, 1):
		return "Infinity", true
	case math.IsInf(f, -1):
		return "-Infinity", true
	}
	return "", false
}

func uuidString(b [16]byte) string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// textOIDs are the PostgreSQL types whose contract value is the engine's own
// text, not a JSON number and not a date re-rendered by the host.
//
// The browser adapter (web/src/api/pglite.ts) returns exactly this text for the
// same columns, and the gateway has to agree: one lesson runs against both, so
// a price that reads "118.00" in the browser must not read 118 over HTTP. Both
// divergences this closes were real and measured:
//
//   - `numeric` used to go through Float64Value, which drops the scale:
//     118.00 arrived as 118 (a value the learner can no longer trust).
//   - a naive `timestamp` used to be decoded into a time value and re-rendered,
//     which moves the wall-clock time by the host's UTC offset (10:30 became
//     05:00 under Asia/Calcutta), because a "timestamp without time zone" has
//     no offset to preserve and any conversion invents one.
//
// Everything else keeps its decoded Go value, because the browser adapter
// produces the same shape: int4/float8 are JSON numbers, bool is a JSON
// boolean, arrays and jsonb keep their structure, int8 stays a string no
// double can hold exactly.
var textOIDs = map[uint32]struct{}{
	1082: {}, // date
	1083: {}, // time
	1114: {}, // timestamp
	1184: {}, // timestamptz
	1186: {}, // interval
	1266: {}, // timetz
	1700: {}, // numeric
}

// normalizeColumn encodes one result column. raw is the value byte for byte as
// the server sent it; for the OIDs above that is the text form, because
// PgxExecutor.Query asks for it (see pgResultFormats). decoded is the value
// pgx parsed, and is only consulted for the other types.
//
// A nil raw value is SQL NULL: it stays nil whatever the OID.
func normalizeColumn(oid uint32, raw []byte, decoded any) any {
	if raw == nil {
		return nil
	}
	if _, ok := textOIDs[oid]; ok {
		return string(raw)
	}
	return normalizeValue(decoded)
}
