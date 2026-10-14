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
