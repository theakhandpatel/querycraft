package eval

import (
	"encoding/json"
	"regexp"
	"strings"
)

var regexFlagPrefix = regexp.MustCompile(`^\(\?[a-zA-Z]+\)`)

// patternString returns the regex source a check carries, from `pattern` or a
// string `test` field.
func (c Check) patternString() (string, bool) {
	if s := strings.TrimSpace(c.Pattern); s != "" {
		return s, true
	}
	if len(c.Test) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(c.Test, &s); err != nil {
		return "", false
	}
	if strings.TrimSpace(s) == "" {
		return "", false
	}
	return s, true
}

// matchPattern runs a check pattern against the SQL text as a RE2 regex. Course
// checks are case-insensitive (the source regexes carry the /i flag), so a
// case-insensitive prefix is used unless the pattern already sets its own flags.
// A pattern that does not compile is treated as "no pattern" by the caller.
func matchPattern(pattern, sql string) (bool, error) {
	re, err := compilePattern(pattern)
	if err != nil {
		return false, err
	}
	return re.MatchString(sql), nil
}

func compilePattern(pattern string) (*regexp.Regexp, error) {
	src := extractRegexSource(pattern)
	if !regexFlagPrefix.MatchString(src) {
		if re, err := regexp.Compile("(?i)" + src); err == nil {
			return re, nil
		}
	}
	return regexp.Compile(src)
}

// extractRegexSource pulls the source out of a JavaScript regex literal or a
// function body such as `function(s){ return /foo\s+bar/i.test(s); }`.
func extractRegexSource(pattern string) string {
	s := strings.TrimSpace(pattern)
	if i := strings.Index(s, "/"); i >= 0 {
		if j := strings.LastIndex(s, "/"); j > i {
			if cand := s[i+1 : j]; strings.TrimSpace(cand) != "" {
				return cand
			}
		}
	}
	return s
}
