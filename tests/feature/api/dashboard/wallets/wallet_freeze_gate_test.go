package wallets

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

const walletFreezeGatePassword = "correct-horse-battery"

// WalletFreezeGateTestSuite drives POST /v1/wallets/{walletId}/freeze through
// WalletFreeze, which runs after WalletContext. Wallet role owner may freeze,
// and so may account role owner or admin. A wallet admin may not. Auditor,
// user, and the other wallet roles are refused before the wallet is frozen.
// An account user with view_all_wallets false and no wallet membership is 404
// from WalletContext.
type WalletFreezeGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWalletFreezeGateSuite(t *testing.T) {
	suite.Run(t, new(WalletFreezeGateTestSuite))
}

func (s *WalletFreezeGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WalletFreezeGateTestSuite) TestWalletFreezeFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet freeze")
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)

	denied := []struct {
		accountRole string
		walletRole  string
	}{
		{models.AccountRoleUser, models.WalletRoleViewer},
		{models.AccountRoleAuditor, models.WalletRoleViewer},
		{models.AccountRoleUser, models.WalletRoleSpender},
		{models.AccountRoleUser, models.WalletRoleApprover},
		{models.AccountRoleUser, models.WalletRoleAdmin},
	}
	for _, caller := range denied {
		actor := s.member(caller.accountRole, account.ID)
		s.assign(actor.id, wallet.ID, caller.walletRole)
		resp := s.freeze(actor.token, account.ID, wallet.ID)
		s.assertFreezeForbidden(resp)
		s.assertUnfrozen(wallet.ID)
	}

	visible := mocks.InsertAccount(s.T(), "wallet freeze visible")
	s.setViewAll(visible.ID, true)
	visibleWallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &visible.ID)
	for _, role := range []string{models.AccountRoleUser, models.AccountRoleAuditor} {
		actor := s.member(role, visible.ID)
		resp := s.freeze(actor.token, visible.ID, visibleWallet.ID)
		s.assertFreezeForbidden(resp)
		s.assertUnfrozen(visibleWallet.ID)
	}

	allowed := []struct {
		accountRole string
		walletRole  string
	}{
		{accountRole: models.AccountRoleOwner},
		{accountRole: models.AccountRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: "owner"},
	}
	for _, caller := range allowed {
		ownWallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, ownWallet.ID, caller.walletRole)
		}
		invalid := s.freezeBody(actor.token, account.ID, ownWallet.ID, `{"frozen_until":"not-a-timestamp"}`)
		invalid.AssertStatus(422)
		s.assertUnfrozen(ownWallet.ID)

		resp := s.freezeBody(actor.token, account.ID, ownWallet.ID, `{}`)
		resp.AssertOk()
		s.Equal("frozen", s.jsonBody(resp)["status"])
		s.NotEmpty(s.jsonBody(resp)["frozen_until"])
		status, frozen := s.freezeState(ownWallet.ID)
		s.Equal("frozen", status)
		s.True(frozen)
	}
}

func (s *WalletFreezeGateTestSuite) TestWalletFreezeStaysHiddenFromAnAccountUser() {
	account := mocks.InsertAccount(s.T(), "wallet freeze hidden")
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	actor := s.member(models.AccountRoleUser, account.ID)

	resp := s.freeze(actor.token, account.ID, wallet.ID)
	resp.AssertStatus(404)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("not_found", parsed.Error.Code)
	s.Equal("wallet not found", parsed.Error.Message)
	s.assertUnfrozen(wallet.ID)
}

type walletFreezeCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletFreezeGateTestSuite) member(role string, accountID uuid.UUID) walletFreezeCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletFreezeCaller{id: userID, token: s.login(userID)}
}

func (s *WalletFreezeGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletFreezeGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletFreezeGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletFreezeGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletFreezeGateTestSuite) setViewAll(accountID uuid.UUID, viewAll bool) {
	_, err := facades.Orm().Query().Exec(
		`UPDATE accounts SET view_all_wallets = ? WHERE id = ?`, viewAll, accountID,
	)
	s.Require().NoError(err)
}

func (s *WalletFreezeGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletFreezeGatePassword)
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

func (s *WalletFreezeGateTestSuite) freeze(token string, accountID, walletID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Post("/v1/wallets/"+walletID.String()+"/freeze", nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletFreezeGateTestSuite) freezeBody(token string, accountID, walletID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("Content-Type", "application/json").
		WithHeader("X-Account-Id", accountID.String()).
		Post("/v1/wallets/"+walletID.String()+"/freeze", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *WalletFreezeGateTestSuite) assertFreezeForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("only owners and account admins may freeze wallets", parsed.Error.Message)
}

func (s *WalletFreezeGateTestSuite) assertUnfrozen(walletID uuid.UUID) {
	status, frozen := s.freezeState(walletID)
	s.Equal("active", status)
	s.False(frozen)
}

func (s *WalletFreezeGateTestSuite) freezeState(walletID uuid.UUID) (string, bool) {
	var stored models.Wallet
	s.Require().NoError(facades.Orm().Query().Where("id = ?", walletID).First(&stored))
	return stored.Status, stored.FrozenUntil != nil
}

func (s *WalletFreezeGateTestSuite) jsonBody(resp contractstesting.Response) map[string]any {
	parsed := map[string]any{}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	return parsed
}

func (s *WalletFreezeGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
