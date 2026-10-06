package feeestimate

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
	"github.com/macrowallets/waas/tests/mocks"
)

// feeEstimateChainID is a chain only this suite registers, so its mock adapter
// cannot leak into other suites sharing the process-wide registry.
const (
	feeEstimateChainID   = "fee-estimate-test"
	feeEstimateNative    = "FEE"
	feeEstimateGasPrice  = 10
	feeEstimateRecipient = "0x53bc147071251db8294b55a303a4570dab595178"
	nativeTransferGas    = 21_000
)

// gasSizingChain adds the transfer gas sizing the planner needs from an EVM adapter.
type gasSizingChain struct {
	*mocks.MockChain
}

func (gasSizingChain) EstimateTransferGasLimit(_ context.Context, req types.TransferRequest) (uint64, error) {
	if req.Token != nil {
		return 0, errors.New("token transfers are not sized by this test chain")
	}
	return nativeTransferGas, nil
}

// feeEstimateSuite covers GET /api/v1/wallets/{walletId}/fee-estimate through the
// real middleware, controller, estimator and planner, with a mock chain adapter.
type feeEstimateSuite struct {
	suite.Suite
	goravelTesting.TestCase
	adapter *mocks.MockChain
}

func TestFeeEstimateSuite(t *testing.T) {
	suite.Run(t, new(feeEstimateSuite))
}

func (s *feeEstimateSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.Require().NoError(facades.Orm().Query().Create(&models.Chain{
		ID: feeEstimateChainID, Name: "Fee estimate test", AdapterType: models.AdapterTypeEVM,
		NativeSymbol: feeEstimateNative, NativeDecimals: 18, RpcURL: "unused", RequiredConfirmations: 1,
		Status: "active",
	}))
	s.adapter = mocks.NewMockChain(feeEstimateChainID)
	s.adapter.NativeAssetVal = feeEstimateNative
	s.adapter.EstimateGasPriceVal = big.NewInt(feeEstimateGasPrice)
	s.adapter.ValidateAddressFn = func(address string) bool {
		return len(address) == 42 && strings.HasPrefix(address, "0x")
	}
	container.Get().Registry.RegisterChain(gasSizingChain{s.adapter})
}

func (s *feeEstimateSuite) seedWallet(accountID uuid.UUID) uuid.UUID {
	s.T().Helper()
	return fixtures.InsertWalletWithAccount(s.T(), feeEstimateChainID, &accountID).ID
}

func feeEstimatePath(walletID uuid.UUID, query url.Values) string {
	path := "/api/v1/wallets/" + walletID.String() + "/fee-estimate"
	if len(query) == 0 {
		return path
	}
	return path + "?" + query.Encode()
}

func (s *feeEstimateSuite) get(path, bearer string, expectedStatus int) map[string]any {
	s.T().Helper()

	request := s.Http(s.T())
	if bearer != "" {
		request = request.WithHeader("Authorization", "Bearer "+bearer)
	}
	resp, err := request.Get(path)
	s.Require().NoError(err)
	content, err := resp.Content()
	s.Require().NoError(err)
	resp.AssertStatus(expectedStatus)
	if s.T().Failed() {
		s.T().Fatalf("%s → %s", path, content)
	}

	body := map[string]any{}
	if content != "" {
		s.Require().NoError(json.Unmarshal([]byte(content), &body), content)
	}
	return body
}

func (s *feeEstimateSuite) TestRequiresBearerToken() {
	accountID, _, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	s.get(feeEstimatePath(walletID, nil), "", 401)
}

func (s *feeEstimateSuite) TestOtherAccountsWalletIsNotFound() {
	ownerAccountID, _, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(ownerAccountID)
	_, intruderBearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := s.get(feeEstimatePath(walletID, url.Values{"amount": {"0.001"}}), intruderBearer, 404)
	s.Equal("wallet not found", feeEstimateErrorMessage(body))
}

func (s *feeEstimateSuite) TestQuotesTheNativeTransferThroughThePlanner() {
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	body := s.get(feeEstimatePath(walletID, url.Values{"amount": {"0.0000000000001"}, "to": {feeEstimateRecipient}}), bearer, 200)

	s.Equal("210000", body["fee_base_units"])
	s.Equal("0.00000000000021", body["fee"])
	s.Equal(feeEstimateNative, body["fee_asset"])
	s.Equal("100000", body["amount_base_units"])
	s.Equal("direct_from_base", body["strategy"])
	s.Equal(false, body["insufficient_funds"])
	evm := body["details"].(map[string]any)["evm"].(map[string]any)
	s.EqualValues(21_000, evm["gas_limit"])
	s.Equal("10", evm["gas_price_wei"])
}

func (s *feeEstimateSuite) TestInsufficientFundsStillReturnsTheFee() {
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	body := s.get(feeEstimatePath(walletID, url.Values{"amount": {"1"}}), bearer, 200)

	s.Equal("210000", body["fee_base_units"])
	s.Equal(true, body["insufficient_funds"])
	s.Equal("probe", body["recipient"])
}

func (s *feeEstimateSuite) TestNodeFailureIs503WithoutAFee() {
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	s.adapter.EstimateGasPriceVal = nil
	s.adapter.EstimateGasPriceErr = errors.New("rpc timeout")

	body := s.get(feeEstimatePath(walletID, url.Values{"amount": {"0.0000000000001"}, "to": {feeEstimateRecipient}}), bearer, 503)

	s.Equal("fee_estimate_unavailable", feeEstimateErrorCode(body))
	s.Nil(body["fee"])
}

func (s *feeEstimateSuite) TestInvalidInputsAreRejectedBeforeQuoting() {
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	cases := []struct {
		query  url.Values
		status int
		code   string
	}{
		{url.Values{"amount": {"0"}}, 400, "invalid_amount"},
		{url.Values{"amount": {"-1"}}, 400, "invalid_amount"},
		{url.Values{"amount": {"1e3"}}, 400, "invalid_amount"},
		{url.Values{"amount": {"0.0000000000000000001"}}, 400, "invalid_amount"},
		{url.Values{"amount": {"1"}, "asset": {"DOGE"}}, 422, "unknown_asset"},
		{url.Values{"amount": {"1"}, "to": {"tb1qur7330emxypadqvr0mu4989sfzw32gpkxgyd2m"}}, 422, "invalid_address"},
	}
	for _, tc := range cases {
		body := s.get(feeEstimatePath(walletID, tc.query), bearer, tc.status)
		s.Equal(tc.code, feeEstimateErrorCode(body), tc.query.Encode())
		s.NotEmpty(feeEstimateErrorMessage(body), tc.query.Encode())
	}
}

func feeEstimateErrorBody(body map[string]any) map[string]any {
	errBody, _ := body["error"].(map[string]any)
	return errBody
}

func feeEstimateErrorCode(body map[string]any) string {
	code, _ := feeEstimateErrorBody(body)["code"].(string)
	return code
}

func feeEstimateErrorMessage(body map[string]any) string {
	message, _ := feeEstimateErrorBody(body)["message"].(string)
	return message
}
