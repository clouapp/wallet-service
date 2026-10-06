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

const (
	walletWhitelistGatePassword = "correct-horse-battery"
	walletWhitelistAddress      = "0x0000000000000000000000000000000000000001"
)

// WalletWhitelistGateTestSuite drives the wallet whitelist routes through
// WalletWhitelist. Wallet role owner or admin may add or delete an entry, and
// so may account role owner or admin. Auditor, user, and the other wallet
// roles are refused before an entry is written or removed.
type WalletWhitelistGateTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWallet_Whitelist_GateSuite(t *testing.T) {
	suite.Run(t, new(WalletWhitelistGateTestSuite))
}

func (s *WalletWhitelistGateTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *WalletWhitelistGateTestSuite) TestWallet_Whitelist_CreateFollowsTheLoadedRoles() {
	account := fixtures.InsertAccount(s.T(), "wallet whitelist")
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
		label := caller.accountRole + "-" + caller.walletRole + "-" + uuid.NewString()[:8]
		resp := s.addEntry(actor.token, account.ID, wallet.ID, walletWhitelistAddress, label)
		s.assertWhitelistForbidden(resp)
		s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
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
		label := caller.accountRole + "-" + caller.walletRole + "-" + uuid.NewString()[:8]
		resp := s.addEntry(actor.token, account.ID, wallet.ID, walletWhitelistAddress, label)
		resp.AssertStatus(201)
		s.assertCreatedEntry(resp, wallet.ID, walletWhitelistAddress, label)
		s.Equal(int64(1), s.whitelistCount(wallet.ID, label))
	}
}

func (s *WalletWhitelistGateTestSuite) TestMissing_Whitelist_EntryIs404BeforeTheRoleCheck() {
	account := fixtures.InsertAccount(s.T(), "wallet whitelist missing")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	actor := s.member(models.AccountRoleUser, account.ID)
	s.assign(actor.id, wallet.ID, models.WalletRoleViewer)

	resp := s.deleteEntry(actor.token, account.ID, wallet.ID, uuid.New())
	resp.AssertNotFound()
	s.Contains(s.body(resp), "whitelist entry not found")
}

func (s *WalletWhitelistGateTestSuite) TestWallet_Whitelist_DeleteFollowsTheLoadedRoles() {
	account := fixtures.InsertAccount(s.T(), "wallet whitelist delete")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
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
		label := caller.accountRole + "-" + caller.walletRole + "-" + uuid.NewString()[:8]
		created := s.addEntry(owner.token, account.ID, wallet.ID, walletWhitelistAddress, label)
		created.AssertStatus(201)
		entryID := s.createdEntryID(created)
		resp := s.deleteEntry(actor.token, account.ID, wallet.ID, entryID)
		s.assertWhitelistForbidden(resp)
		s.Equal(int64(1), s.whitelistCount(wallet.ID, label))
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
		label := caller.accountRole + "-" + caller.walletRole + "-" + uuid.NewString()[:8]
		created := s.addEntry(owner.token, account.ID, wallet.ID, walletWhitelistAddress, label)
		created.AssertStatus(201)
		entryID := s.createdEntryID(created)
		resp := s.deleteEntry(actor.token, account.ID, wallet.ID, entryID)
		resp.AssertStatus(204)
		s.Empty(strings.TrimSpace(s.body(resp)))
		s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
	}
}

func (s *WalletWhitelistGateTestSuite) TestAdd_Whitelist_EntryValidationStaysUnprocessable() {
	account := fixtures.InsertAccount(s.T(), "wallet whitelist validation")
	s.seedChain()
	wallet := fixtures.InsertWalletWithAccount(s.T(), models.ChainETH, &account.ID)
	owner := s.member(models.AccountRoleOwner, account.ID)
	label := "missing-address-" + uuid.NewString()[:8]

	resp := s.addEntry(owner.token, account.ID, wallet.ID, "", label)
	resp.AssertStatus(422)
	content := s.body(resp)
	s.Contains(content, `"validation_failed"`)
	s.Equal(int64(0), s.whitelistCount(wallet.ID, label))
}

type walletWhitelistCaller struct {
	id    uuid.UUID
	token string
}

func (s *WalletWhitelistGateTestSuite) seedChain() {
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: models.ChainETH, Name: models.ChainETH, AdapterType: models.AdapterTypeEVM,
		NativeSymbol: "ETH", NativeDecimals: 18, RpcURL: "encrypted-rpc",
		RequiredConfirmations: 1, Status: "active",
	}))
}

func (s *WalletWhitelistGateTestSuite) member(role string, accountID uuid.UUID) walletWhitelistCaller {
	userID := s.insertUser(role + "-" + uuid.NewString()[:8] + "@example.com")
	s.accountMember(accountID, userID, role)
	return walletWhitelistCaller{id: userID, token: s.login(userID)}
}

func (s *WalletWhitelistGateTestSuite) insertUser(email string) uuid.UUID {
	userID := uuid.New()
	hash, err := authsvc.NewService().HashPassword(walletWhitelistGatePassword)
	s.Require().NoError(err)
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	return userID
}

func (s *WalletWhitelistGateTestSuite) accountMember(accountID, userID uuid.UUID, role string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: accountID, UserID: userID, Role: role, Status: models.MembershipStatusActive,
	}))
}

func (s *WalletWhitelistGateTestSuite) assign(userID, walletID uuid.UUID, roles string) {
	s.Require().NoError(facades.Orm().Query().Create(&models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: roles, Status: "active",
	}))
}

func (s *WalletWhitelistGateTestSuite) login(userID uuid.UUID) string {
	var email string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT email FROM users WHERE id = ?`, userID).Scan(&email))
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletWhitelistGatePassword)
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

func (s *WalletWhitelistGateTestSuite) addEntry(token string, accountID, walletID uuid.UUID, address, label string) contractstesting.Response {
	body := fmt.Sprintf(`{"address":%q,"label":%q}`, address, label)
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		WithHeader("Content-Type", "application/json").
		Post("/v1/wallets/"+walletID.String()+"/whitelist", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *WalletWhitelistGateTestSuite) deleteEntry(token string, accountID, walletID, entryID uuid.UUID) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", accountID.String()).
		Delete("/v1/wallets/"+walletID.String()+"/whitelist/"+entryID.String(), nil)
	s.Require().NoError(err)
	return resp
}

func (s *WalletWhitelistGateTestSuite) createdEntryID(resp contractstesting.Response) uuid.UUID {
	var parsed struct {
		ID string `json:"id"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	id, err := uuid.Parse(parsed.ID)
	s.Require().NoError(err)
	return id
}

func (s *WalletWhitelistGateTestSuite) assertWhitelistForbidden(resp contractstesting.Response) {
	resp.AssertStatus(403)
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.Equal("forbidden", parsed.Error.Code)
	s.Equal("only wallet/account owners and admins may manage the whitelist", parsed.Error.Message)
}

func (s *WalletWhitelistGateTestSuite) assertCreatedEntry(resp contractstesting.Response, walletID uuid.UUID, address, label string) {
	var parsed struct {
		ID       string `json:"id"`
		WalletID string `json:"wallet_id"`
		Address  string `json:"address"`
		Label    string `json:"label"`
	}
	s.Require().NoError(json.Unmarshal([]byte(s.body(resp)), &parsed))
	s.NotEmpty(parsed.ID)
	s.Equal(walletID.String(), parsed.WalletID)
	s.Equal(address, parsed.Address)
	s.Equal(label, parsed.Label)
}

func (s *WalletWhitelistGateTestSuite) whitelistCount(walletID uuid.UUID, label string) int64 {
	var total int64
	s.Require().NoError(facades.Orm().Query().Raw(
		`SELECT count(*) FROM whitelist_entries WHERE wallet_id = ? AND label = ?`,
		walletID, label,
	).Scan(&total))
	return total
}

func (s *WalletWhitelistGateTestSuite) body(resp contractstesting.Response) string {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	return content
}
