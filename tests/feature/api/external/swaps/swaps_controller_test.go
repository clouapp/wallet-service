package swaps

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	testutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

// externalSwapsSuite covers /api/v1/wallets/{walletId}/swaps/*: the external
// surface shares the dashboard's handler, which reads the authenticated account.
// Every endpoint must answer its own refusals, never a 500 panic.
type externalSwapsSuite struct {
	testutil.HTTPSuite
}

func TestExternal_SwapsSuite(t *testing.T) {
	testutil.RunSuite(t, new(externalSwapsSuite))
}

func (s *externalSwapsSuite) SetupTest() {
	fixtures.TestDB(s.T())
}

func (s *externalSwapsSuite) seedWallet(accountID uuid.UUID) uuid.UUID {
	s.T().Helper()
	priv, err := btcec.NewPrivateKey()
	s.Require().NoError(err)
	chainCode := sha256.Sum256([]byte("external-swaps-" + accountID.String()))
	walletID := uuid.New()
	s.Require().NoError(facades.Orm().Query().Create(&models.Wallet{
		ID:               walletID,
		Chain:            models.ChainETH,
		Label:            "external swaps wallet",
		Status:           "active",
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

func swapsPath(walletID uuid.UUID, rest string) string {
	return "/api/v1/wallets/" + walletID.String() + "/swaps" + rest
}

func (s *externalSwapsSuite) TestShow_UnknownSwapIsNotFoundNotAPanic() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	resp := s.External(swapsPath(walletID, "/"+uuid.NewString()), testutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 404, "not_found", "swap not found")
}

func (s *externalSwapsSuite) TestRecover_UnknownKeyIsNotFoundNotAPanic() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	resp := s.External(swapsPath(walletID, "/by-key/"+uuid.NewString()), testutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 404, "not_found", "swap not found")
}

func (s *externalSwapsSuite) TestExecute_UnknownQuoteIsNotFoundNotAPanic() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	resp := s.External(swapsPath(walletID, ""), testutil.Token{Bearer: bearer}).Post(map[string]any{
		"quote_id": uuid.NewString(), "idempotency_key": uuid.NewString(), "passphrase": "test-only-passphrase-123",
	})
	resp.AssertStatus(404)
}

func (s *externalSwapsSuite) TestQuote_ReachesTheServiceWithTheTokenAccount() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	resp := s.External(swapsPath(walletID, "/quotes/to/"+uuid.NewString()), testutil.Token{Bearer: bearer}).Post(map[string]any{
		"from_asset": "ETH", "to_asset": "USDC", "amount": "0.01",
	})
	// The destination wallet does not exist: the service answers, scoped to the
	// token's account, instead of the handler panicking on a missing account.
	resp.AssertStatus(404)
}

func (s *externalSwapsSuite) TestQuote_NeedsTheWithdrawalPermissionLikeExecute() {
	accountID, bearer, _ := testutil.SetupAPIAuth(s.T(), false)
	walletID := s.seedWallet(accountID)
	_, err := facades.Orm().Query().Exec(`UPDATE access_tokens SET permissions = '["wallets.read"]'::jsonb WHERE account_id = ?`, accountID)
	s.Require().NoError(err)
	resp := s.External(swapsPath(walletID, "/quotes/to/"+walletID.String()), testutil.Token{Bearer: bearer}).Post(map[string]any{
		"from_asset": "ETH", "to_asset": "USDC", "amount": "0.01",
	})
	resp.AssertStatus(403)
}
