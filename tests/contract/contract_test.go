package contract

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/bootstrap"
	"github.com/macrowallets/waas/database/seeds"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/feature/support/testenv"
)

const (
	snapshotPath     = "testdata/http_contract.txt"
	baseSnapshotPath = "testdata/http_contract_base.txt"
	baseDiffPath     = "testdata/http_contract_base.diff"
)

var updateContract = flag.Bool("update-contract", false, "rewrite "+snapshotPath+" from the current answers")

// volatileFields are JSON fields whose value is random on every run and has
// no recognizable shape (opaque refresh tokens, TOTP secrets).
var volatileFields = []string{"refresh_token", "secret", "otpauth_url", "qr_code"}

func TestMain(m *testing.M) {
	if err := testenv.Load(); err != nil {
		panic(err)
	}
	bootstrap.Boot()
	_ = container.Get()
	os.Exit(m.Run())
}

// TestHTTPContract replays the scenario against the booted router on a
// freshly migrated database and compares every answer with the snapshot.
// A difference is either a bug or a decided contract change: record the
// decided ones in .ai/guidelines/http-error-contract.md and rewrite the
// snapshot with -update-contract.
func TestContract_HTTP_Contract(t *testing.T) {
	refuseOutboundMail(t)
	fixtures.TestDB(t)
	seedReferenceData(t)

	runner := &scenarioRunner{t: t, vars: map[string]string{}, normalizer: NewNormalizer(volatileFields...)}
	for _, step := range contractScenario() {
		runner.run(step)
	}
	got := Render(runner.exchanges)
	for _, exchange := range runner.exchanges {
		if err := RejectLegacyErrorShape(exchange); err != nil {
			t.Error(err)
		}
	}
	if t.Failed() {
		return
	}

	if *updateContract {
		if err := os.MkdirAll(filepath.Dir(snapshotPath), 0o750); err != nil {
			t.Fatalf("create snapshot directory: %v", err)
		}
		if err := os.WriteFile(snapshotPath, []byte(got), 0o600); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		t.Logf("snapshot rewritten: %d exchanges", len(runner.exchanges))
		return
	}
	want, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read snapshot (run with -update-contract to create it): %v", err)
	}
	for _, difference := range Diff(string(want), got) {
		t.Errorf("%s", difference)
	}
	assertBaseDiff(t, got)
}

// assertBaseDiff compares this run with the recording taken on the base
// commit. Differences this branch already shipped are locked in baseDiffPath
// and listed in .ai/guidelines/http-error-contract.md.
func assertBaseDiff(t *testing.T, got string) {
	t.Helper()
	base, err := os.ReadFile(baseSnapshotPath)
	if err != nil {
		t.Fatalf("read base recording: %v", err)
	}
	gotDiff := RenderDiff(Diff(string(base), got))
	expected, err := os.ReadFile(baseDiffPath)
	if err != nil {
		t.Fatalf("read base diff: %v", err)
	}
	if string(expected) != gotDiff {
		t.Errorf("base-to-tip contract diff changed (%d bytes recorded, %d bytes from this run)", len(expected), len(gotDiff))
	}
}

// refuseOutboundMail points Goravel's mailer at an address Dial rejects before
// any network call. The mailer reads mail.host and mail.port. An unbracketed
// IPv6 address fails that parse, so a blocked SMTP socket cannot turn the
// handler's response into a timeout.
func refuseOutboundMail(t *testing.T) {
	t.Helper()
	facades.Config().Add("mail.host", "::1")
	facades.Config().Add("mail.port", 59999)
}

func seedReferenceData(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	// Ordered: tokens reference chains.
	referenceSeeds := []struct {
		name string
		seed func(context.Context) error
	}{
		{"chains", seeds.SeedChains},
		{"tokens", seeds.SeedTokens},
		{"currencies", seeds.SeedCurrencies},
	}
	for _, reference := range referenceSeeds {
		if err := reference.seed(ctx); err != nil {
			t.Fatalf("seed %s: %v", reference.name, err)
		}
	}
}

// step is one request of the scenario. Path, body and header values may hold
// {{name}} placeholders filled from values captured by earlier steps.
type step struct {
	name    string
	method  string
	path    string
	body    string
	bearer  string            // variable holding the Authorization bearer token
	account string            // variable holding the X-Account-Id header
	capture map[string]string // variable → dotted JSON path in the answer
	before  func(t *testing.T, vars map[string]string)
}

type scenarioRunner struct {
	t          *testing.T
	vars       map[string]string
	normalizer *Normalizer
	exchanges  []Exchange
}

func (r *scenarioRunner) run(s step) {
	r.t.Helper()
	if s.before != nil {
		s.before(r.t, r.vars)
	}
	request := httptest.NewRequest(s.method, r.fill(s.path), strings.NewReader(r.fill(s.body)))
	if s.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	if s.bearer != "" {
		request.Header.Set("Authorization", "Bearer "+r.require(s.bearer))
	}
	if s.account != "" {
		request.Header.Set("X-Account-Id", r.require(s.account))
	}
	response, err := facades.Route().Test(request)
	if err != nil {
		r.t.Fatalf("%s: request failed: %v", s.name, err)
	}
	raw, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil {
		r.t.Fatalf("%s: read body: %v", s.name, err)
	}
	for variable, path := range s.capture {
		value, err := jsonPath(raw, path)
		if err != nil {
			r.t.Fatalf("%s: capture %s from %q: %v (body %s)", s.name, variable, path, err, raw)
		}
		r.vars[variable] = value
	}
	r.exchanges = append(r.exchanges, Exchange{
		Step:        fmt.Sprintf("%02d %s", len(r.exchanges)+1, s.name),
		Method:      s.method,
		Path:        s.path,
		Status:      response.StatusCode,
		ContentType: response.Header.Get("Content-Type"),
		Body:        r.normalizer.Normalize(string(raw)),
	})
}

func (r *scenarioRunner) fill(text string) string {
	for name, value := range r.vars {
		text = strings.ReplaceAll(text, "{{"+name+"}}", value)
	}
	return text
}

func (r *scenarioRunner) require(variable string) string {
	value, ok := r.vars[variable]
	if !ok {
		r.t.Fatalf("variable %q was never captured", variable)
	}
	return value
}

// jsonPath reads a dotted path ("accounts.0.id") out of a JSON document and
// returns the value as text.
func jsonPath(document []byte, path string) (string, error) {
	var node any
	if err := json.Unmarshal(document, &node); err != nil {
		return "", fmt.Errorf("not JSON: %w", err)
	}
	for _, key := range strings.Split(path, ".") {
		switch typed := node.(type) {
		case map[string]any:
			value, ok := typed[key]
			if !ok {
				return "", fmt.Errorf("no key %q", key)
			}
			node = value
		case []any:
			index, err := strconv.Atoi(key)
			if err != nil || index < 0 || index >= len(typed) {
				return "", fmt.Errorf("no index %q", key)
			}
			node = typed[index]
		default:
			return "", fmt.Errorf("cannot descend into %T at %q", node, key)
		}
	}
	if text, ok := node.(string); ok {
		return text, nil
	}
	encoded, err := json.Marshal(node)
	return string(encoded), err
}

const (
	contractEmail    = "contract@example.com"
	contractPassword = "contract-password-1"
	unknownID        = "00000000-0000-4000-8000-000000000000"
	// fixtureDepositAddress replaces the random address mocks gives the wallet.
	fixtureDepositAddress = "0x1111111111111111111111111111111111111111"
)

// contractScenario is the fixed sequence of requests the snapshot records:
// the main dashboard and external routes, success and error paths, nothing
// that calls a chain node or moves funds.
func contractScenario() []step {
	register := fmt.Sprintf(`{"email":%q,"password":%q,"full_name":"Contract User","organization_name":"Contract Org"}`,
		contractEmail, contractPassword)
	login := fmt.Sprintf(`{"email":%q,"password":%q}`, contractEmail, contractPassword)
	return []step{
		{name: "health", method: "GET", path: "/health"},

		// Session lifecycle.
		{name: "register without a body", method: "POST", path: "/v1/auth/register"},
		{name: "register with missing fields", method: "POST", path: "/v1/auth/register", body: `{"email":"not-an-email"}`},
		{name: "register", method: "POST", path: "/v1/auth/register", body: register},
		{name: "register the same e-mail again", method: "POST", path: "/v1/auth/register", body: register},
		{name: "login with a wrong password", method: "POST", path: "/v1/auth/login",
			body: fmt.Sprintf(`{"email":%q,"password":"wrong-password-1"}`, contractEmail)},
		{name: "login", method: "POST", path: "/v1/auth/login", body: login,
			capture: map[string]string{"session": "access_token", "refresh": "refresh_token"}},
		{name: "refresh with an unknown token", method: "POST", path: "/v1/auth/refresh", body: `{"refresh_token":"unknown"}`},
		{name: "refresh", method: "POST", path: "/v1/auth/refresh", body: `{"refresh_token":"{{refresh}}"}`,
			capture: map[string]string{"session": "access_token"}},
		{name: "two-factor verify with a bad token", method: "POST", path: "/v1/auth/2fa/verify",
			body: `{"challenge_token":"bad","code":"000000"}`},
		{name: "recover", method: "POST", path: "/v1/auth/recover", body: fmt.Sprintf(`{"email":%q}`, contractEmail)},
		{name: "recover confirm with an unknown token", method: "POST", path: "/v1/auth/recover/confirm",
			body: `{"token":"unknown","password":"another-password-1"}`},

		// The current user.
		{name: "me without a session", method: "GET", path: "/v1/users/me"},
		{name: "me with a malformed session", method: "GET", path: "/v1/users/me", bearer: "unknownToken",
			before: func(_ *testing.T, vars map[string]string) { vars["unknownToken"] = "not-a-jwt" }},
		{name: "me", method: "GET", path: "/v1/users/me", bearer: "session"},
		{name: "update me", method: "PATCH", path: "/v1/users/me", bearer: "session", body: `{"full_name":"Contract User Renamed"}`},
		{name: "my accounts", method: "GET", path: "/v1/users/me/accounts", bearer: "session"},
		{name: "change password with a wrong current one", method: "POST", path: "/v1/users/me/password", bearer: "session",
			body: `{"current_password":"wrong-password-1","new_password":"another-password-1"}`},
		{name: "preferences", method: "GET", path: "/v1/me/preferences", bearer: "session"},
		{name: "update preferences", method: "PUT", path: "/v1/me/preferences", bearer: "session",
			body: `{"preferred_fiat_code":"BRL","display_in_fiat":false}`},
		{name: "update preferences with an unknown fiat", method: "PUT", path: "/v1/me/preferences", bearer: "session",
			body: `{"preferred_fiat_code":"ZZZ"}`},

		// Accounts.
		{name: "create account without a name", method: "POST", path: "/v1/accounts", bearer: "session", body: `{}`},
		{name: "create account", method: "POST", path: "/v1/accounts", bearer: "session", body: `{"name":"Contract Account"}`,
			capture: map[string]string{"account": "id"}},
		{name: "get account", method: "GET", path: "/v1/accounts/{{account}}", bearer: "session"},
		{name: "get an unknown account", method: "GET", path: "/v1/accounts/" + unknownID, bearer: "session"},
		{name: "update account", method: "PATCH", path: "/v1/accounts/{{account}}", bearer: "session", body: `{"name":"Contract Account Renamed"}`},
		{name: "account users", method: "GET", path: "/v1/accounts/{{account}}/users", bearer: "session"},
		{name: "add account user with an invalid role", method: "POST", path: "/v1/accounts/{{account}}/users", bearer: "session",
			body: `{"email":"invitee@example.com","role":"superuser"}`},
		{name: "account tokens", method: "GET", path: "/v1/accounts/{{account}}/tokens", bearer: "session"},
		{name: "create account token", method: "POST", path: "/v1/accounts/{{account}}/tokens", bearer: "session",
			body: `{"name":"contract token"}`, capture: map[string]string{"apiToken": "token", "apiTokenID": "metadata.id"}},

		// Reference data.
		{name: "chains without an account header", method: "GET", path: "/v1/chains", bearer: "session"},
		{name: "chains", method: "GET", path: "/v1/chains", bearer: "session", account: "account"},
		{name: "chain", method: "GET", path: "/v1/chains/eth", bearer: "session", account: "account"},
		{name: "unknown chain", method: "GET", path: "/v1/chains/nochain", bearer: "session", account: "account"},
		{name: "chain tokens", method: "GET", path: "/v1/chains/eth/tokens", bearer: "session", account: "account"},
		{name: "currencies", method: "GET", path: "/v1/currencies", bearer: "session"},
		{name: "currency", method: "GET", path: "/v1/currencies/USD", bearer: "session"},
		{name: "unknown currency", method: "GET", path: "/v1/currencies/zzz", bearer: "session"},

		// Dashboard wallets (no chain call: the wallet is a fixture row).
		{name: "wallets", method: "GET", path: "/v1/wallets", bearer: "session", account: "account"},
		{name: "create wallet with an invalid body", method: "POST", path: "/v1/wallets", bearer: "session", account: "account", body: `{}`},
		{name: "unknown wallet", method: "GET", path: "/v1/wallets/" + unknownID, bearer: "session", account: "account"},
		{name: "wallet", method: "GET", path: "/v1/wallets/{{wallet}}", bearer: "session", account: "account",
			before: insertFixtureWallet},
		{name: "wallet addresses", method: "GET", path: "/v1/wallets/{{wallet}}/addresses", bearer: "session", account: "account"},
		{name: "wallet users", method: "GET", path: "/v1/wallets/{{wallet}}/users", bearer: "session", account: "account"},
		{name: "wallet whitelist", method: "GET", path: "/v1/wallets/{{wallet}}/whitelist", bearer: "session", account: "account"},
		{name: "wallet webhooks", method: "GET", path: "/v1/wallets/{{wallet}}/webhooks", bearer: "session", account: "account"},
		{name: "wallet settings", method: "GET", path: "/v1/wallets/{{wallet}}/settings", bearer: "session", account: "account"},
		{name: "wallet transactions", method: "GET", path: "/v1/wallets/{{wallet}}/transactions", bearer: "session", account: "account"},
		{name: "wallet withdrawals", method: "GET", path: "/v1/wallets/{{wallet}}/withdrawals", bearer: "session", account: "account"},
		{name: "unknown wallet withdrawal", method: "GET", path: "/v1/wallets/{{wallet}}/withdrawals/" + unknownID, bearer: "session", account: "account"},
		{name: "wallet unspents on an account chain", method: "GET", path: "/v1/wallets/{{wallet}}/unspents", bearer: "session", account: "account"},

		// External API.
		{name: "external without a token", method: "GET", path: "/api/v1/wallets"},
		{name: "external with a malformed token", method: "GET", path: "/api/v1/wallets", bearer: "unknownToken"},
		{name: "external chains", method: "GET", path: "/api/v1/chains", bearer: "apiToken"},
		{name: "external wallets", method: "GET", path: "/api/v1/wallets", bearer: "apiToken"},
		{name: "external wallet", method: "GET", path: "/api/v1/wallets/{{wallet}}", bearer: "apiToken"},
		{name: "external wallet of another account", method: "GET", path: "/api/v1/wallets/" + unknownID, bearer: "apiToken"},
		{name: "external wallet addresses", method: "GET", path: "/api/v1/wallets/{{wallet}}/addresses", bearer: "apiToken"},
		{name: "external unknown withdrawal", method: "GET", path: "/api/v1/wallets/{{wallet}}/withdrawals/unknown-key", bearer: "apiToken"},
		{name: "external transactions", method: "GET", path: "/api/v1/transactions", bearer: "apiToken"},
		{name: "external unknown transaction", method: "GET", path: "/api/v1/transactions/" + unknownID, bearer: "apiToken"},
		{name: "external unknown address", method: "GET", path: "/api/v1/addresses/0x0000000000000000000000000000000000000000", bearer: "apiToken"},
		{name: "external webhooks", method: "GET", path: "/api/v1/webhooks", bearer: "apiToken"},
		{name: "external create webhook with an invalid body", method: "POST", path: "/api/v1/webhooks", bearer: "apiToken", body: `{}`},

		// Inbound provider webhook without a signature.
		{name: "ingest without a signature", method: "POST", path: "/v1/webhooks/ingest/alchemy/eth", body: `{}`},

		// Revocation and logout.
		{name: "revoke account token", method: "DELETE", path: "/v1/accounts/{{account}}/tokens/{{apiTokenID}}", bearer: "session"},
		{name: "external with a revoked token", method: "GET", path: "/api/v1/wallets", bearer: "apiToken"},
		{name: "logout", method: "POST", path: "/v1/auth/logout", bearer: "session"},

		// Rate limit: the second call of one token inside the minute is refused
		// with the error envelope.
		{name: "external within the rate limit", method: "GET", path: "/api/v1/wallets", bearer: "throttledToken",
			before: limitAPIToOnePerMinute},
		{name: "external over the rate limit", method: "GET", path: "/api/v1/wallets", bearer: "throttledToken"},
	}
}

// limitAPIToOnePerMinute lets one call per token through and gives the step a
// bearer no other run has used, so its bucket starts full.
func limitAPIToOnePerMinute(t *testing.T, vars map[string]string) {
	t.Helper()
	const key = "http.throttle.api_per_minute"
	previous := facades.Config().Get(key)
	facades.Config().Add(key, 1)
	t.Cleanup(func() { facades.Config().Add(key, previous) })
	vars["throttledToken"] = "not-a-jwt-" + uuid.NewString()
}

// insertFixtureWallet adds an eth wallet with its deposit address to the
// scenario's account, without keygen or Secrets Manager.
func insertFixtureWallet(t *testing.T, vars map[string]string) {
	t.Helper()
	accountID, err := uuid.Parse(vars["account"])
	if err != nil {
		t.Fatalf("account id %q: %v", vars["account"], err)
	}
	wallet := fixtures.InsertWalletWithAccount(t, "eth", &accountID)
	if _, err := facades.Orm().Query().Exec(`UPDATE addresses SET address = ? WHERE id = ?`,
		fixtureDepositAddress, wallet.DepositAddress.ID); err != nil {
		t.Fatalf("pin fixture deposit address: %v", err)
	}
	vars["wallet"] = wallet.ID.String()
}
