package controllers_test

import (
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/tests/mocks"
)

type WalletsControllerTestSuite struct {
	authSuite
}

func TestWalletsControllerSuite(t *testing.T) {
	suite.Run(t, new(WalletsControllerTestSuite))
}

func (s *WalletsControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WalletsControllerTestSuite) TestCreateWallet_Success() {
	s.SignedPost("/v1/wallets", `{"chain":"eth","label":"Ethereum Wallet"}`).
		AssertCreated().AssertJson(map[string]any{"chain": "eth", "label": "Ethereum Wallet"})
}

func (s *WalletsControllerTestSuite) TestCreateWallet_DuplicateChain() {
	s.SignedPost("/v1/wallets", `{"chain":"eth","label":"First"}`)
	s.SignedPost("/v1/wallets", `{"chain":"eth","label":"Second"}`).AssertConflict()
}

func (s *WalletsControllerTestSuite) TestCreateWallet_MissingChain() {
	s.SignedPost("/v1/wallets", `{"label":"No chain"}`).AssertBadRequest()
}

func (s *WalletsControllerTestSuite) TestCreateWallet_UnknownChain() {
	s.SignedPost("/v1/wallets", `{"chain":"dogecoin"}`).AssertConflict()
}

func (s *WalletsControllerTestSuite) TestListWallets() {
	s.SignedPost("/v1/wallets", `{"chain":"eth","label":"ETH"}`)
	s.SignedPost("/v1/wallets", `{"chain":"btc","label":"BTC"}`)

	s.SignedGet("/v1/wallets").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.HasWithScope("data", 2, func(j contractstestinghttp.AssertableJSON) {
				j.Has("chain").Has("label")
			})
		})
}

func (s *WalletsControllerTestSuite) TestGetWallet_Success() {
	createResp := s.SignedPost("/v1/wallets", `{"chain":"eth","label":"ETH"}`)
	j, err := createResp.Json()
	s.Nil(err)
	walletID := j["id"].(string)

	s.SignedGet("/v1/wallets/"+walletID).
		AssertOk().AssertJson(map[string]any{"id": walletID, "chain": "eth"})
}

func (s *WalletsControllerTestSuite) TestGetWallet_NotFound() {
	s.SignedGet("/v1/wallets/00000000-0000-0000-0000-000000000000").AssertNotFound()
}

func (s *WalletsControllerTestSuite) TestGetWallet_InvalidUUID() {
	s.SignedGet("/v1/wallets/not-a-uuid").AssertBadRequest()
}
