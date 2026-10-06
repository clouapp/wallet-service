package withdrawals

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	testutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// walletWithdrawalLookupSuite covers GET /api/v1/wallets/{walletId}/withdrawals/{idempotencyKey},
// which custody clients use to learn the outcome of a withdrawal whose HTTP
// response they never received.
type walletWithdrawalLookupSuite struct {
	testutil.HTTPSuite
}

func TestWallet_Withdrawal_LookupSuite(t *testing.T) {
	testutil.RunSuite(t, new(walletWithdrawalLookupSuite))
}

func (s *walletWithdrawalLookupSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *walletWithdrawalLookupSuite) seedWallet(accountID uuid.UUID) uuid.UUID {
	s.T().Helper()

	priv, err := btcec.NewPrivateKey()
	s.Require().NoError(err)
	chainCode := sha256.Sum256([]byte("withdrawal-lookup-" + accountID.String()))

	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            "withdrawal lookup wallet",
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     hex.EncodeToString(priv.PubKey().SerializeCompressed()),
		MPCChainCode:     hex.EncodeToString(chainCode[:]),
		MPCCurve:         "secp256k1",
		AccountID:        &accountID,
	}))
	return walletID
}

func (s *walletWithdrawalLookupSuite) seedWithdrawal(walletID, accountID uuid.UUID, status string, failureReason *string, transactionID *uuid.UUID) uuid.UUID {
	s.T().Helper()

	withdrawalID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Withdrawal{
		ID:                 withdrawalID,
		WalletID:           walletID,
		AccountID:          &accountID,
		TransactionID:      transactionID,
		Status:             status,
		Amount:             "4",
		FeeEstimate:        "0",
		DestinationAddress: "0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12",
		FailureReason:      failureReason,
	}))
	return withdrawalID
}

func (s *walletWithdrawalLookupSuite) seedBroadcastTransaction(walletID uuid.UUID, txHash string) uuid.UUID {
	s.T().Helper()

	transactionID := uuid.New()
	_, err := facades.Orm().Query().Exec(
		`INSERT INTO transactions (id, wallet_id, external_user_id, chain, tx_type, tx_hash, to_address, amount, asset, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`,
		transactionID, walletID, "621", models.ChainETH, models.TxTypeWithdrawal, txHash,
		"0x742d35Cc6634C0532925a3b844Bc9e7595f2bD12", "4000000", "eth", "confirming",
	)
	s.Require().NoError(err)
	return transactionID
}

func (s *walletWithdrawalLookupSuite) get(path, bearer string, expectedStatus int) map[string]any {
	s.T().Helper()

	resp := s.External(path, testutil.Token{Bearer: bearer}).Get()
	resp.AssertStatus(expectedStatus)

	content, err := resp.Content()
	s.Require().NoError(err)
	body := map[string]any{}
	if content != "" {
		s.Require().NoError(json.Unmarshal([]byte(content), &body))
	}
	return body
}

func errorObject(code, message string) map[string]any {
	return map[string]any{"code": code, "message": message}
}

func lookupPath(walletID uuid.UUID, idempotencyKey string) string {
	return "/api/v1/wallets/" + walletID.String() + "/withdrawals/" + idempotencyKey
}

func (s *walletWithdrawalLookupSuite) TestReturns_Failed_WithdrawalWithFailureReason() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	reason := "insufficient_funds"
	withdrawalID := s.seedWithdrawal(walletID, accountID, "failed", &reason, nil)

	body := s.get(lookupPath(walletID, withdrawalID.String()), bearer, 200)
	s.Equal(withdrawalID.String(), body["id"])
	s.Equal(withdrawalID.String(), body["idempotency_key"])
	s.Equal(walletID.String(), body["wallet_id"])
	s.Equal("failed", body["status"])
	s.Equal("insufficient_funds", body["failure_reason"])
	s.Nil(body["tx_hash"])
	s.Nil(body["transaction_status"])
}

func (s *walletWithdrawalLookupSuite) TestReturns_Broadcast_WithdrawalWithTxHash() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	txHash := "0x" + hex.EncodeToString(make([]byte, 32))
	transactionID := s.seedBroadcastTransaction(walletID, txHash)
	withdrawalID := s.seedWithdrawal(walletID, accountID, "broadcast", nil, &transactionID)

	body := s.get(lookupPath(walletID, withdrawalID.String()), bearer, 200)
	s.Equal("broadcast", body["status"])
	s.Equal(txHash, body["tx_hash"])
	s.Equal("confirming", body["transaction_status"])
	s.Nil(body["failure_reason"])
}

func (s *walletWithdrawalLookupSuite) TestUnknown_Idempotency_KeyIsNotFound() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	body := s.get(lookupPath(walletID, uuid.NewString()), bearer, 404)
	s.Equal(errorObject("not_found", "withdrawal not found"), body["error"])
}

func (s *walletWithdrawalLookupSuite) TestWithdrawal_Of_AnotherWalletInSameAccountIsNotFound() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletWithWithdrawal := s.seedWallet(accountID)
	otherWallet := s.seedWallet(accountID)
	withdrawalID := s.seedWithdrawal(walletWithWithdrawal, accountID, "failed", nil, nil)

	body := s.get(lookupPath(otherWallet, withdrawalID.String()), bearer, 404)
	s.Equal(errorObject("not_found", "withdrawal not found"), body["error"])
}

func (s *walletWithdrawalLookupSuite) TestOther_Accounts_WalletIsNotFound() {
	ownerAccountID, _, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(ownerAccountID)
	withdrawalID := s.seedWithdrawal(walletID, ownerAccountID, "broadcast", nil, nil)
	_, intruderBearer, _ := testutil.SetupAPIAuth(s.T(), false)

	body := s.get(lookupPath(walletID, withdrawalID.String()), intruderBearer, 404)
	s.Equal(errorObject("not_found", "wallet not found"), body["error"])
}

func (s *walletWithdrawalLookupSuite) TestRequires_Bearer_Token() {
	accountID, _, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	withdrawalID := s.seedWithdrawal(walletID, accountID, "failed", nil, nil)

	s.get(lookupPath(walletID, withdrawalID.String()), "", 401)
}

func (s *walletWithdrawalLookupSuite) TestRejects_Non_UUIDIdempotencyKey() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)

	body := s.get(lookupPath(walletID, "not-a-uuid"), bearer, 400)
	s.Equal(errorObject("invalid_request", "idempotency_key must be a UUID"), body["error"])
}
