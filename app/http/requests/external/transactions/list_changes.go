package transactions

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/pagination"
)

// changesBounds is the window of a changes page: 50 rows by default, at most 200.
var changesBounds = pagination.Bounds{DefaultLimit: 50, MaxLimit: pagination.MaxLimit}

var (
	// ErrInvalidUpdatedSince is an updated_since that is not an RFC 3339 timestamp.
	ErrInvalidUpdatedSince = errors.New("updated_since must be an RFC 3339 timestamp")
	// ErrOffsetWithCursor is a non-zero offset sent with updated_since or cursor.
	ErrOffsetWithCursor = errors.New("offset cannot be combined with updated_since or cursor")
)

// Changes is the query of GET /api/v1/transactions when it reads the changes
// feed: the filters, where the page starts and its size. Times are in UTC.
// AfterUpdatedAt and AfterID are the position a cursor names, zero without one;
// UpdatedSince is zero without updated_since.
type Changes struct {
	Chain          string
	Type           string
	Status         string
	UserID         string
	UpdatedSince   time.Time
	AfterUpdatedAt time.Time
	AfterID        uuid.UUID
	Limit          int
}

// ParseChanges reads the changes feed query of GET /api/v1/transactions. ok is
// false, and nothing is read, when neither updated_since nor cursor is present:
// the request is an offset page. The refusals are 400s the handler maps: an
// updated_since that is not RFC 3339 is ErrInvalidUpdatedSince, a cursor this API
// did not write is pagination.ErrInvalidCursor, an invalid limit or offset is the
// pagination sentinel (a limit above 200 is capped), and a non-zero offset is
// ErrOffsetWithCursor.
func ParseChanges(ctx http.Context) (Changes, bool, error) {
	rawSince := strings.TrimSpace(ctx.Request().Query("updated_since"))
	rawCursor := strings.TrimSpace(ctx.Request().Query("cursor"))
	if rawSince == "" && rawCursor == "" {
		return Changes{}, false, nil
	}

	query := Changes{
		Chain:  ctx.Request().Query("chain"),
		Type:   ctx.Request().Query("type"),
		Status: ctx.Request().Query("status"),
		UserID: ctx.Request().Query("user_id"),
	}
	if rawSince != "" {
		since, err := time.Parse(time.RFC3339, rawSince)
		if err != nil {
			return Changes{}, true, ErrInvalidUpdatedSince
		}
		query.UpdatedSince = since.UTC()
	}
	if rawCursor != "" {
		at, id, err := pagination.DecodeCursor(rawCursor)
		if err != nil {
			return Changes{}, true, err
		}
		query.AfterUpdatedAt, query.AfterID = at, id
	}
	limit, offset, err := pagination.ParseStrict(ctx.Request().Query("limit"), ctx.Request().Query("offset"), changesBounds)
	if err != nil {
		return Changes{}, true, err
	}
	if offset != 0 {
		return Changes{}, true, ErrOffsetWithCursor
	}
	query.Limit = limit
	return query, true, nil
}
