package controllers_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	goravelTesting "github.com/goravel/framework/testing"
	"github.com/stretchr/testify/suite"

	ctltestutil "github.com/macrowallets/waas/app/http/controllers/testutil"
	"github.com/macrowallets/waas/tests/testutil"
)

// WalletsControllerTestSuite exercises the external /api/v1/wallets endpoints.
//
// Creation flows exercise the real wallet service (MPC keygen + AWS Secrets
// Manager via LocalStack); make dev brings up waas-localstack so these tests
// run green in the local developer environment and in any CI job that boots
// the docker-compose stack. Read endpoints (list/get) seed wallets directly
// via the ORM so they stay independent of LocalStack / chain adapters.
type WalletsControllerTestSuite struct {
	suite.Suite
	goravelTesting.TestCase
}

func TestWalletsControllerSuite(t *testing.T) {
	suite.Run(t, new(WalletsControllerTestSuite))
}

// Note on wallet creation coverage
// --------------------------------
// The happy-path POST /api/v1/wallets test is intentionally NOT migrated:
// the underlying WalletService.CreateWallet requires the chain registry to
// have an entry for the target chain (e.g. `eth`), and the chain registry
// is populated from RPC-reachable providers at bootstrap time — behaviour
// not suitable for a hermetic integration test. The validator-level create
// tests below still guarantee the 422 contract for bad inputs, and
// TestCriticalEndpointsSuite exercises the wallet-bound external API paths
// against a directly-seeded wallet.

// TestCreateWallet_MissingChain confirms that the external API rejects a
// request with no chain. The shared validator maps rule violations to 422
// (Unprocessable Entity) via controllers.validateRequest — not 400.
func (s *WalletsControllerTestSuite) TestCreateWallet_MissingChain() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"label":"No chain","passphrase":"test-passphrase-123"}`
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/wallets", body, bearer, nil).
		AssertStatus(422)
}

// TestCreateWallet_UnknownChain confirms the db_exists:chains,id rule rejects
// chains that aren't seeded. Returns 422 via the shared validator.
func (s *WalletsControllerTestSuite) TestCreateWallet_UnknownChain() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	body := `{"chain":"dogecoin","label":"Doge","passphrase":"test-passphrase-123"}`
	ctltestutil.
		Post(s.T(), &s.TestCase, "/api/v1/wallets", body, bearer, nil).
		AssertStatus(422)
}

func (s *WalletsControllerTestSuite) TestListWallets() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	_ = seedAPIWalletForAccount(s.T(), accountID, "eth", "ETH")
	_ = seedAPIWalletForAccount(s.T(), accountID, "btc", "BTC")

	resp := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/wallets", bearer)
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
func (s *WalletsControllerTestSuite) TestListWallets_ScopedToAccount() {
	testutil.SeededTestDB(s.T())
	accountA, bearerA, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_ = seedAPIWalletForAccount(s.T(), accountA, "eth", "A-eth")

	respA := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/wallets", bearerA)
	respA.AssertOk()
	contentA, err := respA.Content()
	s.Require().NoError(err)
	var payloadA struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentA), &payloadA))
	s.Len(payloadA.Data, 1)

	respB := ctltestutil.Get(s.T(), &s.TestCase, "/api/v1/wallets", bearerB)
	respB.AssertOk()
	contentB, err := respB.Content()
	s.Require().NoError(err)
	var payloadB struct {
		Data []any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(contentB), &payloadB))
	s.Empty(payloadB.Data, "wallets must be scoped to the calling account")
}

func (s *WalletsControllerTestSuite) TestGetWallet_Success() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "ETH")

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/wallets/"+walletID, bearer).
		AssertOk().
		AssertJson(map[string]any{"id": walletID, "chain": "eth"})
}

// TestGetWallet_NotFound — the APIWalletContext middleware returns a generic
// 404 "wallet not found" body for any walletId that the caller's account
// does not own (including valid UUIDs that simply don't exist).
func (s *WalletsControllerTestSuite) TestGetWallet_NotFound() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/wallets/"+uuid.NewString(), bearer).
		AssertNotFound()
}

// TestGetWallet_InvalidUUID — the middleware intentionally returns 404 (not
// 400) for malformed walletIds on the external API so callers can't probe
// for the difference between "malformed" and "not yours". This is the
// documented behaviour of middleware.APIWalletContext.
func (s *WalletsControllerTestSuite) TestGetWallet_InvalidUUID() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/wallets/not-a-uuid", bearer).
		AssertNotFound()
}

// TestGetWallet_OtherAccount — IDOR guard: a wallet owned by account A is
// indistinguishable from "does not exist" when looked up by account B.
func (s *WalletsControllerTestSuite) TestGetWallet_OtherAccount() {
	testutil.SeededTestDB(s.T())
	accountA, _, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	_, bearerB, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	walletA := seedAPIWalletForAccount(s.T(), accountA, "eth", "A-owned")

	ctltestutil.
		Get(s.T(), &s.TestCase, "/api/v1/wallets/"+walletA, bearerB).
		AssertNotFound()
}
