package wallets

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const walletCanAddressPassword = "correct-horse-battery"

// WalletCanAddressTestSuite drives POST /v1/wallets/{walletId}/addresses through
// WalletCan. An over-long label stops in validation, after the permission check
// and before any address is derived.
type WalletCanAddressTestSuite struct {
	support.HTTPSuite
	account models.Account
}

func TestWallet_Can_AddressSuite(t *testing.T) {
	support.RunSuite(t, new(WalletCanAddressTestSuite))
}

func (s *WalletCanAddressTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.account = fixtures.InsertAccount(s.T(), "wallet-can")
	_, err := facades.Orm().Query().Exec(`UPDATE accounts SET view_all_wallets = TRUE WHERE id = ?`, s.account.ID)
	s.Require().NoError(err)
	s.account.ViewAllWallets = true
}

func (s *WalletCanAddressTestSuite) loginUser(role string) string {
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	hash, err := authsvc.NewService().HashPassword(walletCanAddressPassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: s.account.ID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletCanAddressPassword)
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

func (s *WalletCanAddressTestSuite) post(token, path, body string) contractstesting.Response {
	resp := s.Post(path, support.Session{AccessToken: token, AccountID: s.account.ID.String()}, body)
	return resp
}

func (s *WalletCanAddressTestSuite) assertForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("forbidden", parsed.Error.Message)
}

func (s *WalletCanAddressTestSuite) TestAddress_Create_FollowsWalletCan() {
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &s.account.ID)
	body := `{"label":"` + strings.Repeat("a", 256) + `"}`
	path := "/v1/wallets/" + wallet.ID.String() + "/addresses"

	for _, role := range []string{models.AccountRoleOwner, models.AccountRoleAdmin, models.AccountRoleUser} {
		resp := s.post(s.loginUser(role), path, body)
		resp.AssertStatus(422)
		content, err := resp.Content()
		s.Require().NoError(err)
		s.Contains(content, `"validation_failed"`, role)
		s.NotContains(content, `"message":"forbidden"`, role)
	}

	s.assertForbidden(s.post(s.loginUser(models.AccountRoleAuditor), path, body))
}

func (s *WalletCanAddressTestSuite) TestMissing_Wallet_IsNotFoundBeforeWalletCan() {
	token := s.loginUser(models.AccountRoleOwner)
	resp := s.post(token, "/v1/wallets/"+uuid.NewString()+"/addresses", `{"label":"desk"}`)
	resp.AssertNotFound()
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"code":"not_found"`)
	s.NotContains(content, `"message":"forbidden"`)
}

func (s *WalletCanAddressTestSuite) TestUser_Still_CannotMoveFunds() {
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &s.account.ID)
	token := s.loginUser(models.AccountRoleUser)
	for _, path := range []string{
		"/v1/wallets",
		"/v1/wallets/" + wallet.ID.String() + "/withdrawals",
		"/v1/wallets/" + wallet.ID.String() + "/consolidate",
	} {
		resp := s.post(token, path, `{}`)
		resp.AssertStatus(403)
		content, err := resp.Content()
		s.Require().NoError(err)
		s.NotContains(content, `"message":"forbidden"`, path)
	}
}
