package transactions

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/http/pagination"
)

func parseChanges(t *testing.T, query string) (Changes, bool, error) {
	t.Helper()
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(nethttp.MethodGet, "/api/v1/transactions?"+query, nil)
	return ParseChanges(ginpkg.NewContext(ginCtx))
}

func TestParse_Changes_LeavesAnOffsetPageAlone(t *testing.T) {
	for _, query := range []string{"", "limit=5&offset=10", "chain=eth&type=deposit&status=pending&user_id=u1", "limit=abc&offset=-1", "updated_since=&cursor="} {
		t.Run(query, func(t *testing.T) {
			got, ok, err := parseChanges(t, query)
			if ok || err != nil || got != (Changes{}) {
				t.Fatalf("got %+v, %v, %v; want an offset page, nothing read", got, ok, err)
			}
		})
	}
}

func TestParse_Changes_ReadsTheFeedQuery(t *testing.T) {
	at := time.Date(2026, 10, 11, 12, 30, 15, 0, time.UTC)
	id := uuid.MustParse("7d3c9f3e-2b1a-4c5d-8e6f-0a1b2c3d4e5f")
	cursor := pagination.EncodeCursor(at, id)
	cases := map[string]Changes{
		"updated_since=2026-10-11T12:30:15Z":                            {UpdatedSince: at, Limit: 50},
		"updated_since=" + url.QueryEscape("2026-10-11T09:30:15-03:00"): {UpdatedSince: at, Limit: 50},
		"updated_since=2026-10-11T12:30:15.25Z&limit=7":                 {UpdatedSince: at.Add(250 * time.Millisecond), Limit: 7},
		"cursor=" + cursor + "&limit=500":                               {AfterUpdatedAt: at, AfterID: id, Limit: 200},
		"cursor=" + cursor + "&updated_since=2026-10-01T00:00:00Z&offset=0&chain=eth&type=deposit&status=confirmed&user_id=alice": {
			Chain: "eth", Type: "deposit", Status: "confirmed", UserID: "alice",
			UpdatedSince: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), AfterUpdatedAt: at, AfterID: id, Limit: 50,
		},
	}
	for query, want := range cases {
		t.Run(query, func(t *testing.T) {
			got, ok, err := parseChanges(t, query)
			if !ok || err != nil {
				t.Fatalf("ok = %v, err = %v; want the feed query", ok, err)
			}
			if !got.UpdatedSince.Equal(want.UpdatedSince) || !got.AfterUpdatedAt.Equal(want.AfterUpdatedAt) {
				t.Fatalf("times = (%s, %s), want (%s, %s)", got.UpdatedSince, got.AfterUpdatedAt, want.UpdatedSince, want.AfterUpdatedAt)
			}
			if got.UpdatedSince.Location() != time.UTC {
				t.Fatalf("updated_since is in %s, want UTC", got.UpdatedSince.Location())
			}
			got.UpdatedSince, got.AfterUpdatedAt = want.UpdatedSince, want.AfterUpdatedAt
			if got != want {
				t.Fatalf("got %+v, want %+v", got, want)
			}
		})
	}
}

func TestParse_Changes_NamesWhatIsWrong(t *testing.T) {
	cursor := pagination.EncodeCursor(time.Now(), uuid.New())
	cases := map[string]error{
		"updated_since=yesterday":                     ErrInvalidUpdatedSince,
		"updated_since=2026-10-11":                    ErrInvalidUpdatedSince,
		"updated_since=2026-10-11T12:30:15":           ErrInvalidUpdatedSince,
		"updated_since=2026-10-11%2012:30:15":         ErrInvalidUpdatedSince,
		"updated_since=2026-10-11T12:30:15+00:00":     ErrInvalidUpdatedSince, // an unescaped + reads as a space
		"updated_since=1760185815":                    ErrInvalidUpdatedSince,
		"cursor=not-a-cursor":                         pagination.ErrInvalidCursor,
		"cursor=" + cursor + "&updated_since=never":   ErrInvalidUpdatedSince,
		"updated_since=2026-10-11T12:30:15Z&limit=0":  pagination.ErrInvalidLimit,
		"cursor=" + cursor + "&limit=many":            pagination.ErrInvalidLimit,
		"cursor=" + cursor + "&offset=-1":             pagination.ErrInvalidOffset,
		"cursor=" + cursor + "&offset=50":             ErrOffsetWithCursor,
		"updated_since=2026-10-11T12:30:15Z&offset=1": ErrOffsetWithCursor,
	}
	for query, want := range cases {
		t.Run(query, func(t *testing.T) {
			if _, _, err := parseChanges(t, query); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
		})
	}
	if ErrInvalidUpdatedSince.Error() != "updated_since must be an RFC 3339 timestamp" ||
		ErrOffsetWithCursor.Error() != "offset cannot be combined with updated_since or cursor" {
		t.Fatal("the refusal sentences changed")
	}
}
