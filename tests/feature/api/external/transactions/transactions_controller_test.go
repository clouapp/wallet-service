package transactions

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// TransactionsControllerTestSuite exercises the external /api/v1 transaction
// read routes. Creation/mutation is covered by the dashboard suite and by
// TestCriticalEndpointsSuite for withdrawal — here we confirm the listing,
// filtering, and single-transaction retrieval contract.
type TransactionsControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestTransactionsControllerSuite(t *testing.T) {
	suite.Run(t, new(TransactionsControllerTestSuite))
}

// seedTransactionForAccount inserts a Wallet (bound to accountID) plus one
// Transaction row so GET /api/v1/transactions/{id} can resolve it. Returns
// the transaction ID as a string for URL interpolation.
func seedTransactionForAccount(t *testing.T, accountID uuid.UUID, chain string) string {
	t.Helper()

	walletID := uuid.New()
	acct := accountID
	w := &models.Wallet{
		ID:               walletID,
		Chain:            chain,
		Label:            "tx-suite wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     "02abc123def456",
		MPCCurve:         "secp256k1",
		AccountID:        &acct,
	}
	if err := facades.Orm().Query().Create(w); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}

	txID := uuid.New()
	// transaction_direction / transaction_source are PG ENUMs that reject the
	// Go zero value ("") — set explicit values so the ORM insert passes.
	tx := &models.Transaction{
		ID:             txID,
		WalletID:       walletID,
		ExternalUserID: "user_tx",
		Chain:          chain,
		TxType:         models.TxTypeWithdrawal,
		TxHash:         "0xtesthash" + uuid.NewString()[:8],
		ToAddress:      "0xrecipient",
		Amount:         "1000",
		Asset:          chain,
		Status:         "pending",
		RequiredConfs:  3,
		Direction:      "outbound",
		Source:         "withdrawal_flow",
		RawPayload:     "{}",
	}
	if err := facades.Orm().Query().Create(tx); err != nil {
		t.Fatalf("insert transaction: %v", err)
	}
	return txID.String()
}

func (s *TransactionsControllerTestSuite) TestListTransactions_Empty() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/transactions", bearer)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Empty(payload.Data)
}

func (s *TransactionsControllerTestSuite) TestListTransactions_WithFilters() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := ctltestutil.Get(s.T(), &s.TestCase,
		"/api/v1/transactions?chain=eth&type=deposit&status=pending&limit=10", bearer)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotNil(payload.Data)
}

func (s *TransactionsControllerTestSuite) TestListTransactions_WithPagination() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/transactions?limit=5&offset=0", bearer).
		AssertOk()
}

func (s *TransactionsControllerTestSuite) TestListTransactions_ScopedToAccount() {
	testutil.SeededTestDB(s.T())
	accountA, bearerA, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	accountB, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	_ = seedTransactionForAccount(s.T(), accountA, "eth")

	respA := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/transactions", bearerA)
	respA.AssertOk()
	contentA, err := respA.Content()
	s.Require().NoError(err)
	var payloadA struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentA), &payloadA))
	s.Len(payloadA.Data, 1, "owner should see their transaction")

	respB := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/transactions", bearerB)
	respB.AssertOk()
	contentB, err := respB.Content()
	s.Require().NoError(err)
	var payloadB struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentB), &payloadB))
	s.Empty(payloadB.Data, "other account must not see A's transactions")
	_ = accountB
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_NotFound() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/transactions/"+uuid.NewString(), bearer).
		AssertNotFound()
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_InvalidUUID() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/transactions/not-a-uuid", bearer).
		AssertBadRequest()
}

func (s *TransactionsControllerTestSuite) TestGetTransaction_Success() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	txID := seedTransactionForAccount(s.T(), accountID, "eth")

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/transactions/"+txID, bearer).
		AssertOk().
		AssertJson(map[string]any{
			"id":      txID,
			"tx_type": models.TxTypeWithdrawal,
			"status":  "pending",
		})
}

func (s *TransactionsControllerTestSuite) TestListUserTransactions() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/users/user_nobody/transactions", bearer)
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotNil(payload.Data)
}

func (s *TransactionsControllerTestSuite) TestListUserTransactions_WithFilters() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/users/test_user/transactions?chain=eth&type=deposit", bearer).
		AssertOk()
}
