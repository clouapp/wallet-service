package wallets

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/google/uuid"
	contractstesting "github.com/goravel/framework/contracts/testing/http"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	"github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const walletAddUserGatePassword = "correct-horse-battery"

// WalletAddUserGateTestSuite drives POST /v1/wallets/{walletId}/users through
// the route gate. Wallet role owner or admin may add a member, and so may
// account role owner or admin. Auditor, user, and the other wallet roles are
// refused before a wallet membership is written.
type WalletAddUserGateTestSuite struct {
	support.HTTPSuite
}

func TestWallet_Add_UserGateSuite(t *testing.T) {
	support.RunSuite(t, new(WalletAddUserGateTestSuite))
}

func (s *WalletAddUserGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *WalletAddUserGateTestSuite) TestWallet_Add_UserFollowsTheLoadedRoles() {
	account := fixtures.InsertAccount(s.T(), "wallet add user")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)

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
		resp := s.addUser(actor.token, account.ID, wallet.ID, target, models.WalletRoleViewer)
		s.assertAddForbidden(resp)
		s.Equal(int64(0), s.walletMembershipCount(wallet.ID, target))
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
		resp := s.addUser(actor.token, account.ID, wallet.ID, target, models.WalletRoleViewer)
		resp.AssertStatus(201)
		s.assertCreatedViewer(resp, target)
		s.Equal(int64(1), s.walletMembershipCount(wallet.ID, target))
		s.Equal(models.WalletRoleViewer, s.storedWalletRoles(wallet.ID, target))
	}
}

func (s *WalletAddUserGateTestSuite) TestAdd_Wallet_UserValidationStaysUnprocessable() {
	account := fixtures.InsertAccount(s.T(), "wallet add user validation")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)
	target := s.insertUser("invalid-role-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(account.ID, target, models.AccountRoleUser)

	resp := s.addUser(owner.token, account.ID, wallet.ID, target, "owner")
	resp.AssertStatus(422)
	s.AssertError(resp, 422, "unprocessable", "roles must be a set of admin, spender, approver, viewer")
	s.Equal(int64(0), s.walletMembershipCount(wallet.ID, target))
}

type walletAddUserCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletAddUserGateTestSuite) seedChain() {
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainETH, Name: models.ChainETH, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
}

func (s *WalletAddUserGateTestSuite) member(role string, accountID uuid.UUID) walletAddUserCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletAddUserCaller{id: userID, token: s.login(userID)}
}

func (s *WalletAddUserGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletAddUserGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletAddUserGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletAddUserGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletAddUserGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletAddUserGatePassword)
	resp := s.Post("/v1/auth/login", support.Session{}, body)
	resp.AssertStatus(200)
	var parsed struct {
		AccessToken string `json:"access_token"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Require().NotEmpty(parsed.AccessToken)
	return parsed.AccessToken
}

func (s *WalletAddUserGateTestSuite) addUser(token string, accountID, walletID, targetID uuid.UUID, roles string) contractstesting.Response {
	body := fmt.Sprintf(`{"user_id":%q,"roles":%q}`, targetID.String(), roles)
	resp := s.Post("/v1/wallets/"+walletID.String()+"/users", support.Session{AccessToken: token, AccountID: accountID.String()}, body)
	return resp
}

func (s *WalletAddUserGateTestSuite) assertAddForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	s.AssertError(resp, 403, "forbidden", "only wallet/account owners and admins may add wallet users")
}

func (s *WalletAddUserGateTestSuite) assertCreatedViewer(resp contractstesting.Response, targetID uuid.UUID) {
	var parsed struct {
		UserID string `json:"user_id"`
		Roles  string `json:"roles"`
		Status string `json:"status"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal(targetID.String(), parsed.UserID)
	s.Equal(models.WalletRoleViewer, parsed.Roles)
	s.Equal("active", parsed.Status)
}

func (s *WalletAddUserGateTestSuite) walletMembershipCount(walletID, userID uuid.UUID) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM wallet_users WHERE wallet_id = ? AND user_id = ? AND deleted_at IS NULL`,
		walletID, userID,
	).Scan(&total))
	return total
}

func (s *WalletAddUserGateTestSuite) storedWalletRoles(walletID, userID uuid.UUID) string {
	var roles string
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT roles FROM wallet_users WHERE wallet_id = ? AND user_id = ? AND deleted_at IS NULL`,
		walletID, userID,
	).Scan(&roles))
	return roles
}

func (s *WalletAddUserGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
