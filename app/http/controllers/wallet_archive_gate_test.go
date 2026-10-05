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

const walletArchiveGatePassword = "correct-horse-battery"

// WalletArchiveGateTestSuite drives POST /v1/wallets/{walletId}/archive through
// WalletArchive, which runs after WalletContext. Wallet role owner or admin may
// archive, and so may account role owner or admin. Auditor, user, and the other
// wallet roles are refused before the wallet status changes. An account user
// with view_all_wallets false and no wallet membership is 404 from WalletContext.
type WalletArchiveGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWalletArchiveGateSuite(t *testing.T) {
	suite.Run(t, new(WalletArchiveGateTestSuite))
}

func (s *WalletArchiveGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WalletArchiveGateTestSuite) TestWalletArchiveFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet archive")
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
		resp := s.archive(actor.token, account.ID, wallet.ID)
		s.assertArchiveForbidden(resp)
		s.Equal("active", s.walletStatus(wallet.ID))
	}

	visible := mocks.InsertAccount(s.T(), "wallet archive visible")
	s.setViewAll(visible.ID, true)
	visibleWallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &visible.ID)
	for _, role := range []string{models.AccountRoleUser, models.AccountRoleAuditor} {
		actor := s.member(role, visible.ID)
		resp := s.archive(actor.token, visible.ID, visibleWallet.ID)
		s.assertArchiveForbidden(resp)
		s.Equal("active", s.walletStatus(visibleWallet.ID))
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
		ownWallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, ownWallet.ID, caller.walletRole)
		}
		resp := s.archive(actor.token, account.ID, ownWallet.ID)
		resp.AssertOk()
		s.Equal(models.WalletStatusArchived, s.jsonBody(resp)["status"])
		s.Equal(models.WalletStatusArchived, s.walletStatus(ownWallet.ID))
	}
}

func (s *WalletArchiveGateTestSuite) TestWalletArchiveStaysHiddenFromAnAccountUser() {
	account := mocks.InsertAccount(s.T(), "wallet archive hidden")
	wallet := mocks.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	actor := s.member(models.AccountRoleUser, account.ID)

	resp := s.archive(actor.token, account.ID, wallet.ID)
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
	s.Equal("active", s.walletStatus(wallet.ID))
}

type walletArchiveCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletArchiveGateTestSuite) member(role string, accountID uuid.UUID) walletArchiveCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletArchiveCaller{id: userID, token: s.login(userID)}
}

func (s *WalletArchiveGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletArchiveGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletArchiveGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletArchiveGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletArchiveGateTestSuite) setViewAll(accountID uuid.UUID, viewAll bool) {
	_, err := facades.Orm().Query().Exec(
		`UPDATE accounts SET view_all_wallets = ? WHERE id = ?`, viewAll, accountID,
	)
	s.Require().NoError(err)
}

func (s *WalletArchiveGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletArchiveGatePassword)
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

func (s *WalletArchiveGateTestSuite) archive(token string, accountID, walletID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Post("/v1/wallets/"+walletID.String()+"/archive", nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletArchiveGateTestSuite) assertArchiveForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("only wallet/account owners and admins may archive wallets", parsed.Error.Message)
}

func (s *WalletArchiveGateTestSuite) walletStatus(walletID uuid.UUID) string {
	var stored models.Wallet
	s.Require().NoError(facades.Orm().Query().Where("id = ?", walletID).First(&stored))
	return stored.Status
}

func (s *WalletArchiveGateTestSuite) jsonBody(resp contractstesting.Response) map[string]any {
	parsed := map[string]any{}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	return parsed
}

func (s *WalletArchiveGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
