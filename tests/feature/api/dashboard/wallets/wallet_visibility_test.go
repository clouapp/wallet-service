package wallets

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const walletVisibilityPassword = "correct-horse-battery"

// WalletVisibilityTestSuite checks S3.4.2: view_all_wallets=false means a user
// or auditor sees only the wallets they belong to. Owner and admin still see
// every wallet of the account.
type WalletVisibilityTestSuite struct {
	support.HTTPSuite
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

func TestWallet_Visibility_Suite(t *testing.T) {
	support.RunSuite(t, new(WalletVisibilityTestSuite))
}

func (s *WalletVisibilityTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.account = fixtures.InsertAccount(s.T(), "visibility")
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainBase, Name: models.ChainBase, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
	s.assigned = fixtures.InsertWalletWithAccount(s.T(), models.ChainBase, &s.account.ID)
	s.hidden = fixtures.InsertWalletWithAccount(s.T(), models.ChainBase, &s.account.ID)
	s.owner = s.member(models.AccountRoleOwner)
	s.admin = s.member(models.AccountRoleAdmin)
	s.user = s.member(models.AccountRoleUser)
	s.auditor = s.member(models.AccountRoleAuditor)
	s.assign(s.user.id, s.assigned.ID)
	s.assign(s.auditor.id, s.assigned.ID)
}

func (s *WalletVisibilityTestSuite) TestFlag_Off_HidesWalletsTheMemberDoesNotBelongTo() {
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
		s.AssertError(hidden, 404, "not_found", "wallet not found")
		settings := s.getPath(caller.token, "/v1/wallets/"+s.hidden.ID.String()+"/settings")
		s.AssertError(settings, 404, "not_found", "wallet not found")
	}
}

func (s *WalletVisibilityTestSuite) TestFlag_On_LetsUserAndAuditorSeeEveryWallet() {
	_, err := facades.Orm().Query().Exec(`UPDATE accounts SET view_all_wallets = TRUE WHERE id = ?`, s.account.ID)
	s.Require().NoError(err)

	for _, caller := range []sessionUser{s.user, s.auditor} {
		s.ElementsMatch([]string{s.assigned.ID.String(), s.hidden.ID.String()}, s.listed(caller.token))
		s.get(caller.token, s.hidden.ID).AssertOk()
	}
}

func (s *WalletVisibilityTestSuite) member(role string) sessionUser {
	hash, err := authsvc.NewService(appfacades.Hash()).HashPassword(walletVisibilityPassword)
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
	resp := s.Post("/v1/auth/login", support.Session{}, body)
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
	resp := s.Get(path, support.Session{AccessToken: token, AccountID: s.account.ID.String()})
	return resp
}

func (s *WalletVisibilityTestSuite) bodyContains(resp contractstesting.Response, text string) {
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, text)
}
