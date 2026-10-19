package dialect

import (
	_ "embed"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

/* sqlite.go is the embedded SQLite profile plus the value and error encoders the
 * profile drives. dialect.go holds the profile types and the affinity algorithm;
 * this file holds the profile bytes and the code that reads them, so the
 * encoders follow the contract file rather than a second copy of the rules.
 */

// sqliteProfileJSON is the SQLite dialect profile.
//
// contracts/dialects/sqlite.json is the source of truth. A go:embed pattern
// cannot contain "..", so the Go side embeds a byte-identical mirror and
// TestEmbeddedProfileMatchesContract fails if the two ever differ. The same
// arrangement keeps web/src/api/dataset.ts honest for the dataset SQL.
//
//go:embed sqlite.json
var sqliteProfileJSON []byte

// sqliteProfile is the decoded embedded profile. A profile that does not decode
// is a build-time bug, not a runtime condition, so this panics rather than
// handing every caller an error it cannot act on.
var sqliteProfile = mustDecode(sqliteProfileJSON)

// mustDecode decodes the embedded profile and checks the keys EncodeValue reads
// verbatim. A profile that omitted one would make the encoder invent an answer,
// so a missing key is a fault caught here once rather than defaulted at each
// call.
func mustDecode(raw []byte) Profile {
	p, err := Decode(raw)
	if err != nil {
		panic("dialect: embedded SQLite profile is invalid: " + err.Error())
	}
	if p.ValueEncoding.Integer.MaxSafeInteger <= 0 {
		panic("dialect: embedded SQLite profile has no integer.max_safe_integer")
	}
	nonFinite := p.ValueEncoding.Real.NonFinite
	if nonFinite.NaN == "" || nonFinite.PositiveInfinity == "" || nonFinite.NegativeInfinity == "" {
		panic("dialect: embedded SQLite profile has an incomplete real.non_finite rule")
	}
	if p.ValueEncoding.Blob.Prefix == "" {
		panic("dialect: embedded SQLite profile has no blob.prefix")
	}
	return p
}

// SQLite returns the embedded SQLite profile.
func SQLite() Profile { return sqliteProfile }

// ClassifyError returns the closed QueryResult error code for err by matching
// the profile's message rules in order (first match wins; the profile's last
// rule is the catch-all).
//
// SQLite has no SQLSTATE. The engine reports a primary result code such as
// SQLITE_CONSTRAINT(19) plus a message, and the message is what distinguishes a
// syntax error from a missing table, so classification is by message pattern and
// every pattern has a case in dialect_test.go. A nil error has no code: it
// returns "".
//
// The rules mean what they say only for the engine's sentence. Four of them —
// the equals rules for "incomplete input", "database is locked" and
// "interrupted", and the prefix rule for "unrecognized token:" — cannot match a
// framed message at all, so a caller whose driver adds framing must strip it
// first (web/src/api/sqliteFraming.ts; the Go adapter reads the raw sentence).
// TestClassifyErrorExpectsTheEnginesSentence records the boundary.
func (p Profile) ClassifyError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	for _, rule := range p.ErrorClassification {
		if rule.matches(msg) {
			return rule.Code
		}
	}
	return CodeRuntimeError
}

// matches reports whether a rule's message test holds. The match kinds are the
// profile's closed set (prefix, contains, equals); TestErrorClassificationShape
// makes the default branch unreachable by failing on any other value.
func (r MatchRule) matches(msg string) bool {
	switch r.Match {
	case "equals":
		return msg == r.Pattern
	case "prefix":
		return strings.HasPrefix(msg, r.Pattern)
	default:
		return strings.Contains(msg, r.Pattern)
	}
}

// EncodeValue renders one value the engine handed back as the JSON value a
// QueryResult row carries.
//
// declaredAffinity is the affinity class of the column the value came from. It
// is accepted deliberately, and deliberately not applied: SQLite applies
// affinity when a value is *written*, not when it is read, so by the time a
// driver returns a value its storage class is already fixed. Re-applying the
// declared affinity would report a value the engine never stored. A TEXT value
// in a NUMERIC-affinity column therefore stays a string, which is the dynamic
// typing the dialect must teach (see TestEncodeValueIgnoresAffinity, which runs
// every class). The parameter is there so the mismatch is visible at the call
// site; the class is read from the value.
func (p Profile) EncodeValue(raw any, declaredAffinity string) any {
	switch v := raw.(type) {
	case nil:
		return nil
	case int64:
		return p.encodeInt(v)
	case float64:
		return p.encodeFloat(v)
	case string:
		return v
	case []byte:
		return p.encodeBlob(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// encodeInt reports an integer as a JSON number, or as a decimal string when it
// is outside the range a JSON number can hold exactly. That is the same rule
// web/src/api/pglite.ts already applies to PostgreSQL bigint.
func (p Profile) encodeInt(v int64) any {
	if max := p.ValueEncoding.Integer.MaxSafeInteger; v > max || v < -max {
		return strconv.FormatInt(v, 10)
	}
	return v
}

// encodeFloat reports a REAL as a JSON number, with the profile's named rule for
// the three values JSON cannot hold. SQLite stores NaN as NULL, so the profile's
// NaN rule is the JSON null — the engine's own answer; the infinities are
// strings, because a null would hide a value that exists and a number would be
// wrong.
func (p Profile) encodeFloat(v float64) any {
	nonFinite := p.ValueEncoding.Real.NonFinite
	switch {
	case math.IsNaN(v):
		if nonFinite.NaN == "null" {
			return nil
		}
		return nonFinite.NaN
	case math.IsInf(v, 1):
		return nonFinite.PositiveInfinity
	case math.IsInf(v, -1):
		return nonFinite.NegativeInfinity
	}
	return v
}

// encodeBlob reports binary as the profile's prefix plus lowercase hex, the
// form PostgreSQL bytea already uses, so both dialects agree.
func (p Profile) encodeBlob(b []byte) string {
	return p.ValueEncoding.Blob.Prefix + hex.EncodeToString(b)
}
