package users

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/models"
)

const accountsSearchMaxLength = 100

var accountsBounds = pagination.Bounds{DefaultLimit: 20, MaxLimit: 100}

var (
	// ErrSearchTooLong is a search longer than 100 characters.
	ErrSearchTooLong = fmt.Errorf("search must be at most %d characters", accountsSearchMaxLength)
	// ErrUnknownEnvironment is an environment other than prod or test.
	ErrUnknownEnvironment = fmt.Errorf("environment must be %q or %q", models.EnvironmentProd, models.EnvironmentTest)
)

// ListAccounts is the query of GET /v1/users/me/accounts: the page window and
// the filter.
type ListAccounts struct {
	Limit       int
	Offset      int
	Search      string
	Environment string
}

// ParseListAccounts reads the query of GET /v1/users/me/accounts. Its refusals
// are 400s, not a form request's 422, so they are errors the handler maps: an
// invalid limit or offset is the pagination sentinel (a limit above 100 is
// capped), a search longer than 100 characters is ErrSearchTooLong and an
// environment other than prod or test is ErrUnknownEnvironment. Search and
// environment are trimmed.
func ParseListAccounts(ctx http.Context) (ListAccounts, error) {
	limit, offset, err := pagination.ParseStrict(ctx.Request().Query("limit"), ctx.Request().Query("offset"), accountsBounds)
	if err != nil {
		return ListAccounts{}, err
	}
	search := strings.TrimSpace(ctx.Request().Query("search"))
	if utf8.RuneCountInString(search) > accountsSearchMaxLength {
		return ListAccounts{}, ErrSearchTooLong
	}
	environment := strings.TrimSpace(ctx.Request().Query("environment"))
	if environment != "" && environment != models.EnvironmentProd && environment != models.EnvironmentTest {
		return ListAccounts{}, ErrUnknownEnvironment
	}
	return ListAccounts{Limit: limit, Offset: offset, Search: search, Environment: environment}, nil
}
