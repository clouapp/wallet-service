package middleware

import (
	contractshttp "github.com/goravel/framework/contracts/http"
)

// scanRequest walks every string value in the request headers, query params,
// and parsed body, returning true on the first match. The matcher receives
// already-lowercased input so callers can use lowercase needles directly.
func scanRequest(ctx contractshttp.Context, matcher func(lowered string) bool) bool {
	for _, values := range ctx.Request().Headers() {
		for _, v := range values {
			if matcher(toLower(v)) {
				return true
			}
		}
	}

	for _, v := range ctx.Request().Queries() {
		if matcher(toLower(v)) {
			return true
		}
	}

	return scanValue(ctx.Request().All(), matcher)
}

func scanValue(v any, matcher func(string) bool) bool {
	switch t := v.(type) {
	case string:
		return matcher(toLower(t))
	case []any:
		for _, item := range t {
			if scanValue(item, matcher) {
				return true
			}
		}
	case map[string]any:
		for _, item := range t {
			if scanValue(item, matcher) {
				return true
			}
		}
	}
	return false
}

// toLower is a tiny ASCII-only fast path; payloads we care about are ASCII.
func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}
