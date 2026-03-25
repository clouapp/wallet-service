package controllers_test

import (
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/tests/mocks"
)

type WithdrawalsControllerTestSuite struct {
	authSuite
}

func TestWithdrawalsControllerSuite(t *testing.T) {
	suite.Run(t, new(WithdrawalsControllerTestSuite))
}

func (s *WithdrawalsControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *WithdrawalsControllerTestSuite) createWallet() string {
	resp := s.SignedPost("/v1/wallets", `{"chain":"eth"}`)
	j, _ := resp.Json()
	return j["id"].(string)
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_Success() {
	walletID := s.createWallet()
	s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user_withdraw","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"1000000","asset":"eth","idempotency_key":"withdraw_001"}`).
		AssertCreated().AssertJson(map[string]any{
		"status":           "pending",
		"tx_type":          "withdrawal",
		"external_user_id": "user_withdraw",
		"amount":           "1000000",
		"asset":            "eth",
	})
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_Idempotency() {
	walletID := s.createWallet()
	body := `{"external_user_id":"user_idem","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"500000","asset":"eth","idempotency_key":"idem_test_001"}`

	j1, _ := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", body).Json()
	j2, _ := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals", body).Json()

	s.Equal(j1["id"], j2["id"])
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_MissingIdempotencyKey() {
	walletID := s.createWallet()
	s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"100","asset":"eth"}`).
		AssertBadRequest()
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_InvalidAddress() {
	walletID := s.createWallet()
	s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user","to_address":"invalid_address","amount":"100","asset":"eth","idempotency_key":"invalid_addr_001"}`).
		AssertStatus(409)
}

func (s *WithdrawalsControllerTestSuite) TestCreateWithdrawal_WalletNotFound() {
	s.SignedPost("/v1/wallets/00000000-0000-0000-0000-000000000000/withdrawals",
		`{"external_user_id":"user","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"100","asset":"eth","idempotency_key":"notfound_001"}`).
		AssertNotFound()
}
