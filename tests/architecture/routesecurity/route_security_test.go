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

// guard is the authentication a route runs behind. The ordered middleware
// chain is routeSecurity.chain (.ai/guidelines/authorization.md).
type guard string

const (
	guardPublic            guard = "public"             // health and API docs
	guardGuest             guard = "guest"              // the auth endpoints that create a session
	guardProviderSignature guard = "provider-signature" // inbound chain-provider webhooks
	guardSession           guard = "session"            // middleware.SessionAuth (dashboard, /v1)
	guardAPIToken          guard = "api-token"          // middleware.APITokenAuth (external, /api/v1)
)

// Repeated guard chains. Cors and CacheControl are not guards. The ingest
// signature is checked in the handler, so that route's chain is empty.
const (
	chainSession           = "SessionAuth"
	chainAccount           = "SessionAuth > AccountContext > TOTPEnrollment"
	chainAccountUsers      = chainAccount + " > Can(users.read)"
	chainAccountUsersWrite = chainAccount + " > Can(users.write)"
	chainHeader            = "SessionAuth > AccountHeader > TOTPEnrollment"
	chainCreateWallet      = chainHeader + " > RequireFundAction"
	chainWallet            = "SessionAuth > AccountHeader > TOTPEnrollment > WalletContext"
	chainMoveFunds         = chainWallet + " > RequireFundAction"
	chainGenerateAddress   = chainWallet + " > WalletCan(addresses.create)"
	chainUnspent           = "SessionAuth > AccountHeader > TOTPEnrollment > WalletContext > UTXOOnly"
	chainAPI               = "APITokenAuth"
	chainAPIWallet         = "APITokenAuth > APIWalletContext"
	chainWalletsRead       = "APITokenAuth > APIScope(wallets.read)"
	chainWalletsCreate     = "APITokenAuth > APIScope(wallets.create)"
	chainWalletRead        = "APITokenAuth > APIWalletContext > APIScope(wallets.read)"
	chainAddresses         = "APITokenAuth > APIWalletContext > APIScope(addresses.create)"
	chainSweep             = "APITokenAuth > APIWalletContext > APIScope(sweep.execute)"
	chainWithdrawals       = "APITokenAuth > APIWalletContext > APIScope(withdrawals.create)"
	chainTransactions      = "APITokenAuth > APIScope(transactions.read)"
	chainWebhooksRead      = "APITokenAuth > APIScope(webhooks.read)"
	chainWebhooksWrite     = "APITokenAuth > APIScope(webhooks.write)"
)

// routeSecurity is one row of the closed table: who may call the route, and
// the ordered guards the route files register.
type routeSecurity struct {
	auth  guard
	chain string
}

func session(chain string) routeSecurity { return routeSecurity{auth: guardSession, chain: chain} }
func api(chain string) routeSecurity     { return routeSecurity{auth: guardAPIToken, chain: chain} }

func guest() routeSecurity { return routeSecurity{auth: guardGuest} }
func public() routeSecurity {
	return routeSecurity{auth: guardPublic}
}
func provider() routeSecurity { return routeSecurity{auth: guardProviderSignature} }

// routeTable is the closed list of every route the router serves, keyed by
// Goravel's "METHOD /path". A new route fails TestEveryRouteIsInTheRouteTable
// and TestGuardChainMatchesRegistration until it gets a row here.
var routeTable = map[string]routeSecurity{
	"GET|HEAD /api/v1/addresses/{address}":                             api(chainAPI),
	"GET|HEAD /api/v1/chains":                                          api(chainAPI),
	"GET|HEAD /api/v1/transactions":                                    api(chainTransactions),
	"GET|HEAD /api/v1/transactions/{id}":                               api(chainTransactions),
	"GET|HEAD /api/v1/users/{external_id}/addresses":                   api(chainAPI),
	"GET|HEAD /api/v1/users/{external_id}/transactions":                api(chainTransactions),
	"GET|HEAD /api/v1/wallets":                                         api(chainWalletsRead),
	"POST /api/v1/wallets":                                             api(chainWalletsCreate),
	"GET|HEAD /api/v1/wallets/{walletId}":                              api(chainWalletRead),
	"GET|HEAD /api/v1/wallets/{walletId}/addresses":                    api(chainAPIWallet),
	"POST /api/v1/wallets/{walletId}/addresses":                        api(chainAddresses),
	"PATCH /api/v1/wallets/{walletId}/addresses/{addressId}":           api(chainAPIWallet),
	"GET|HEAD /api/v1/wallets/{walletId}/fee-estimate":                 api(chainAPIWallet),
	"POST /api/v1/wallets/{walletId}/consolidate":                      api(chainSweep),
	"POST /api/v1/wallets/{walletId}/gas-check":                        api(chainAPIWallet),
	"GET|HEAD /api/v1/wallets/{walletId}/gas-status":                   api(chainAPIWallet),
	"POST /api/v1/wallets/{walletId}/withdraw/preview":                 api(chainAPIWallet),
	"POST /api/v1/wallets/{walletId}/withdrawals":                      api(chainWithdrawals),
	"GET|HEAD /api/v1/wallets/{walletId}/withdrawals/{idempotencyKey}": api(chainAPIWallet),
	"GET|HEAD /api/v1/webhooks":                                        api(chainWebhooksRead),
	"POST /api/v1/webhooks":                                            api(chainWebhooksWrite),
	"PATCH /api/v1/webhooks/{webhookId}":                               api(chainWebhooksWrite),
	"GET|HEAD /health":                                                 public(),
	"GET|HEAD /swagger/doc.json":                                       public(),
	"GET|HEAD /swagger/index.html":                                     public(),
	"POST /v1/accounts":                                                session(chainSession),
	"GET|HEAD /v1/accounts/{accountId}":                                session(chainAccount),
	"PATCH /v1/accounts/{accountId}":                                   session(chainAccount),
	"POST /v1/accounts/{accountId}/archive":                            session(chainAccount),
	"POST /v1/accounts/{accountId}/freeze":                             session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/activity":                       session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/tokens":                         session(chainAccount),
	"POST /v1/accounts/{accountId}/tokens":                             session(chainAccount),
	"DELETE /v1/accounts/{accountId}/tokens/{tokenId}":                 session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/users":                          session(chainAccountUsers),
	"POST /v1/accounts/{accountId}/users":                              session(chainAccount),
	"PATCH /v1/accounts/{accountId}/users/{userId}":                    session(chainAccount),
	"DELETE /v1/accounts/{accountId}/users/{userId}":                   session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/invites":                        session(chainAccountUsers),
	"POST /v1/accounts/{accountId}/invites":                            session(chainAccountUsersWrite),
	"POST /v1/accounts/{accountId}/invites/{id}/resend":                session(chainAccountUsersWrite),
	"DELETE /v1/accounts/{accountId}/invites/{id}":                     session(chainAccountUsersWrite),
	"GET|HEAD /v1/accounts/{accountId}/settings":                       session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/settings/{group}":               session(chainAccount),
	"POST /v1/accounts/{accountId}/settings/sections/{section}/cache":  session(chainAccount),
	"POST /v1/accounts/{accountId}/settings/sections/{section}/reset":  session(chainAccount),
	"PATCH /v1/accounts/{accountId}/settings/{group}":                  session(chainAccount),
	"PUT /v1/accounts/{accountId}/settings/{group}":                    session(chainAccount),
	"GET|HEAD /v1/accounts/{accountId}/features":                       session(chainAccount),
	"PATCH /v1/accounts/{accountId}/features/{key}":                    session(chainAccount),
	"POST /v1/auth/2fa/verify":                                         guest(),
	"POST /v1/auth/login":                                              guest(),
	"POST /v1/auth/logout":                                             session(chainSession),
	"GET|HEAD /v1/auth/invites/{token}":                                guest(),
	"POST /v1/auth/invites/accept":                                     guest(),
	"POST /v1/auth/recover":                                            guest(),
	"POST /v1/auth/recover/confirm":                                    guest(),
	"POST /v1/auth/refresh":                                            guest(),
	"POST /v1/auth/register":                                           guest(),
	"GET|HEAD /v1/chains":                                              session(chainHeader),
	"GET|HEAD /v1/chains/{chainId}":                                    session(chainHeader),
	"GET|HEAD /v1/chains/{chainId}/resources":                          session(chainHeader),
	"GET|HEAD /v1/chains/{chainId}/tokens":                             session(chainHeader),
	"GET|HEAD /v1/convert":                                             session(chainSession),
	"GET|HEAD /v1/currencies":                                          session(chainSession),
	"GET|HEAD /v1/currencies/{code}":                                   session(chainSession),
	"GET|HEAD /v1/me/preferences":                                      session(chainSession),
	"PATCH /v1/platform/chains/{chainId}":                              session(chainSession),
	"PATCH /v1/platform/chains/{chainId}/rpc":                          session(chainSession),
	"GET|HEAD /v1/platform/accounts":                                   session(chainSession),
	"GET|HEAD /v1/platform/accounts/{accountId}/users":                 session(chainSession),
	"POST /v1/platform/accounts/{accountId}/owners":                    session(chainSession),
	"POST /v1/platform/accounts/{accountId}/archive":                   session(chainSession),
	"POST /v1/platform/accounts/{accountId}/freeze":                    session(chainSession),
	"POST /v1/platform/accounts/{accountId}/unfreeze":                  session(chainSession),
	"GET|HEAD /v1/platform/accounts/{accountId}/settings/{group}":      session(chainSession),
	"PUT /v1/platform/accounts/{accountId}/settings/{group}":           session(chainSession),
	"GET|HEAD /v1/platform/settings":                                   session(chainSession),
	"POST /v1/platform/settings/mail/test":                             session(chainSession),
	"GET|HEAD /v1/platform/settings/{group}":                           session(chainSession),
	"PUT /v1/platform/settings/{group}":                                session(chainSession),
	"POST /v1/platform/settings/sections/{section}/cache":              session(chainSession),
	"POST /v1/platform/settings/sections/{section}/reset":              session(chainSession),
	"GET|HEAD /v1/platform/activity":                                   session(chainSession),
	"GET|HEAD /v1/platform/features":                                   session(chainSession),
	"GET|HEAD /v1/platform/users":                                      session(chainSession),
	"PATCH /v1/platform/features/{key}":                                session(chainSession),
	"DELETE /v1/platform/users/{id}/mfa":                               session(chainSession),
	"POST /v1/platform/users/{id}/reactivate":                          session(chainSession),
	"POST /v1/platform/users/{id}/sessions/revoke":                     session(chainSession),
	"POST /v1/platform/users/{id}/suspend":                             session(chainSession),
	"PUT /v1/me/preferences":                                           session(chainSession),
	"GET|HEAD /v1/users/me":                                            session(chainSession),
	"PATCH /v1/users/me":                                               session(chainSession),
	"GET|HEAD /v1/users/me/accounts":                                   session(chainSession),
	"PATCH /v1/users/me/default-account":                               session(chainSession),
	"POST /v1/users/me/password":                                       session(chainSession),
	"DELETE /v1/users/me/totp":                                         session(chainSession),
	"POST /v1/users/me/totp/setup":                                     session(chainSession),
	"POST /v1/users/me/totp/verify":                                    session(chainSession),
	"GET|HEAD /v1/wallets":                                             session(chainHeader),
	"POST /v1/wallets":                                                 session(chainCreateWallet),
	"GET|HEAD /v1/wallets/{walletId}":                                  session(chainHeader),
	"POST /v1/wallets/{walletId}/activate":                             session(chainWallet),
	"POST /v1/wallets/{walletId}/archive":                              session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/addresses":                        session(chainWallet),
	"POST /v1/wallets/{walletId}/addresses":                            session(chainGenerateAddress),
	"PATCH /v1/wallets/{walletId}/addresses/{addressId}":               session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/balances":                         session(chainWallet),
	"POST /v1/wallets/{walletId}/consolidate":                          session(chainMoveFunds),
	"GET|HEAD /v1/wallets/{walletId}/fee-estimate":                     session(chainWallet),
	"POST /v1/wallets/{walletId}/freeze":                               session(chainWallet),
	"POST /v1/wallets/{walletId}/gas-check":                            session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/gas-status":                       session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/settings":                         session(chainWallet),
	"PATCH /v1/wallets/{walletId}/settings":                            session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/transactions":                     session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/transactions/{txId}":              session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/unspents":                         session(chainUnspent),
	"GET|HEAD /v1/wallets/{walletId}/users":                            session(chainWallet),
	"POST /v1/wallets/{walletId}/users":                                session(chainWallet),
	"DELETE /v1/wallets/{walletId}/users/{userId}":                     session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/webhooks":                         session(chainWallet),
	"POST /v1/wallets/{walletId}/webhooks":                             session(chainWallet),
	"POST /v1/wallets/{walletId}/webhooks/{webhookId}/test":            session(chainWallet),
	"DELETE /v1/wallets/{walletId}/webhooks/{webhookId}":               session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/whitelist":                        session(chainWallet),
	"POST /v1/wallets/{walletId}/whitelist":                            session(chainWallet),
	"DELETE /v1/wallets/{walletId}/whitelist/{entryId}":                session(chainWallet),
	"POST /v1/wallets/{walletId}/withdraw/preview":                     session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/withdrawals":                      session(chainWallet),
	"POST /v1/wallets/{walletId}/withdrawals":                          session(chainMoveFunds),
	"POST /v1/wallets/{walletId}/withdrawals/estimate":                 session(chainWallet),
	"GET|HEAD /v1/wallets/{walletId}/withdrawals/{withdrawalId}":       session(chainWallet),
	"POST /v1/wallets/{walletId}/withdrawals/{withdrawalId}/cancel":    session(chainWallet),
	"GET|HEAD /v1/withdrawals/{withdrawalId}":                          session(chainHeader),
	"POST /v1/webhooks/ingest/{provider}/{chainID}":                    provider(),
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
	for route, entry := range routeTable {
		if entry.auth != guardSession && entry.auth != guardAPIToken {
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
