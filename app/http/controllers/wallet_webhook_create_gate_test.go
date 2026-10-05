package controllers_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/mocks"
)

const walletWebhookCreateGatePassword = "correct-horse-battery"

// WalletWebhookCreateGateTestSuite drives POST /v1/wallets/{walletId}/webhooks,
// POST /v1/wallets/{walletId}/webhooks/{webhookId}/test, and
// DELETE /v1/wallets/{walletId}/webhooks/{webhookId} through
// WalletManageWebhooks. Wallet role owner or admin may create, test, or delete
// a webhook, and so may account role owner or admin. Auditor, user, and the
// other wallet roles are refused before a webhook is written, tested, or removed.
type WalletWebhookCreateGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWalletWebhookCreateGateSuite(t *testing.T) {
	suite.Run(t, new(WalletWebhookCreateGateTestSuite))
}

func (s *WalletWebhookCreateGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WalletWebhookCreateGateTestSuite) TestWalletWebhookCreateFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet webhook create")
	s.seedChain()
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)

	denied := []struct {
		accountRole string
		walletRole  string
	}{
		{models.AccountRoleUser, models.WalletRoleViewer},
		{models.AccountRoleAuditor, models.WalletRoleViewer},
		{models.AccountRoleUser, models.WalletRoleSpender},
		{models.AccountRoleUser, models.WalletRoleApprover},
	}
	for _, caller := range denied {
		actor := s.member(caller.accountRole, account.ID)
		s.assign(actor.id, wallet.ID, caller.walletRole)
		hookURL := s.hookURL(caller.accountRole + "-" + caller.walletRole)
		resp := s.createWebhook(actor.token, account.ID, wallet.ID, hookURL)
		s.assertWebhookForbidden(resp)
		s.Equal(int64(0), s.webhookCount(wallet.ID, hookURL))
	}

	allowed := []struct {
		accountRole string
		walletRole  string
	}{
		{accountRole: models.AccountRoleOwner},
		{accountRole: models.AccountRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: "owner"},
	}
	for _, caller := range allowed {
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, wallet.ID, caller.walletRole)
		}
		hookURL := s.hookURL(caller.accountRole + "-" + caller.walletRole)
		resp := s.createWebhook(actor.token, account.ID, wallet.ID, hookURL)
		resp.AssertStatus(201)
		s.assertCreatedWebhook(resp, wallet.ID, hookURL)
		s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	}
}

func (s *WalletWebhookCreateGateTestSuite) TestMissingWalletWebhookIs404BeforeTheRoleCheck() {
	account := mocks.InsertAccount(s.T(), "wallet webhook missing")
	s.seedChain()
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	actor := s.member(models.AccountRoleUser, account.ID)
	s.assign(actor.id, wallet.ID, models.WalletRoleViewer)

	resp := s.deleteWebhook(actor.token, account.ID, wallet.ID, uuid.New())
	resp.AssertNotFound()
	s.Contains(s.body(resp), "webhook not found")
}

func (s *WalletWebhookCreateGateTestSuite) TestWalletWebhookDeleteFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet webhook delete")
	s.seedChain()
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)

	denied := []struct {
		accountRole string
		walletRole  string
	}{
		{models.AccountRoleUser, models.WalletRoleViewer},
		{models.AccountRoleAuditor, models.WalletRoleViewer},
		{models.AccountRoleUser, models.WalletRoleSpender},
		{models.AccountRoleUser, models.WalletRoleApprover},
	}
	for _, caller := range denied {
		actor := s.member(caller.accountRole, account.ID)
		s.assign(actor.id, wallet.ID, caller.walletRole)
		hookURL := s.hookURL(caller.accountRole + "-" + caller.walletRole)
		created := s.createWebhook(owner.token, account.ID, wallet.ID, hookURL)
		created.AssertStatus(201)
		webhookID := s.createdWebhookID(created)
		resp := s.deleteWebhook(actor.token, account.ID, wallet.ID, webhookID)
		s.assertWebhookForbidden(resp)
		s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	}

	allowed := []struct {
		accountRole string
		walletRole  string
	}{
		{accountRole: models.AccountRoleOwner},
		{accountRole: models.AccountRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: "owner"},
	}
	for _, caller := range allowed {
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, wallet.ID, caller.walletRole)
		}
		hookURL := s.hookURL(caller.accountRole + "-" + caller.walletRole)
		created := s.createWebhook(owner.token, account.ID, wallet.ID, hookURL)
		created.AssertStatus(201)
		webhookID := s.createdWebhookID(created)
		resp := s.deleteWebhook(actor.token, account.ID, wallet.ID, webhookID)
		resp.AssertStatus(204)
		s.Empty(strings.TrimSpace(s.body(resp)))
		s.Equal(int64(0), s.webhookCount(wallet.ID, hookURL))
	}
}

func (s *WalletWebhookCreateGateTestSuite) TestWalletWebhookTestFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet webhook test")
	s.seedChain()
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)

	var deliveries atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		deliveries.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	s.T().Cleanup(server.Close)

	hookURL := server.URL + "/hooks/" + uuid.NewString()[:8]
	created := s.createWebhook(owner.token, account.ID, wallet.ID, hookURL)
	created.AssertStatus(201)
	webhookID := s.createdWebhookID(created)

	denied := []struct {
		accountRole string
		walletRole  string
	}{
		{models.AccountRoleUser, models.WalletRoleViewer},
		{models.AccountRoleAuditor, models.WalletRoleViewer},
		{models.AccountRoleUser, models.WalletRoleSpender},
		{models.AccountRoleUser, models.WalletRoleApprover},
	}
	for _, caller := range denied {
		actor := s.member(caller.accountRole, account.ID)
		s.assign(actor.id, wallet.ID, caller.walletRole)
		before := deliveries.Load()
		resp := s.testWebhook(actor.token, account.ID, wallet.ID, webhookID)
		s.assertWebhookForbidden(resp)
		s.Equal(before, deliveries.Load())
		s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	}

	allowed := []struct {
		accountRole string
		walletRole  string
	}{
		{accountRole: models.AccountRoleOwner},
		{accountRole: models.AccountRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: "owner"},
	}
	for _, caller := range allowed {
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, wallet.ID, caller.walletRole)
		}
		before := deliveries.Load()
		resp := s.testWebhook(actor.token, account.ID, wallet.ID, webhookID)
		resp.AssertOk()
		s.assertWebhookTestDelivered(resp)
		s.Equal(before+1, deliveries.Load())
		s.Equal(int64(1), s.webhookCount(wallet.ID, hookURL))
	}
}

func (s *WalletWebhookCreateGateTestSuite) TestCreateWalletWebhookValidationStaysUnprocessable() {
	account := mocks.InsertAccount(s.T(), "wallet webhook validation")
	s.seedChain()
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)

	resp := s.postWebhook(owner.token, account.ID, wallet.ID, `{}`)
	resp.AssertStatus(422)
	s.Contains(s.body(resp), `"validation_failed"`)
	s.Equal(int64(0), s.webhookCount(wallet.ID, ""))
}

type walletWebhookCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletWebhookCreateGateTestSuite) seedChain() {
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainETH, Name: models.ChainETH, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
}

func (s *WalletWebhookCreateGateTestSuite) member(role string, accountID uuid.UUID) walletWebhookCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletWebhookCaller{id: userID, token: s.login(userID)}
}

func (s *WalletWebhookCreateGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletWebhookCreateGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletWebhookCreateGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletWebhookCreateGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletWebhookCreateGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletWebhookCreateGatePassword)
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertStatus(200)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

func (s *WalletWebhookCreateGateTestSuite) hookURL(label string) string {
	return "https://example.com/hooks/" + label + "-" + uuid.NewString()[:8]
}

func (s *WalletWebhookCreateGateTestSuite) createWebhook(token string, accountID, walletID uuid.UUID, hookURL string) contractstesting.Response {
	body := fmt.Sprintf(`{"url":%q,"events":"deposit.confirmed"}`, hookURL)
	return s.postWebhook(token, accountID, walletID, body)
}

func (s *WalletWebhookCreateGateTestSuite) createdWebhookID(resp contractstesting.Response) uuid.UUID {
	var parsed struct {
		ID string `json:"id"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	id, err := uuid.Parse(parsed.ID)
	s.Require().NoError(err)
	return id
}

func (s *WalletWebhookCreateGateTestSuite) testWebhook(token string, accountID, walletID, webhookID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Post("/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String()+"/test", nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletWebhookCreateGateTestSuite) deleteWebhook(token string, accountID, walletID, webhookID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Delete("/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String(), nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletWebhookCreateGateTestSuite) postWebhook(token string, accountID, walletID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/wallets/"+walletID.String()+"/webhooks", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *WalletWebhookCreateGateTestSuite) assertWebhookTestDelivered(resp contractstesting.Response) {
	content := s.body(resp)
	s.NotContains(content, "secret")
	var parsed struct {
		Delivered bool `json:"delivered"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.True(parsed.Delivered)
}

func (s *WalletWebhookCreateGateTestSuite) assertWebhookForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("only wallet/account owners and admins may manage webhooks", parsed.Error.Message)
}

func (s *WalletWebhookCreateGateTestSuite) assertCreatedWebhook(resp contractstesting.Response, walletID uuid.UUID, hookURL string) {
	content := s.body(resp)
	var raw map[string]json.RawMessage
	s.Require().NoError(json.Unmarshal([]byte(content), &raw))
	_, hasSecret := raw["secret"]
	s.False(hasSecret)

	var parsed struct {
		ID       string `json:"id"`
		URL      string `json:"url"`
		WalletID string `json:"wallet_id"`
		Events   string `json:"events"`
		Type     string `json:"type"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.NotEmpty(parsed.ID)
	s.Equal(hookURL, parsed.URL)
	s.Equal(walletID.String(), parsed.WalletID)
	s.Equal("deposit.confirmed", parsed.Events)
	s.Equal("wallet", parsed.Type)
}

func (s *WalletWebhookCreateGateTestSuite) webhookCount(walletID uuid.UUID, hookURL string) int64 {
	var total int64
	if hookURL == "" {
		s.Require().NoError(facades.Orm().Query().Raw(
			`SELECT count(*) FROM webhook_configs WHERE wallet_id = ?`,
			walletID,
		).Scan(&total))
		return total
	}
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM webhook_configs WHERE wallet_id = ? AND url = ?`,
		walletID, hookURL,
	).Scan(&total))
	return total
}

func (s *WalletWebhookCreateGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
