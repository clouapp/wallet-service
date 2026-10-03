package routesecurity_test

import (
	"fmt"
	nethttp "net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/tests/architecture"
	"github.com/macrowallets/waas/tests/testenv"
)

// guard is the authentication a route runs behind. Permissions inside a
// surface are not recorded here: they are a pending product decision
// (alignment plan §0.5, .ai/guidelines/authorization.md).
type guard string

const (
	guardPublic            guard = "public"             // health and API docs
	guardGuest             guard = "guest"              // the auth endpoints that create a session
	guardProviderSignature guard = "provider-signature" // inbound chain-provider webhooks
	guardSession           guard = "session"            // middleware.SessionAuth (dashboard, /v1)
	guardAPIToken          guard = "api-token"          // middleware.APITokenAuth (external, /api/v1)
)

// routeTable is the closed list of every route the router serves, keyed by
// Goravel's "METHOD /path", with the guard it runs behind. A new route fails
// TestEveryRouteIsInTheRouteTable until it gets a row here.
var routeTable = map[string]guard{
	"GET|HEAD /api/v1/addresses/{address}":                             guardAPIToken,
	"GET|HEAD /api/v1/chains":                                          guardAPIToken,
	"GET|HEAD /api/v1/transactions":                                    guardAPIToken,
	"GET|HEAD /api/v1/transactions/{id}":                               guardAPIToken,
	"GET|HEAD /api/v1/users/{external_id}/addresses":                   guardAPIToken,
	"GET|HEAD /api/v1/users/{external_id}/transactions":                guardAPIToken,
	"GET|HEAD /api/v1/wallets":                                         guardAPIToken,
	"POST /api/v1/wallets":                                             guardAPIToken,
	"GET|HEAD /api/v1/wallets/{walletId}":                              guardAPIToken,
	"GET|HEAD /api/v1/wallets/{walletId}/addresses":                    guardAPIToken,
	"POST /api/v1/wallets/{walletId}/addresses":                        guardAPIToken,
	"PATCH /api/v1/wallets/{walletId}/addresses/{addressId}":           guardAPIToken,
	"POST /api/v1/wallets/{walletId}/consolidate":                      guardAPIToken,
	"POST /api/v1/wallets/{walletId}/gas-check":                        guardAPIToken,
	"GET|HEAD /api/v1/wallets/{walletId}/gas-status":                   guardAPIToken,
	"POST /api/v1/wallets/{walletId}/withdraw/preview":                 guardAPIToken,
	"POST /api/v1/wallets/{walletId}/withdrawals":                      guardAPIToken,
	"GET|HEAD /api/v1/wallets/{walletId}/withdrawals/{idempotencyKey}": guardAPIToken,
	"GET|HEAD /api/v1/webhooks":                                        guardAPIToken,
	"POST /api/v1/webhooks":                                            guardAPIToken,
	"PATCH /api/v1/webhooks/{webhookId}":                               guardAPIToken,
	"GET|HEAD /health":                                                 guardPublic,
	"GET|HEAD /swagger/doc.json":                                       guardPublic,
	"GET|HEAD /swagger/index.html":                                     guardPublic,
	"POST /v1/accounts":                                                guardSession,
	"GET|HEAD /v1/accounts/{accountId}":                                guardSession,
	"PATCH /v1/accounts/{accountId}":                                   guardSession,
	"POST /v1/accounts/{accountId}/archive":                            guardSession,
	"POST /v1/accounts/{accountId}/freeze":                             guardSession,
	"GET|HEAD /v1/accounts/{accountId}/tokens":                         guardSession,
	"POST /v1/accounts/{accountId}/tokens":                             guardSession,
	"DELETE /v1/accounts/{accountId}/tokens/{tokenId}":                 guardSession,
	"GET|HEAD /v1/accounts/{accountId}/users":                          guardSession,
	"POST /v1/accounts/{accountId}/users":                              guardSession,
	"DELETE /v1/accounts/{accountId}/users/{userId}":                   guardSession,
	"GET|HEAD /v1/accounts/{accountId}/settings":                       guardSession,
	"PATCH /v1/accounts/{accountId}/settings/{group}":                  guardSession,
	"GET|HEAD /v1/accounts/{accountId}/features":                       guardSession,
	"PATCH /v1/accounts/{accountId}/features/{key}":                    guardSession,
	"POST /v1/auth/2fa/verify":                                         guardGuest,
	"POST /v1/auth/login":                                              guardGuest,
	"POST /v1/auth/logout":                                             guardSession,
	"POST /v1/auth/recover":                                            guardGuest,
	"POST /v1/auth/recover/confirm":                                    guardGuest,
	"POST /v1/auth/refresh":                                            guardGuest,
	"POST /v1/auth/register":                                           guardGuest,
	"GET|HEAD /v1/chains":                                              guardSession,
	"GET|HEAD /v1/chains/{chainId}":                                    guardSession,
	"GET|HEAD /v1/chains/{chainId}/resources":                          guardSession,
	"GET|HEAD /v1/chains/{chainId}/tokens":                             guardSession,
	"GET|HEAD /v1/convert":                                             guardSession,
	"GET|HEAD /v1/currencies":                                          guardSession,
	"GET|HEAD /v1/currencies/{code}":                                   guardSession,
	"GET|HEAD /v1/me/preferences":                                      guardSession,
	"PUT /v1/me/preferences":                                           guardSession,
	"GET|HEAD /v1/users/me":                                            guardSession,
	"PATCH /v1/users/me":                                               guardSession,
	"GET|HEAD /v1/users/me/accounts":                                   guardSession,
	"PATCH /v1/users/me/default-account":                               guardSession,
	"POST /v1/users/me/password":                                       guardSession,
	"DELETE /v1/users/me/totp":                                         guardSession,
	"POST /v1/users/me/totp/setup":                                     guardSession,
	"POST /v1/users/me/totp/verify":                                    guardSession,
	"GET|HEAD /v1/wallets":                                             guardSession,
	"POST /v1/wallets":                                                 guardSession,
	"GET|HEAD /v1/wallets/{walletId}":                                  guardSession,
	"POST /v1/wallets/{walletId}/activate":                             guardSession,
	"GET|HEAD /v1/wallets/{walletId}/addresses":                        guardSession,
	"POST /v1/wallets/{walletId}/addresses":                            guardSession,
	"PATCH /v1/wallets/{walletId}/addresses/{addressId}":               guardSession,
	"GET|HEAD /v1/wallets/{walletId}/balances":                         guardSession,
	"POST /v1/wallets/{walletId}/consolidate":                          guardSession,
	"POST /v1/wallets/{walletId}/freeze":                               guardSession,
	"POST /v1/wallets/{walletId}/gas-check":                            guardSession,
	"GET|HEAD /v1/wallets/{walletId}/gas-status":                       guardSession,
	"GET|HEAD /v1/wallets/{walletId}/settings":                         guardSession,
	"PATCH /v1/wallets/{walletId}/settings":                            guardSession,
	"GET|HEAD /v1/wallets/{walletId}/transactions":                     guardSession,
	"GET|HEAD /v1/wallets/{walletId}/transactions/{txId}":              guardSession,
	"GET|HEAD /v1/wallets/{walletId}/unspents":                         guardSession,
	"GET|HEAD /v1/wallets/{walletId}/users":                            guardSession,
	"POST /v1/wallets/{walletId}/users":                                guardSession,
	"DELETE /v1/wallets/{walletId}/users/{userId}":                     guardSession,
	"GET|HEAD /v1/wallets/{walletId}/webhooks":                         guardSession,
	"POST /v1/wallets/{walletId}/webhooks":                             guardSession,
	"DELETE /v1/wallets/{walletId}/webhooks/{webhookId}":               guardSession,
	"GET|HEAD /v1/wallets/{walletId}/whitelist":                        guardSession,
	"POST /v1/wallets/{walletId}/whitelist":                            guardSession,
	"DELETE /v1/wallets/{walletId}/whitelist/{entryId}":                guardSession,
	"POST /v1/wallets/{walletId}/withdraw/preview":                     guardSession,
	"GET|HEAD /v1/wallets/{walletId}/withdrawals":                      guardSession,
	"POST /v1/wallets/{walletId}/withdrawals":                          guardSession,
	"POST /v1/wallets/{walletId}/withdrawals/estimate":                 guardSession,
	"GET|HEAD /v1/wallets/{walletId}/withdrawals/{withdrawalId}":       guardSession,
	"POST /v1/wallets/{walletId}/withdrawals/{withdrawalId}/cancel":    guardSession,
	"POST /v1/webhooks/ingest/{provider}/{chainID}":                    guardProviderSignature,
}

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	bootstrap.Boot()
	_ = container.Get()
	os.Exit(m.Run())
}

// TestEveryRouteIsInTheRouteTable checks the table against the booted router
// in both directions: a served route without a row, and a row the router no
// longer serves.
func TestEveryRouteIsInTheRouteTable(t *testing.T) {
	served := servedRoutes()
	var violations architecture.Violations
	for route := range served {
		if _, listed := routeTable[route]; !listed {
			violations.Add("%s is served but has no row in routeTable", route)
		}
	}
	for route := range routeTable {
		if !served[route] {
			violations.Add("%s has a row in routeTable but is not served", route)
		}
	}
	architecture.Report(t, &violations)
}

// TestAuthenticatedRoutesRefuseAnAnonymousCaller sends every session and
// API-token route a request without credentials and expects 401 before any
// handler runs.
func TestAuthenticatedRoutesRefuseAnAnonymousCaller(t *testing.T) {
	var violations architecture.Violations
	for route, routeGuard := range routeTable {
		if routeGuard != guardSession && routeGuard != guardAPIToken {
			continue
		}
		method, path := requestFor(route)
		response, err := facades.Route().Test(httptest.NewRequest(method, path, strings.NewReader("{}")))
		if err != nil {
			violations.Add("%s: request failed: %v", route, err)
			continue
		}
		_ = response.Body.Close()
		if response.StatusCode != nethttp.StatusUnauthorized {
			violations.Add("%s answers %d to an anonymous caller, want 401", route, response.StatusCode)
		}
	}
	architecture.Report(t, &violations)
}

func TestRequestFor_FillsEveryPathParameter(t *testing.T) {
	method, path := requestFor("GET|HEAD /v1/wallets/{walletId}/transactions/{txId}")
	if method != nethttp.MethodGet {
		t.Errorf("method = %q, want GET", method)
	}
	if strings.ContainsAny(path, "{}") || !strings.HasPrefix(path, "/v1/wallets/") {
		t.Errorf("path = %q, want every parameter filled", path)
	}
	if method, _ := requestFor("DELETE /v1/users/me/totp"); method != nethttp.MethodDelete {
		t.Errorf("method = %q, want DELETE", method)
	}
}

func servedRoutes() map[string]bool {
	served := map[string]bool{}
	for _, info := range facades.Route().GetRoutes() {
		served[fmt.Sprintf("%s %s", info.Method, info.Path)] = true
	}
	return served
}

var pathParameter = regexp.MustCompile(`\{[^}]+\}`)

// placeholderID fills every path parameter: a syntactically valid UUID that
// matches no row.
const placeholderID = "00000000-0000-4000-8000-000000000000"

// requestFor turns a route key into a concrete method and path.
func requestFor(route string) (string, string) {
	methods, path, _ := strings.Cut(route, " ")
	method, _, _ := strings.Cut(methods, "|")
	return method, pathParameter.ReplaceAllString(path, placeholderID)
}
