package guard

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

const password = "correct-horse-battery-staple"

// PlatformGuardSuite proves the /v1/platform group refuses a signed-in
// non-admin on every route, before any handler looks the target up.
type PlatformGuardSuite struct {
	support.HTTPSuite
}

func TestPlatform_Guard_Suite(t *testing.T) {
	support.RunSuite(t, new(PlatformGuardSuite))
}

func (s *PlatformGuardSuite) SetupTest() {
	testutil.SeededTestDB(s.T())
}

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

// placeholderID fills every path parameter with a valid UUID that matches no row.
const placeholderID = "00000000-0000-4000-8000-000000000000"

func platformRoutes() []string {
	var routes []string
	for _, info := range facades.Route().GetRoutes() {
		if !strings.HasPrefix(info.Path, "/v1/platform/") {
			continue
		}
		method, _, _ := strings.Cut(info.Method, "|")
		routes = append(routes, method+" "+info.Path)
	}
	sort.Strings(routes)
	return routes
}

// call sends one request through the booted router and returns status and body.
func (s *PlatformGuardSuite) call(method, path, bearer, body string) (int, string) {
	s.T().Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	response, err := facades.Route().Test(request)
	s.Require().NoError(err)
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(response.Body)
	s.Require().NoError(err)
	return response.StatusCode, string(raw)
}

// signIn creates a user that is not a platform admin and returns its access token.
func (s *PlatformGuardSuite) signIn() string {
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(password)
	s.Require().NoError(err)
	id := uuid.New()
	email := "member-" + id.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, 'active', NOW(), NOW())`, id, email, hash)
	s.Require().NoError(err)
	status, raw := s.call(http.MethodPost, "/v1/auth/login", "", fmt.Sprintf(`{"email":%q,"password":%q}`, email, password))
	s.Require().Equal(http.StatusOK, status, raw)
	var body struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(raw), &body))
	return body.AccessToken
}

// refusals is the 403 sentence each /v1/platform route gives a signed-in
// caller who is not a platform admin. Every route has a row, so a new platform
// route fails TestA_NonAdmin_IsRefusedOnEveryPlatformRoute until it gets one.
// The sentences are the ones the services have always returned.
var refusals = map[string]string{
	"DELETE /v1/platform/users/{id}/mfa":                     "you do not have permission to reset user mfa",
	"GET /v1/platform/accounts":                              "you do not have permission to view accounts",
	"GET /v1/platform/accounts/{accountId}/settings/{group}": "you do not have permission to view settings",
	"GET /v1/platform/accounts/{accountId}/users":            "you do not have permission to view account users",
	"GET /v1/platform/activity":                              "you do not have permission to view platform activity",
	"GET /v1/platform/features":                              "you do not have permission to manage platform features",
	"GET /v1/platform/features/{scope}/{id}":                 "you do not have permission to manage platform features",
	"GET /v1/platform/settings":                              "you do not have permission to view settings",
	"GET /v1/platform/settings/{group}":                      "you do not have permission to view settings",
	"GET /v1/platform/users":                                 "you do not have permission to view users",
	"PATCH /v1/platform/chains/{chainId}":                    "you do not have permission to update chains",
	"PATCH /v1/platform/chains/{chainId}/rpc":                "you do not have permission to update chains",
	"PATCH /v1/platform/features/{key}":                      "you do not have permission to manage platform features",
	"POST /v1/platform/accounts/{accountId}/archive":         "you do not have permission to change account status",
	"POST /v1/platform/accounts/{accountId}/freeze":          "you do not have permission to change account status",
	"POST /v1/platform/accounts/{accountId}/owners":          "you do not have permission to attach an account owner",
	"POST /v1/platform/accounts/{accountId}/unfreeze":        "you do not have permission to change account status",
	"POST /v1/platform/settings/mail/test":                   "you do not have permission to update settings",
	"POST /v1/platform/settings/sections/{section}/cache":    "you do not have permission to update settings",
	"POST /v1/platform/settings/sections/{section}/reset":    "you do not have permission to update settings",
	"POST /v1/platform/users/{id}/reactivate":                "you do not have permission to suspend users",
	"POST /v1/platform/users/{id}/sessions/revoke":           "you do not have permission to revoke user sessions",
	"POST /v1/platform/users/{id}/suspend":                   "you do not have permission to suspend users",
	"PUT /v1/platform/accounts/{accountId}/settings/{group}": "you do not have permission to update settings",
	"PUT /v1/platform/features/{scope}/{id}":                 "you do not have permission to manage platform features",
	"PUT /v1/platform/features/{scope}/{id}/{feature}":       "you do not have permission to manage platform features",
	"PUT /v1/platform/settings/{group}":                      "you do not have permission to update settings",
}

func (s *PlatformGuardSuite) TestA_NonAdmin_IsRefusedOnEveryPlatformRoute() {
	token := s.signIn()
	served := platformRoutes()
	s.Require().NotEmpty(served)
	for _, route := range served {
		want, listed := refusals[route]
		s.Require().True(listed, "%s has no row in refusals", route)

		method, path, _ := strings.Cut(route, " ")
		status, raw := s.call(method, pathParameter.ReplaceAllString(path, placeholderID), token, "{}")
		s.Equal(http.StatusForbidden, status, "%s: %s", route, raw)
		var body struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		s.Require().NoError(json.Unmarshal([]byte(raw), &body), "%s: %s", route, raw)
		s.Equal("forbidden", body.Error.Code, route)
		s.Equal(want, body.Error.Message, route)
	}
	for route := range refusals {
		s.Contains(served, route, "refusals lists a route the router does not serve")
	}
}
