package controllers_test

import (
	"testing"

	contractstestinghttp "github.com/goravel/framework/contracts/testing/http"
	"github.com/stretchr/testify/suite"

	"github.com/macromarkets/vault/tests/mocks"
)

type TransactionsControllerTestSuite struct {
	authSuite
}

func TestTransactionsControllerSuite(t *testing.T) {
	suite.Run(t, new(TransactionsControllerTestSuite))
}

func (s *TransactionsControllerTestSuite) SetupTest() {
	mocks.TestDB(s.T())
}

func (s *TransactionsControllerTestSuite) TestListTransactions_Empty() {
	s.SignedGet("/v1/transactions").
		AssertOk().
		AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
			json.Has("data").Count("data", 0)
		})
}

func (s *TransactionsControllerTestSuite) TestListTransactions_WithFilters() {
	s.SignedGet("/v1/transactions?chain=eth&type=deposit&status=pending&limit=10").
		AssertOk().AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
		json.Has("data")
	})
}

func (s *TransactionsControllerTestSuite) TestListTransactions_WithPagination() {
	s.SignedGet("/v1/transactions?limit=5&offset=0").AssertOk()
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_NotFound() {
	s.SignedGet("/v1/transactions/00000000-0000-0000-0000-000000000000").AssertNotFound()
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_InvalidUUID() {
	s.SignedGet("/v1/transactions/not-a-uuid").AssertBadRequest()
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_Success() {
	wj, _ := s.SignedPost("/v1/wallets", `{"chain":"eth"}`).Json()
	walletID := wj["id"].(string)

	tj, _ := s.SignedPost("/v1/wallets/"+walletID+"/withdrawals",
		`{"external_user_id":"user","to_address":"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12","amount":"1000","asset":"eth","idempotency_key":"tx_get_001"}`).Json()
	txID := tj["id"].(string)

	s.SignedGet("/v1/transactions/"+txID).
		AssertOk().AssertJson(map[string]any{"id": txID, "tx_type": "withdrawal", "status": "pending"})
}

func (s *TransactionsControllerTestSuite) TestListUserTransactions() {
	s.SignedGet("/v1/users/user_nobody/transactions").
		AssertOk().AssertFluentJson(func(json contractstestinghttp.AssertableJSON) {
		json.Has("data")
	})
}

func (s *TransactionsControllerTestSuite) TestListUserTransactions_WithFilters() {
	s.SignedGet("/v1/users/test_user/transactions?chain=eth&type=deposit").AssertOk()
}
