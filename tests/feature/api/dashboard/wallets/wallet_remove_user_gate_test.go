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

const walletRemoveUserGatePassword = "correct-horse-battery"

// WalletRemoveUserGateTestSuite drives DELETE /v1/wallets/{walletId}/users/{userId}
// through the route gate. Wallet role owner or admin may remove a member, and
// so may account role owner or admin. Auditor, user, and the other wallet
// roles are refused before the membership is removed.
type WalletRemoveUserGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWalletRemoveUserGateSuite(t *testing.T) {
	suite.Run(t, new(WalletRemoveUserGateTestSuite))
}

func (s *WalletRemoveUserGateTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WalletRemoveUserGateTestSuite) TestWalletRemoveUserFollowsTheLoadedRoles() {
	account := mocks.InsertAccount(s.T(), "wallet remove user")
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
		target := s.insertUser(caller.accountRole + "-target-" + uuid.NewString()[:8] + "@example.com")
		s.accountMember(account.ID, target, models.AccountRoleUser)
		s.assign(target, wallet.ID, models.WalletRoleViewer)
		resp := s.removeUser(actor.token, account.ID, wallet.ID, target)
		s.assertRemoveForbidden(resp)
		s.Equal(int64(1), s.activeWalletMembershipCount(wallet.ID, target))
		s.Equal(models.WalletRoleViewer, s.storedWalletRoles(wallet.ID, target))
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
		target := s.insertUser(caller.accountRole + "-target-" + uuid.NewString()[:8] + "@example.com")
		s.accountMember(account.ID, target, models.AccountRoleUser)
		s.assign(target, wallet.ID, models.WalletRoleViewer)
		resp := s.removeUser(actor.token, account.ID, wallet.ID, target)
		resp.AssertStatus(204)
		s.Empty(strings.TrimSpace(s.body(resp)))
		s.Equal(int64(0), s.activeWalletMembershipCount(wallet.ID, target))
		s.Equal(int64(1), s.walletMembershipCount(wallet.ID, target))
	}
}

type walletRemoveUserCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletRemoveUserGateTestSuite) seedChain() {
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainETH, Name: models.ChainETH, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
}

func (s *WalletRemoveUserGateTestSuite) member(role string, accountID uuid.UUID) walletRemoveUserCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletRemoveUserCaller{id: userID, token: s.login(userID)}
}

func (s *WalletRemoveUserGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletRemoveUserGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletRemoveUserGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletRemoveUserGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletRemoveUserGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletRemoveUserGatePassword)
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

func (s *WalletRemoveUserGateTestSuite) removeUser(token string, accountID, walletID, targetID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Delete("/v1/wallets/"+walletID.String()+"/users/"+targetID.String(), nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletRemoveUserGateTestSuite) assertRemoveForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("only wallet/account owners and admins may add wallet users", parsed.Error.Message)
}

func (s *WalletRemoveUserGateTestSuite) activeWalletMembershipCount(walletID, userID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM wallet_users WHERE wallet_id = ? AND user_id = ? AND deleted_at IS NULL`,
		walletID, userID,
	).Scan(&total))
	return total
}

func (s *WalletRemoveUserGateTestSuite) walletMembershipCount(walletID, userID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM wallet_users WHERE wallet_id = ? AND user_id = ?`,
		walletID, userID,
	).Scan(&total))
	return total
}

func (s *WalletRemoveUserGateTestSuite) storedWalletRoles(walletID, userID uuid.UUID) string {
	var roles string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT roles FROM wallet_users WHERE wallet_id = ? AND user_id = ? AND deleted_at IS NULL`,
		walletID, userID,
	).Scan(&roles))
	return roles
}

func (s *WalletRemoveUserGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
