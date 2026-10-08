package pagination

import (
	"errors"
	"strconv"
	"strings"

	"github.com/goravel/framework/contracts/http"
)

const MaxLimit = 200

var (
	ErrInvalidLimit  = errors.New("limit must be a positive integer")
	ErrInvalidOffset = errors.New("offset must be a non-negative integer")
	ErrInvalidBounds = errors.New("pagination bounds require 0 < default limit <= max limit")
)

// Bounds configures ParseStrict for one endpoint.
type Bounds struct {
	DefaultLimit int
	MaxLimit     int
}

func ParseParams(ctx http.Context, defaultLimit int) (limit, offset int) {
	limit, _ = strconv.Atoi(ctx.Request().Query("limit", strconv.Itoa(defaultLimit)))
	offset, _ = strconv.Atoi(ctx.Request().Query("offset", "0"))

	if limit <= 0 || limit > MaxLimit {
		limit = defaultLimit
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// ParseStrict validates raw limit/offset query values instead of silently
// replacing bad input: empty values take the defaults, a limit above the max
// is capped, and anything non-numeric or out of range is an error. An offset
// past the last row is valid and yields an empty page.
func ParseStrict(rawLimit, rawOffset string, bounds Bounds) (limit, offset int, err error) {
	if bounds.DefaultLimit <= 0 || bounds.MaxLimit < bounds.DefaultLimit {
		return 0, 0, ErrInvalidBounds
	}

	limit = bounds.DefaultLimit
	if trimmed := strings.TrimSpace(rawLimit); trimmed != "" {
		parsed, parseErr := strconv.Atoi(trimmed)
		if parseErr != nil || parsed <= 0 {
			return 0, 0, ErrInvalidLimit
		}
		limit = min(parsed, bounds.MaxLimit)
	}

	if trimmed := strings.TrimSpace(rawOffset); trimmed != "" {
		parsed, parseErr := strconv.Atoi(trimmed)
		if parseErr != nil || parsed < 0 {
			return 0, 0, ErrInvalidOffset
		}
		offset = parsed
	}
	return limit, offset, nil
}

func Response(data any, total int64, limit, offset int) http.Json {
	return http.Json{
		"data":   data,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}
}
