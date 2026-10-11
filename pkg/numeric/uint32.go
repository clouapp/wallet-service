package numeric

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

// ParseUint32 reads val as an unsigned 32-bit integer; ok is false for anything else,
// including fractions, signs, exponents and surrounding spaces.
func ParseUint32(val any) (uint32, bool) {
	switch v := val.(type) {
	case float64:
		if v < 0 || v > math.MaxUint32 || v != math.Trunc(v) {
			return 0, false
		}
		return uint32(v), true
	case json.Number:
		return parseUint32Text(v.String())
	case string:
		return parseUint32Text(v)
	case int:
		return parseUint32Text(strconv.Itoa(v))
	case int64:
		return parseUint32Text(strconv.FormatInt(v, 10))
	case uint32:
		return v, true
	case uint64:
		return parseUint32Text(strconv.FormatUint(v, 10))
	default:
		return 0, false
	}
}

func parseUint32Text(text string) (uint32, bool) {
	if text == "" || strings.TrimSpace(text) != text || strings.HasPrefix(text, "+") {
		return 0, false
	}
	parsed, err := strconv.ParseUint(text, 10, 32)
	if err != nil || parsed > math.MaxUint32 {
		return 0, false
	}
	return uint32(parsed), true
}
