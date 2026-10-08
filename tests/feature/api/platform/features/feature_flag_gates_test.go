package features

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/google/uuid"
	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const featureGatePassword = "correct-horse-battery"

// featureGateSuite is one HTTP test per money-moving flag. A missing row and
// enabled=true must not answer that gate's 409 and must not persist or
// broadcast. enabled=false answers 409. Both dashboard and external routes
// use the same check. The body is empty so a request that passes the gate
// fails validation before any chain call.
type featureGateSuite struct {
	support.HTTPSuite
}

func TestFeature_Flag_Gates(t *testing.T) {
	support.RunSuite(t, new(featureGateSuite))
}

func (s *featureGateSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *featureGateSuite) TestFeatureGate_Withdrawals_Enabled() {
	s.gateBothSurfaces(features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused, "/withdrawals")
}

func (s *featureGateSuite) TestFeatureGate_Sweep_Enabled() {
	s.gateBothSurfaces(features.FlagSweepEnabled, features.CodeSweepPaused, "/consolidate")
}

func (s *featureGateSuite) gateBothSurfaces(key, code, suffix string) {
	s.T().Helper()
	_, accountID, session, walletID := s.ownerWallet()
	apiToken := s.apiToken(accountID, key)

	surfaces := []gateSurface{
		{name: "dashboard", token: session, prefix: "/v1/wallets/", header: true},
		{name: "external", token: apiToken, prefix: "/api/v1/wallets/", header: false},
	}

	s.openWithoutBroadcast("missing", surfaces, accountID, walletID, suffix, code)

	s.setFlag(session, accountID, key, true)
	s.openWithoutBroadcast("enabled", surfaces, accountID, walletID, suffix, code)

	s.setFlag(session, accountID, key, false)
	for _, surface := range surfaces {
		beforeWithdrawals := s.rows(&models.Withdrawal{}, walletID)
		beforeTransactions := s.rows(&models.Transaction{}, walletID)
		response := s.post(surface, accountID, walletID, suffix)
		s.AssertError(response, http.StatusConflict, code, code)
		s.Equal(beforeWithdrawals, s.rows(&models.Withdrawal{}, walletID), surface.name+" persisted a withdrawal")
		s.Equal(beforeTransactions, s.rows(&models.Transaction{}, walletID), surface.name+" persisted a transaction")
	}
}

func (s *featureGateSuite) openWithoutBroadcast(label string, surfaces []gateSurface, accountID, walletID uuid.UUID, suffix, code string) {
	s.T().Helper()
	for _, surface := range surfaces {
		beforeWithdrawals := s.rows(&models.Withdrawal{}, walletID)
		beforeTransactions := s.rows(&models.Transaction{}, walletID)
		s.notGate(surface.name+" "+label, s.post(surface, accountID, walletID, suffix), code)
		s.Equal(beforeWithdrawals, s.rows(&models.Withdrawal{}, walletID), surface.name+" "+label+" persisted a withdrawal")
		s.Equal(beforeTransactions, s.rows(&models.Transaction{}, walletID), surface.name+" "+label+" persisted a transaction")
	}
}

type gateSurface struct {
	name   string
	token  string
	prefix string
	header bool
}

func (s *featureGateSuite) post(surface gateSurface, accountID, walletID uuid.UUID, suffix string) contractstestinghttp.Response {
	s.T().Helper()
	path := surface.prefix + walletID.String() + suffix
	if strings.HasPrefix(surface.prefix, "/api/") {
		return s.External(path, support.Token{Bearer: surface.token}).Post(`{}`)
	}
	session := support.Session{AccessToken: surface.token}
	if surface.header {
		session.AccountID = accountID.String()
	}
	return s.Post(path, session, `{}`)
}

func (s *featureGateSuite) notGate(label string, response contractstestinghttp.Response, code string) {
	s.T().Helper()
	status := s.status(response)
	body := s.json(response)
	errorBody, _ := body["error"].(map[string]any)
	got, _ := errorBody["code"].(string)
	if status == http.StatusConflict && got == code {
		s.Failf(label, "gate 409 %s body %s", code, mustJSON(body))
	}
}

func (s *featureGateSuite) status(response contractstestinghttp.Response) int {
	s.T().Helper()
	value := reflect.ValueOf(response)
	s.Require().Equal(reflect.Ptr, value.Kind())
	field := value.Elem().FieldByName("response")
	s.Require().True(field.IsValid())
	raw := reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem()
	httpResponse, ok := raw.Interface().(*http.Response)
	s.Require().True(ok)
	s.Require().NotNil(httpResponse)
	return httpResponse.StatusCode
}

func (s *featureGateSuite) json(response contractstestinghttp.Response) map[string]any {
	s.T().Helper()
	body, err := response.Json()
	s.Require().NoError(err)
	return body
}

func (s *featureGateSuite) setFlag(_ string, accountID uuid.UUID, key string, enabled bool) {
	s.T().Helper()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO features (account_id, "key", enabled, created_at, updated_at)
		 VALUES (?, ?, ?, NOW(), NOW())
		 ON CONFLICT (account_id, "key") DO UPDATE
		 SET enabled = EXCLUDED.enabled, updated_at = NOW()`,
		accountID, key, enabled,
	)
	s.Require().NoError(err)
}

func (s *featureGateSuite) rows(model any, walletID uuid.UUID) int64 {
	s.T().Helper()
	count, err := facades.Orm().Query().Model(model).Where("wallet_id = ?", walletID).Count()
	s.Require().NoError(err)
	return count
}

func (s *featureGateSuite) ownerWallet() (uuid.UUID, uuid.UUID, string, uuid.UUID) {
	s.T().Helper()
	hash, err := authsvc.NewService().HashPassword(featureGatePassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := "owner-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)

	accountID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Account{
		ID:          accountID,
		Name:        "Gates " + accountID.String()[:8],
		Status:      "active",
		Environment: "prod",
	}))
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: "owner",
	}))

	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            "gate wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     "02abc123def456",
		MPCCurve:         "secp256k1",
		AccountID:        &accountID,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, featureGatePassword)
	response := s.Post("/v1/auth/login", support.Session{}, body)
	response.AssertOk()
	content, err := response.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return userID, accountID, parsed.AccessToken, walletID
}

func (s *featureGateSuite) apiToken(accountID uuid.UUID, label string) string {
	s.T().Helper()
	tokenID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO access_tokens (id, account_id, name, token_hash, spending_limit, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, NOW(), NOW())`,
		tokenID, accountID, "gate-"+label, "test-hash-gate-"+label+"-"+tokenID.String(), "{}",
	)
	s.Require().NoError(err)
	jwt, err := middleware.MintAPIToken(&models.AccessToken{
		ID:        tokenID,
		AccountID: accountID,
		Name:      "gate-" + label,
	}, false)
	s.Require().NoError(err)
	return jwt
}

func mustJSON(body map[string]any) string {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Sprint(body)
	}
	return string(encoded)
}
