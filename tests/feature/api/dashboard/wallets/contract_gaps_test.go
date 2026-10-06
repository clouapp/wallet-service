package wallets

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/mocks"
)

const contractGapPassword = "correct-horse-battery"

type contractGapsSuite struct {
	suite.Suite
	goravelTesting.TestCase

	accountID uuid.UUID
	ownerID   uuid.UUID
	token     string
}

func TestContractGapsSuite(t *testing.T) {
	suite.Run(t, new(contractGapsSuite))
}

func (s *contractGapsSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.accountID = uuid.Nil
	s.accountID, s.ownerID, s.token = s.seedSession("owner")
}

func (s *contractGapsSuite) seedSession(role string) (uuid.UUID, uuid.UUID, string) {
	s.T().Helper()

	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(contractGapPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)

	accountID := s.accountID
	if accountID == uuid.Nil {
		accountID = uuid.New()
		s.Require().NoError(facades.Orm().Query().Create(&models.Account{
			ID: accountID, Name: "contract gaps", Status: "active", Environment: models.EnvironmentProd,
		}))
	}
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: "active",
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, contractGapPassword)
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var session struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &session))
	s.Require().NotEmpty(session.AccessToken)
	return accountID, userID, session.AccessToken
}

func (s *contractGapsSuite) seedWallet(label string) uuid.UUID {
	s.T().Helper()

	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            label,
		MPCCustomerShare: "aa",
		MPCShareIV:       "bb",
		MPCShareSalt:     "cc",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:contract-gap",
		MPCPublicKey:     "02",
		MPCCurve:         "secp256k1",
		MPCChainCode:     "dd",
		AccountID:        &s.accountID,
		Status:           "active",
	}))
	return walletID
}

func (s *contractGapsSuite) seedWebhook(walletID uuid.UUID, url, secret string) uuid.UUID {
	s.T().Helper()

	webhookID := uuid.New()
	sealed, err := settings.Seal(appfacades.Crypt(), secret)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.WebhookConfig{
		ID:       webhookID,
		URL:      url,
		Secret:   sealed,
		Events:   "deposit.confirmed",
		IsActive: true,
		WalletID: &walletID,
		Type:     "wallet",
	}))
	return webhookID
}

func (s *contractGapsSuite) seedWithdrawal(walletID uuid.UUID) uuid.UUID {
	s.T().Helper()

	withdrawalID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Withdrawal{
		ID:                 withdrawalID,
		WalletID:           walletID,
		AccountID:          &s.accountID,
		Status:             "pending",
		Amount:             "1",
		FeeEstimate:        "0",
		DestinationAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
	}))
	return withdrawalID
}

func (s *contractGapsSuite) call(method, path, token, body string) contractstesting.Response {
	s.T().Helper()

	req := s.Http(s.T()).WithHeader("Content-Type", "application/json")
	if token != "" {
		req = req.WithHeader("Authorization", "Bearer "+token).
			WithHeader("X-Account-Id", s.accountID.String())
	}
	var (
		resp contractstesting.Response
		err  error
	)
	reader := strings.NewReader(body)
	switch method {
	case http.MethodGet:
		resp, err = req.Get(path)
	case http.MethodPost:
		resp, err = req.Post(path, reader)
	case http.MethodPatch:
		resp, err = req.Patch(path, reader)
	default:
		s.FailNow("unsupported method " + method)
	}
	s.Require().NoError(err)
	return resp
}

func (s *contractGapsSuite) errorText(body map[string]any) string {
	s.T().Helper()
	switch value := body["error"].(type) {
	case string:
		return value
	case map[string]any:
		message, _ := value["message"].(string)
		return message
	default:
		return ""
	}
}

func (s *contractGapsSuite) jsonBody(resp contractstesting.Response) map[string]any {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	parsed := map[string]any{}
	if content != "" {
		s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	}
	return parsed
}

func (s *contractGapsSuite) TestArchiveWallet_Unauthenticated() {
	walletID := s.seedWallet("archive auth")
	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/archive", "", "")
	resp.AssertUnauthorized()
}

func (s *contractGapsSuite) TestArchiveWallet_ForbiddenForAccountUser() {
	walletID := s.seedWallet("archive forbidden")
	_, _, token := s.seedSession("user")

	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/archive", token, "")
	resp.AssertForbidden()
	s.Equal("only wallet/account owners and admins may archive wallets", s.errorText(s.jsonBody(resp)))
}

func (s *contractGapsSuite) TestArchiveWallet_ArchivesThenRejectsASecondCall() {
	walletID := s.seedWallet("archive twice")
	path := "/v1/wallets/" + walletID.String() + "/archive"

	first := s.call(http.MethodPost, path, s.token, "")
	first.AssertOk()
	s.Equal(models.WalletStatusArchived, s.jsonBody(first)["status"])

	var stored models.Wallet
	s.Require().NoError(facades.Orm().Query().Where("id = ?", walletID).First(&stored))
	s.Equal(models.WalletStatusArchived, stored.Status)

	second := s.call(http.MethodPost, path, s.token, "")
	second.AssertStatus(http.StatusConflict)
	s.Equal("wallet already archived", s.errorText(s.jsonBody(second)))
}

func (s *contractGapsSuite) TestUpdateWalletSettings_PersistsLabel() {
	walletID := s.seedWallet("old name")
	resp := s.call(http.MethodPatch, "/v1/wallets/"+walletID.String()+"/settings", s.token, `{"label":"Desk"}`)
	resp.AssertOk()
	s.Equal("Desk", s.jsonBody(resp)["label"])

	var stored models.Wallet
	s.Require().NoError(facades.Orm().Query().Where("id = ?", walletID).First(&stored))
	s.Equal("Desk", stored.Label)
}

func (s *contractGapsSuite) TestWebhookTest_Unauthenticated() {
	walletID := s.seedWallet("webhook auth")
	webhookID := s.seedWebhook(walletID, "http://127.0.0.1:1/hook", "secret")
	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String()+"/test", "", "")
	resp.AssertUnauthorized()
}

func (s *contractGapsSuite) captureLogs() *bytes.Buffer {
	s.T().Helper()
	logs := &bytes.Buffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	s.T().Cleanup(func() { slog.SetDefault(previous) })
	return logs
}

func (s *contractGapsSuite) TestWebhookTest_SignsTheBodyLikeANormalDelivery() {
	const secret = "test-webhook-secret-not-logged"
	logs := s.captureLogs()
	var (
		gotBody []byte
		gotSig  string
		gotEvt  string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		s.Require().NoError(err)
		gotBody = body
		gotSig = r.Header.Get("X-Vault-Signature")
		gotEvt = r.Header.Get("X-Vault-Event")
		s.Equal("application/json", r.Header.Get("Content-Type"))
		s.NotEmpty(r.Header.Get("X-Vault-Delivery-Id"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	walletID := s.seedWallet("webhook happy")
	webhookID := s.seedWebhook(walletID, server.URL, secret)
	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String()+"/test", s.token, "")
	resp.AssertOk()
	body := s.jsonBody(resp)
	s.Equal(true, body["delivered"])
	s.NotContains(contractGapJSON(s.T(), body), secret)
	s.NotContains(logs.String(), secret)

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(gotBody)
	s.Equal(hex.EncodeToString(mac.Sum(nil)), gotSig)
	s.Equal("webhook.test", gotEvt)

	var payload map[string]any
	s.Require().NoError(json.Unmarshal(gotBody, &payload))
	s.Equal("webhook.test", payload["type"])
	data, _ := payload["data"].(map[string]any)
	s.Equal(walletID.String(), data["wallet_id"])
	s.Equal(webhookID.String(), data["webhook_id"])
}

func (s *contractGapsSuite) TestWebhookTest_RefusedURLIsAnError() {
	const secret = "refused-webhook-secret"
	logs := s.captureLogs()
	closed := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := closed.URL
	closed.Close()

	walletID := s.seedWallet("webhook refused")
	webhookID := s.seedWebhook(walletID, url, secret)
	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/webhooks/"+webhookID.String()+"/test", s.token, "")
	resp.AssertStatus(http.StatusBadGateway)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(content, secret)
	s.NotContains(logs.String(), secret)
	s.Equal("webhook test delivery failed", s.errorText(s.jsonBody(resp)))
}

func (s *contractGapsSuite) TestGetWithdrawal_Unauthenticated() {
	resp := s.call(http.MethodGet, "/v1/withdrawals/"+uuid.NewString(), "", "")
	resp.AssertUnauthorized()
}

func (s *contractGapsSuite) TestGetWithdrawal_ReturnsTheAccountWithdrawal() {
	walletID := s.seedWallet("withdrawal happy")
	withdrawalID := s.seedWithdrawal(walletID)

	resp := s.call(http.MethodGet, "/v1/withdrawals/"+withdrawalID.String(), s.token, "")
	resp.AssertOk()
	body := s.jsonBody(resp)
	s.Equal(withdrawalID.String(), body["id"])
	s.Equal(walletID.String(), body["wallet_id"])
	s.Equal("pending", body["status"])
}

func (s *contractGapsSuite) TestGetWithdrawal_UnknownIDIsNotFound() {
	resp := s.call(http.MethodGet, "/v1/withdrawals/"+uuid.NewString(), s.token, "")
	resp.AssertNotFound()
	s.Equal("withdrawal not found", s.errorText(s.jsonBody(resp)))
}

func (s *contractGapsSuite) TestAddWalletUser_Unauthenticated() {
	walletID := s.seedWallet("wallet user auth")
	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/users", "", `{"user_id":"`+uuid.NewString()+`","roles":"view"}`)
	resp.AssertUnauthorized()
}

func (s *contractGapsSuite) TestAddWalletUser_AddsAnActiveAccountMember() {
	walletID := s.seedWallet("wallet user happy")
	_, memberID, _ := s.seedSession("user")

	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/users", s.token,
		fmt.Sprintf(`{"user_id":%q,"roles":" viewer, spender "}`, memberID.String()))
	resp.AssertCreated()
	body := s.jsonBody(resp)
	s.Equal(memberID.String(), body["user_id"])
	s.Equal("viewer,spender", body["roles"])
}

func (s *contractGapsSuite) TestAddWalletUser_RejectsAnUnknownRole() {
	walletID := s.seedWallet("wallet user role")
	_, memberID, _ := s.seedSession("user")

	for _, roles := range []string{"view", "spend", "owner", "auditor", "viewer,nope"} {
		resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/users", s.token,
			fmt.Sprintf(`{"user_id":%q,"roles":%q}`, memberID.String(), roles))
		resp.AssertUnprocessableEntity()
		s.Equal("roles must be a set of admin, spender, approver, viewer", s.errorText(s.jsonBody(resp)))
	}

	listed := s.call(http.MethodGet, "/v1/wallets/"+walletID.String()+"/users", s.token, "")
	listed.AssertOk()
	data, _ := s.jsonBody(listed)["data"].([]any)
	s.Empty(data)
}

func (s *contractGapsSuite) TestAddWalletUser_RejectsUserWhoIsNotAMember() {
	walletID := s.seedWallet("wallet user outsider")
	outsiderID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(contractGapPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		outsiderID, "outsider-"+outsiderID.String()[:8]+"@example.com", hash, "active",
	)
	s.Require().NoError(err)

	resp := s.call(http.MethodPost, "/v1/wallets/"+walletID.String()+"/users", s.token,
		fmt.Sprintf(`{"user_id":%q,"roles":"viewer"}`, outsiderID.String()))
	resp.AssertUnprocessableEntity()
	s.Equal("user is not an active member of this account", s.errorText(s.jsonBody(resp)))
}

func contractGapJSON(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}
