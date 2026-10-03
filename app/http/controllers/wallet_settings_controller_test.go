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

const walletSettingsTestPassword = "correct-horse-battery"

// WalletSettingsTestSuite drives PATCH /v1/wallets/{id}/settings through the
// production router with dashboard sessions of an owner and a viewer.
type WalletSettingsTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
	account     models.Account
	ownerToken  string
	viewerToken string
}

func TestWalletSettingsSuite(t *testing.T) {
	suite.Run(t, new(WalletSettingsTestSuite))
}

func (s *WalletSettingsTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.account = mocks.InsertAccount(s.T(), "settings")
	s.ownerToken = s.memberToken(models.AccountRoleOwner)
	s.viewerToken = s.memberToken(models.AccountRoleAuditor)
	for _, chain := range []struct{ id, adapter string }{
		{models.ChainBase, models.AdapterTypeEVM},
		{models.ChainBTC, models.AdapterTypeBitcoin},
		{models.ChainSOL, models.AdapterTypeSolana},
	} {
		s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
			ID: chain.id, Name: chain.id, AdapterType: chain.adapter, NativeSymbol: chain.id, NativeDecimals: 8,
			RpcURL: "encrypted-rpc", RequiredConfirmations: 1, Status: "active",
		}))
	}
}

func (s *WalletSettingsTestSuite) memberToken(role string) string {
	hash, err := authsvc.NewService().HashPassword(walletSettingsTestPassword)
	s.Require().NoError(err)
	userID := uuid.New()
	email := role + "-" + userID.String()[:8] + "@example.com"
	_, err = facades.Orm().Query().Exec(
		`INSERT INTO users (id, email, password_hash, status, created_at, updated_at) VALUES (?, ?, ?, ?, NOW(), NOW())`,
		userID, email, hash, "active",
	)
	s.Require().NoError(err)
	s.Require().NoError(facades.Orm().Query().Create(&models.AccountUser{
		ID: uuid.New(), AccountID: s.account.ID, UserID: userID, Role: role,
	}))

	body := fmt.Sprintf(`{"email":%q,"password":%q}`, email, walletSettingsTestPassword)
	resp, err := s.Http(s.T()).WithHeader("Content-Type", "application/json").Post("/v1/auth/login", strings.NewReader(body))
	s.Require().NoError(err)
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

func (s *WalletSettingsTestSuite) wallet(chainID string) models.Wallet {
	return mocks.InsertWalletWithAccount(s.T(), chainID, &s.account.ID)
}

func (s *WalletSettingsTestSuite) patch(token string, walletID uuid.UUID, body string) contractstesting.Response {
	resp, err := s.Http(s.T()).
		WithHeader("Content-Type", "application/json").
		WithHeader("Authorization", "Bearer "+token).
		WithHeader("X-Account-Id", s.account.ID.String()).
		Patch("/v1/wallets/"+walletID.String()+"/settings", strings.NewReader(body))
	s.Require().NoError(err)
	return resp
}

func (s *WalletSettingsTestSuite) stored(walletID uuid.UUID) models.Wallet {
	var wallet models.Wallet
	s.Require().NoError(facades.Orm().Query().Where("id = ?", walletID).First(&wallet))
	return wallet
}

func (s *WalletSettingsTestSuite) TestOwnerSetsAndResetsTheFeeMultiplier() {
	wallet := s.wallet(models.ChainBase)

	resp := s.patch(s.ownerToken, wallet.ID, `{"fee_multiplier": 1.25, "label": "Treasury"}`)
	resp.AssertStatus(200)
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, `"fee_multiplier":1.25`)
	s.Contains(content, `"label":"Treasury"`)
	stored := s.stored(wallet.ID)
	s.True(stored.FeeMultiplier.Valid)
	s.Equal("1.25", stored.FeeMultiplier.Decimal.String())
	s.Equal("Treasury", stored.Label)

	s.patch(s.ownerToken, wallet.ID, `{"fee_multiplier": null}`).AssertStatus(200)
	s.False(s.stored(wallet.ID).FeeMultiplier.Valid, "null resets the multiplier to the network default")
	s.Equal("Treasury", s.stored(wallet.ID).Label, "omitted fields stay unchanged")
}

func (s *WalletSettingsTestSuite) TestInvalidFieldsAreRefusedWithoutWriting() {
	wallet := s.wallet(models.ChainBase)
	for _, body := range []string{
		`{"fee_multiplier": 0.5}`,
		`{"fee_multiplier": 5.5}`,
		`{"fee_multiplier": 1.23456}`,
		`{"fee_multiplier": "abc"}`,
		`{"fee_rate_min": 2}`,
		`{"frozen_until": "2030-01-01T00:00:00Z"}`,
		`{"status": "active"}`,
	} {
		resp := s.patch(s.ownerToken, wallet.ID, body)
		resp.AssertStatus(422)
		content, err := resp.Content()
		s.Require().NoError(err)
		s.Contains(content, `"errors"`, body)
		s.Contains(content, `"validation_failed"`, body)
	}
	s.patch(s.ownerToken, wallet.ID, `{}`).AssertStatus(400)
	s.False(s.stored(wallet.ID).FeeMultiplier.Valid)
}

func (s *WalletSettingsTestSuite) TestChainRulesForFeeSettings() {
	btc := s.wallet(models.ChainBTC)
	s.patch(s.ownerToken, btc.ID, `{"fee_multiplier": "1.5", "fee_rate_min": 2, "fee_rate_max": 40}`).AssertStatus(200)
	stored := s.stored(btc.ID)
	s.Require().NotNil(stored.FeeRateMin)
	s.Require().NotNil(stored.FeeRateMax)
	s.Equal(2, *stored.FeeRateMin)
	s.Equal(40, *stored.FeeRateMax)
	s.patch(s.ownerToken, btc.ID, `{"fee_rate_min": 41}`).AssertStatus(422)

	sol := s.wallet(models.ChainSOL)
	s.patch(s.ownerToken, sol.ID, `{"fee_multiplier": 2}`).AssertStatus(422)
}

func (s *WalletSettingsTestSuite) TestViewersCannotChangeSettings() {
	wallet := s.wallet(models.ChainBase)
	s.patch(s.viewerToken, wallet.ID, `{"fee_multiplier": 2}`).AssertStatus(403)
	s.False(s.stored(wallet.ID).FeeMultiplier.Valid)
}

func (s *WalletSettingsTestSuite) TestOtherAccountsWalletsAreNotReachable() {
	other := mocks.InsertAccount(s.T(), "other")
	foreign := mocks.InsertWalletWithAccount(s.T(), models.ChainBase, &other.ID)
	s.patch(s.ownerToken, foreign.ID, `{"fee_multiplier": 2}`).AssertStatus(403)
	s.False(s.stored(foreign.ID).FeeMultiplier.Valid)
}
