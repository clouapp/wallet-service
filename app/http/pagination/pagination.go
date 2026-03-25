package pagination

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"
)

const MaxLimit = 200

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

func Response(data any, total int64, limit, offset int) http.Json {
	return http.Json{
		"data":   data,
		"total":  total,
		"limit":  limit,
		"offset": offset,
	}
}
