package wallets

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// WalletsControllerTestSuite exercises the external /api/v1/wallets endpoints.
//
// Creation flows exercise the real wallet service (MPC keygen + AWS Secrets
// Manager via LocalStack); make dev brings up waas-localstack so these tests
// run green in the local developer environment and in any CI job that boots
// the docker-compose stack. Read endpoints (list/get) seed wallets directly
// via the ORM so they stay independent of LocalStack / chain adapters.
type WalletsControllerTestSuite struct {
	ctltestutil.HTTPSuite
}

func TestWallets_Controller_Suite(t *testing.T) {
	ctltestutil.RunSuite(t, new(WalletsControllerTestSuite))
}

// Note on wallet creation coverage
// --------------------------------
// The happy-path create tests live in WalletRecoveryMaterialTestSuite, which
// passes a wallet service backed by mock chain / MPC / Secrets Manager
// dependencies into the wallet controllers (the real registry is populated
// from RPC-reachable providers at bootstrap). The validator-level create
// tests below guarantee the 422 contract for bad inputs, and
// TestCriticalEndpointsSuite exercises the wallet-bound external API paths
// against a directly-seeded wallet.

// TestCreateWallet_MissingChain confirms that the external API rejects a
// request with no chain. The shared validator maps rule violations to 422
// (Unprocessable Entity) via controllers.validateRequest — not 400.
func (s *WalletsControllerTestSuite) TestCreate_Wallet_MissingChain() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"label":"No chain","passphrase":"test-passphrase-123"}`
	resp := s.External("/api/v1/wallets", ctltestutil.Token{Bearer: bearer}).Post(body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "Blockchain chain is required")
}

// TestCreateWallet_UnknownChain confirms the db_exists:chains,id rule rejects
// chains that aren't seeded. Returns 422 via the shared validator.
func (s *WalletsControllerTestSuite) TestCreate_Wallet_UnknownChain() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"chain":"dogecoin","label":"Doge","passphrase":"test-passphrase-123"}`
	resp := s.External("/api/v1/wallets", ctltestutil.Token{Bearer: bearer}).Post(body)
	s.AssertError(resp, 422, "validation_failed", "validation failed")
	content, err := resp.Content()
	s.Require().NoError(err)
	s.Contains(content, "The specified chain is not supported")
}

func (s *WalletsControllerTestSuite) TestWalletsController_List_Wallets() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	_ = seedAPIWalletForAccount(s.T(), accountID, "eth", "ETH")
	_ = seedAPIWalletForAccount(s.T(), accountID, "btc", "BTC")

	resp := s.External("/api/v1/wallets", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []struct {
			Chain string `json:"chain"`
			Label string `json:"label"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Len(payload.Data, 2)
	for _, w := range payload.Data {
		s.NotEmpty(w.Chain)
		s.NotEmpty(w.Label)
	}
}

// TestListWallets_ScopedToAccount guards the account filter on ListWallets:
// wallets owned by account A must not appear in the response for account B.
func (s *WalletsControllerTestSuite) TestList_Wallets_ScopedToAccount() {
	testutil.SeededTestDB(s.T())
	accountA, bearerA, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_ = seedAPIWalletForAccount(s.T(), accountA, "eth", "A-eth")

	respA := s.External("/api/v1/wallets", ctltestutil.Token{Bearer: bearerA}).Get()
	respA.AssertOk()
	contentA, err := respA.Content()
	s.Require().NoError(err)
	var payloadA struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentA), &payloadA))
	s.Len(payloadA.Data, 1)

	respB := s.External("/api/v1/wallets", ctltestutil.Token{Bearer: bearerB}).Get()
	respB.AssertOk()
	contentB, err := respB.Content()
	s.Require().NoError(err)
	var payloadB struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentB), &payloadB))
	s.Empty(payloadB.Data, "wallets must be scoped to the calling account")
}

func (s *WalletsControllerTestSuite) TestGet_Wallet_Success() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "ETH")

	s.External("/api/v1/wallets/"+walletID, ctltestutil.Token{Bearer: bearer}).Get().
		AssertOk().
		AssertJson(map[string]any{"id": walletID, "chain": "eth"})
}

// TestGetWallet_NotFound — the APIWalletContext middleware returns a generic
// 404 "wallet not found" body for any walletId that the caller's account
// does not own (including valid UUIDs that simply don't exist).
func (s *WalletsControllerTestSuite) TestGet_Wallet_NotFound() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := s.External("/api/v1/wallets/"+uuid.NewString(), ctltestutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 404, "not_found", "wallet not found")
}

// TestGetWallet_InvalidUUID — the middleware intentionally returns 404 (not
// 400) for malformed walletIds on the external API so callers can't probe
// for the difference between "malformed" and "not yours". This is the
// documented behaviour of middleware.APIWalletContext.
func (s *WalletsControllerTestSuite) TestGet_Wallet_InvalidUUID() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := s.External("/api/v1/wallets/not-a-uuid", ctltestutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 404, "not_found", "wallet not found")
}

// TestGetWallet_OtherAccount — IDOR guard: a wallet owned by account A is
// indistinguishable from "does not exist" when looked up by account B.
func (s *WalletsControllerTestSuite) TestGet_Wallet_OtherAccount() {
	testutil.SeededTestDB(s.T())
	accountA, _, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	walletA := seedAPIWalletForAccount(s.T(), accountA, "eth", "A-owned")

	resp := s.External("/api/v1/wallets/"+walletA, ctltestutil.Token{Bearer: bearerB}).Get()
	s.AssertError(resp, 404, "not_found", "wallet not found")
}
