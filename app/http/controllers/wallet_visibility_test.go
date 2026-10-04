package controllers_test

import (
	"encoding/json"
	"fmt"
	"strings"
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

const walletVisibilityPassword = "correct-horse-battery"

// WalletVisibilityTestSuite checks S3.4.2: view_all_wallets=false means a user
// or auditor sees only the wallets they belong to. Owner and admin still see
// every wallet of the account.
type WalletVisibilityTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
	account  models.Account
	assigned models.Wallet
	hidden   models.Wallet
	owner    sessionUser
	admin    sessionUser
	user     sessionUser
	auditor  sessionUser
}

type sessionUser struct {
	id    uuid.UUID
	token string
}

func TestWalletVisibilitySuite(t *testing.T) {
	suite.Run(t, new(WalletVisibilityTestSuite))
}

func (s *WalletVisibilityTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.account = mocks.InsertAccount(s.T(), "visibility")
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainBase, Name: models.ChainBase, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
	s.assigned = mocks.InsertWalletWithAccount(s.T(), models.ChainBase, &s.account.ID)
	s.hidden = mocks.InsertWalletWithAccount(s.T(), models.ChainBase, &s.account.ID)
	s.owner = s.member(models.AccountRoleOwner)
	s.admin = s.member(models.AccountRoleAdmin)
	s.user = s.member(models.AccountRoleUser)
	s.auditor = s.member(models.AccountRoleAuditor)
	s.assign(s.user.id, s.assigned.ID)
	s.assign(s.auditor.id, s.assigned.ID)
}

func (s *WalletVisibilityTestSuite) TestFlagOffHidesWalletsTheMemberDoesNotBelongTo() {
	for _, caller := range []sessionUser{s.owner, s.admin} {
		ids := s.listed(caller.token)
		s.ElementsMatch([]string{s.assigned.ID.String(), s.hidden.ID.String()}, ids, caller.id.String())
		s.get(caller.token, s.hidden.ID).AssertOk()
	}

	for _, caller := range []sessionUser{s.user, s.auditor} {
		ids := s.listed(caller.token)
		s.Equal([]string{s.assigned.ID.String()}, ids)
		s.get(caller.token, s.assigned.ID).AssertOk()
		hidden := s.get(caller.token, s.hidden.ID)
		hidden.AssertNotFound()
		s.bodyContains(hidden, "wallet not found")
		settings := s.getPath(caller.token, "/v1/wallets/"+s.hidden.ID.String()+"/settings")
		settings.AssertNotFound()
		s.bodyContains(settings, "wallet not found")
	}
}

func (s *WalletVisibilityTestSuite) TestFlagOnLetsUserAndAuditorSeeEveryWallet() {
	_, err := facades.Orm().Query().Exec(`UPDATE accounts SET view_all_wallets = TRUE WHERE id = ?`, s.account.ID)
	s.Require().NoError(err)

	for _, caller := range []sessionUser{s.user, s.auditor} {
		s.ElementsMatch([]string{s.assigned.ID.String(), s.hidden.ID.String()}, s.listed(caller.token))
		s.get(caller.token, s.hidden.ID).AssertOk()
	}
}

func (s *WalletVisibilityTestSuite) member(role string) sessionUser {
	hash, err := authsvc.NewService().HashPassword(walletVisibilityPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: s.account.ID, UserID: userID, Role: role, Status: "active",
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletVisibilityPassword)
	resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return sessionUser{id: userID, token: parsed.AccessToken}
}

func (s *WalletVisibilityTestSuite) assign(userID, walletID uuid.UUID) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active",
	}))
}

func (s *WalletVisibilityTestSuite) listed(token string) []string {
	resp := s.getPath(token, "/v1/wallets")
	resp.AssertOk()
	content, err := resp.Content()
	s.Require().NoError(err)
	var page struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
		Total int `json:"total"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &page))
	s.Equal(len(page.Data), page.Total)
	ids := make([]string, 0, len(page.Data))
	for _, item := range page.Data {
		ids = append(ids, item.ID)
	}
	return ids
}

func (s *WalletVisibilityTestSuite) get(token string, walletID uuid.UUID) contractstesting.Response {
	return s.getPath(token, "/v1/wallets/"+walletID.String())
}

func (s *WalletVisibilityTestSuite) getPath(token, path string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", s.account.ID.String()).
		Get(path)
	s.Require().NoError(err)
	return resp
}

func (s *WalletVisibilityTestSuite) bodyContains(resp contractstesting.Response, text string) {
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, text)
}
