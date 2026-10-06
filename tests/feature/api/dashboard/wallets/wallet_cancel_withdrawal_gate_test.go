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
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const walletCancelWithdrawalGatePassword = "correct-horse-battery"

// WalletCancelWithdrawalGateTestSuite drives
// POST /v1/wallets/{walletId}/withdrawals/{withdrawalId}/cancel through
// WalletCancelWithdrawal, which runs after WalletContext. A non-auditor
// creator may cancel their own pending withdrawal. Wallet role owner or
// admin may cancel any, and so may account role owner or admin. An account
// auditor is denied even when they created the withdrawal. A caller who can
// see the wallet but may not cancel is 403, and the withdrawal stays
// pending. An account user with view_all_wallets false and no wallet
// membership is 404 from WalletContext.
type WalletCancelWithdrawalGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWallet_Cancel_WithdrawalGateSuite(t *testing.T) {
	suite.Run(t, new(WalletCancelWithdrawalGateTestSuite))
}

func (s *WalletCancelWithdrawalGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *WalletCancelWithdrawalGateTestSuite) TestWallet_Cancel_WithdrawalFollowsTheLoadedRoles() {
	account := fixtures.InsertAccount(s.T(), "wallet cancel")
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	someoneElse := uuid.New()

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
		withdrawalID := s.pending(wallet.ID, account.ID, &someoneElse)
		resp := s.cancel(actor.token, account.ID, wallet.ID, withdrawalID)
		s.assertCancelForbidden(resp)
		s.Equal("pending", s.withdrawalStatus(withdrawalID))
	}

	visible := fixtures.InsertAccount(s.T(), "wallet cancel visible")
	s.setViewAll(visible.ID, true)
	visibleWallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &visible.ID)
	for _, role := range []string{models.AccountRoleUser, models.AccountRoleAuditor} {
		actor := s.member(role, visible.ID)
		withdrawalID := s.pending(visibleWallet.ID, visible.ID, &someoneElse)
		resp := s.cancel(actor.token, visible.ID, visibleWallet.ID, withdrawalID)
		s.assertCancelForbidden(resp)
		s.Equal("pending", s.withdrawalStatus(withdrawalID))
	}

	allowed := []struct {
		accountRole string
		walletRole  string
		creator     bool
	}{
		{accountRole: models.AccountRoleOwner},
		{accountRole: models.AccountRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: "owner"},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleAdmin},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleViewer, creator: true},
		{accountRole: models.AccountRoleUser, walletRole: models.WalletRoleSpender, creator: true},
	}
	for _, caller := range allowed {
		ownWallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
		actor := s.member(caller.accountRole, account.ID)
		if caller.walletRole != "" {
			s.assign(actor.id, ownWallet.ID, caller.walletRole)
		}
		creatorID := someoneElse
		if caller.creator {
			creatorID = actor.id
		}
		held := s.insertWithdrawal(ownWallet.ID, account.ID, models.WithdrawalStatusConfirmed, &creatorID)
		refused := s.cancel(actor.token, account.ID, ownWallet.ID, held)
		refused.AssertStatus(422)
		s.Equal("only pending withdrawals can be cancelled", s.errorMessage(refused))
		s.Equal(models.WithdrawalStatusConfirmed, s.withdrawalStatus(held))

		withdrawalID := s.pending(ownWallet.ID, account.ID, &creatorID)
		resp := s.cancel(actor.token, account.ID, ownWallet.ID, withdrawalID)
		resp.AssertOk()
		body := s.jsonBody(resp)
		s.Equal("cancelled", body["status"])
		s.Equal(withdrawalID.String(), body["id"])
		s.Equal("cancelled", s.withdrawalStatus(withdrawalID))
	}
}

func (s *WalletCancelWithdrawalGateTestSuite) TestWallet_Cancel_WithdrawalDeniesTheAuditorWhoCreatedIt() {
	account := fixtures.InsertAccount(s.T(), "wallet cancel auditor creator")
	s.setViewAll(account.ID, true)
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)

	auditor := s.member(models.AccountRoleAuditor, account.ID)
	createdByAuditor := s.pending(wallet.ID, account.ID, &auditor.id)
	denied := s.cancel(auditor.token, account.ID, wallet.ID, createdByAuditor)
	s.assertCancelForbidden(denied)
	s.Equal("pending", s.withdrawalStatus(createdByAuditor))

	creator := s.member(models.AccountRoleUser, account.ID)
	s.assign(creator.id, wallet.ID, models.WalletRoleViewer)
	own := s.pending(wallet.ID, account.ID, &creator.id)
	cancelled := s.cancel(creator.token, account.ID, wallet.ID, own)
	cancelled.AssertOk()
	s.Equal("cancelled", s.withdrawalStatus(own))

	owner := s.member(models.AccountRoleOwner, account.ID)
	ownerCancel := s.cancel(owner.token, account.ID, wallet.ID, createdByAuditor)
	ownerCancel.AssertOk()
	s.Equal("cancelled", s.withdrawalStatus(createdByAuditor))
}

func (s *WalletCancelWithdrawalGateTestSuite) TestWallet_Cancel_WithdrawalStaysHiddenFromAnAccountUser() {
	account := fixtures.InsertAccount(s.T(), "wallet cancel hidden")
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	creator := uuid.New()
	withdrawalID := s.pending(wallet.ID, account.ID, &creator)
	actor := s.member(models.AccountRoleUser, account.ID)

	resp := s.cancel(actor.token, account.ID, wallet.ID, withdrawalID)
	resp.AssertStatus(404)
	s.Equal("not_found", s.errorCode(resp))
	s.Equal("wallet not found", s.errorMessage(resp))
	s.Equal("pending", s.withdrawalStatus(withdrawalID))
}

func (s *WalletCancelWithdrawalGateTestSuite) TestMissing_Withdrawal_StaysNotFoundForAViewer() {
	account := fixtures.InsertAccount(s.T(), "wallet cancel missing")
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	actor := s.member(models.AccountRoleUser, account.ID)
	s.assign(actor.id, wallet.ID, models.WalletRoleViewer)
	kept := s.pending(wallet.ID, account.ID, nil)

	missing := s.cancel(actor.token, account.ID, wallet.ID, uuid.New())
	missing.AssertStatus(404)
	s.Equal("withdrawal not found", s.errorMessage(missing))
	s.Equal("pending", s.withdrawalStatus(kept))

	invalid := s.cancelPath(actor.token, account.ID, "/v1/wallets/"+wallet.ID.String()+"/withdrawals/not-a-uuid/cancel")
	invalid.AssertStatus(400)
	s.Equal("invalid withdrawal id", s.errorMessage(invalid))
	s.Equal("pending", s.withdrawalStatus(kept))
}

type walletCancelCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletCancelWithdrawalGateTestSuite) member(role string, accountID uuid.UUID) walletCancelCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletCancelCaller{id: userID, token: s.login(userID)}
}

func (s *WalletCancelWithdrawalGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletCancelWithdrawalGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletCancelWithdrawalGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletCancelWithdrawalGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletCancelWithdrawalGateTestSuite) setViewAll(accountID uuid.UUID, viewAll bool) {
	_, err := facades.Orm().Query().Exec(
		`UPDATE accounts SET view_all_wallets = ? WHERE id = ?`, viewAll, accountID,
	)
	s.Require().NoError(err)
}

func (s *WalletCancelWithdrawalGateTestSuite) pending(walletID, accountID uuid.UUID, creatorID *uuid.UUID) uuid.UUID {
	return s.insertWithdrawal(walletID, accountID, "pending", creatorID)
}

func (s *WalletCancelWithdrawalGateTestSuite) insertWithdrawal(walletID, accountID uuid.UUID, status string, creatorID *uuid.UUID) uuid.UUID {
	s.T().Helper()
	withdrawalID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Withdrawal{
		ID:                 withdrawalID,
		WalletID:           walletID,
		AccountID:          &accountID,
		Status:             status,
		Amount:             "4",
		FeeEstimate:        "0",
		DestinationAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		CreatedBy:          creatorID,
	}))
	return withdrawalID
}

func (s *WalletCancelWithdrawalGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletCancelWithdrawalGatePassword)
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

func (s *WalletCancelWithdrawalGateTestSuite) cancel(token string, accountID, walletID, withdrawalID uuid.UUID) contractstesting.Response {
	return s.cancelPath(token, accountID, "/v1/wallets/"+walletID.String()+"/withdrawals/"+withdrawalID.String()+"/cancel")
}

func (s *WalletCancelWithdrawalGateTestSuite) cancelPath(token string, accountID uuid.UUID, path string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Post(path, nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletCancelWithdrawalGateTestSuite) assertCancelForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	s.Equal("forbidden", s.errorCode(resp))
	s.Equal("only the creator or an owner/admin may cancel this withdrawal", s.errorMessage(resp))
}

func (s *WalletCancelWithdrawalGateTestSuite) withdrawalStatus(withdrawalID uuid.UUID) string {
	var stored models.Withdrawal
	s.Require().NoError(facades.Orm().Query().Where("id = ?", withdrawalID).First(&stored))
	return stored.Status
}

func (s *WalletCancelWithdrawalGateTestSuite) errorCode(resp contractstesting.Response) string {
	return s.errorObject(resp).Code
}

func (s *WalletCancelWithdrawalGateTestSuite) errorMessage(resp contractstesting.Response) string {
	return s.errorObject(resp).Message
}

func (s *WalletCancelWithdrawalGateTestSuite) errorObject(resp contractstesting.Response) struct {
	Code    string `json:"code"`
	Message string `json:"message"`
} {
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	return parsed.Error
}

func (s *WalletCancelWithdrawalGateTestSuite) jsonBody(resp contractstesting.Response) map[string]any {
	parsed := map[string]any{}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	return parsed
}

func (s *WalletCancelWithdrawalGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
