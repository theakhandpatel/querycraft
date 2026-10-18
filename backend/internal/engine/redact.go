// Secret redaction for anything that can reach a log line or an error envelope.
// engine.NewError and the access log call RedactSecrets; pgx redacts the password
// it was handed, and this catches the shapes a proxy, wrapper or hand-built error
// can quote verbatim.
package engine

import "regexp"

// Credentials must not reach a log line or an error envelope. pgx redacts the
// password it was handed, but a proxy, a wrapper, a future driver or a
// hand-built error can quote a DSN verbatim, and both the access log and
// QueryError.detail are reachable surfaces.
//
// This is best-effort by design: it removes the credential shapes a connection
// string can take rather than trying to parse the text. A false positive costs
// a redacted word in a diagnostic message; a false negative leaks a password.
var (
	// scheme://user:password@host
	urlCredential = regexp.MustCompile(`://([^:/?#@\s]+):([^@/?#\s]*)@`)
	// password=... / sslpassword = ... (keyword/value DSNs and libpq strings)
	keyCredential = regexp.MustCompile(`(?i)\b(password|sslpassword)\s*=\s*[^\s]+`)
)

// RedactSecrets replaces credential values in a diagnostic string.
func RedactSecrets(s string) string {
	if s == "" {
		return s
	}
	s = urlCredential.ReplaceAllString(s, "://$1:xxxxx@")
	return keyCredential.ReplaceAllString(s, "$1=xxxxx")
}
