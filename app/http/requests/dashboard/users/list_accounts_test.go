package users

import (
	"errors"
	nethttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	ginpkg "github.com/goravel/gin"

	"github.com/macrowallets/waas/app/http/pagination"
)

func parseQuery(t *testing.T, query string) (ListAccounts, error) {
	t.Helper()
	ginCtx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ginCtx.Request = httptest.NewRequest(nethttp.MethodGet, "/v1/users/me/accounts?"+query, nil)
	return ParseListAccounts(ginpkg.NewContext(ginCtx))
}

func TestParse_List_AccountsReadsTheWindowAndTheFilter(t *testing.T) {
	cases := map[string]ListAccounts{
		"":                  {Limit: 20},
		"limit=5&offset=10": {Limit: 5, Offset: 10},
		"limit=500":         {Limit: 100},
		"search=%20acme%20&environment=%20test%20": {Limit: 20, Search: "acme", Environment: "test"},
		"environment=prod":                         {Limit: 20, Environment: "prod"},
	}
	for query, want := range cases {
		t.Run(query, func(t *testing.T) {
			got, err := parseQuery(t, query)
			if err != nil || got != want {
				t.Fatalf("got %+v, %v; want %+v", got, err, want)
			}
		})
	}
}

func TestParse_List_AccountsNamesWhatIsWrong(t *testing.T) {
	cases := map[string]error{
		"limit=0":                            pagination.ErrInvalidLimit,
		"limit=abc":                          pagination.ErrInvalidLimit,
		"offset=-1":                          pagination.ErrInvalidOffset,
		"search=" + strings.Repeat("a", 101): ErrSearchTooLong,
		"environment=staging":                ErrUnknownEnvironment,
		"limit=0&environment=x":              pagination.ErrInvalidLimit,
		"search=" + strings.Repeat("é", 101) + "&environment=x": ErrSearchTooLong,
	}
	for query, want := range cases {
		t.Run(query, func(t *testing.T) {
			if _, err := parseQuery(t, query); !errors.Is(err, want) {
				t.Fatalf("err = %v, want %v", err, want)
			}
		})
	}
	if _, err := parseQuery(t, "search="+strings.Repeat("é", 100)); err != nil {
		t.Fatalf("100 characters are refused: %v", err)
	}
	if ErrSearchTooLong.Error() != "search must be at most 100 characters" || ErrUnknownEnvironment.Error() != `environment must be "prod" or "test"` {
		t.Fatal("the refusal sentences changed")
	}
}
