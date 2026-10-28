package chains

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// ChainsControllerTestSuite covers both auth surfaces the chains list handler
// is mounted on:
//
//   - /v1/chains — dashboard SessionAuth + AccountHeader. We only assert the
//     401 rejection here because minting a session JWT inside tests requires
//     a helper that has not been extracted from auth_controller yet (tracked
//     in docs/superpowers/specs/2026-04-18-base-address-sweep-follow-ups.md).
//
//   - /api/v1/chains — external APITokenAuth. SetupAPIAuth seeds an account
//     and access token, so we can exercise the live handler end-to-end with
//     the seeded `chains` fixtures.
type ChainsControllerTestSuite struct {
	ctltestutil.HTTPSuite
}

func TestChains_Controller_Suite(t *testing.T) {
	ctltestutil.RunSuite(t, new(ChainsControllerTestSuite))
}

// TestListChains_Dashboard_Unauthenticated returns 401 without a Bearer token.
// /v1/chains is guarded by SessionAuth; the handler never runs.
func (s *ChainsControllerTestSuite) TestListChains_Dashboard_Unauthenticated() {
	resp := s.Get("/v1/chains", ctltestutil.Session{})
	s.AssertError(resp, 401, "unauthorized", "missing or malformed bearer token")
}

// TestListChains_ExternalAPI_Success exercises the /api/v1/chains route with
// a real access-token-backed bearer. The `chains` table is seeded by
// SeededTestDB so the live handler returns the seeded list.
func (s *ChainsControllerTestSuite) TestListChains_ExternalAPI_Success() {
	s.assertExternalChainsEnvironment(models.EnvironmentProd)
}

func (s *ChainsControllerTestSuite) TestListChains_ExternalAPI_TestEnvironment() {
	s.assertExternalChainsEnvironment(models.EnvironmentTest)
}

// The environment is read from the authenticated account, never a caller's
// header. Both paths exercise the actual APITokenAuth middleware.
func (s *ChainsControllerTestSuite) assertExternalChainsEnvironment(environment string) {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.Require().NoError(repositories.NewAccountRepository(nil).SetEnvironment(context.Background(), accountID, environment))

	resp := s.External("/api/v1/chains", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotEmpty(payload.Data, "seeded chains fixture should return at least one chain")
	for _, chain := range payload.Data {
		s.Equal(environment == models.EnvironmentTest, chain["is_testnet"], "chain %v must match the authenticated account environment", chain["id"])
	}
}

// TestListChains_ExternalAPI_Unauthenticated returns 401 when no bearer is present.
func (s *ChainsControllerTestSuite) TestListChains_ExternalAPI_Unauthenticated() {
	resp := s.External("/api/v1/chains", ctltestutil.Token{}).Get()
	s.AssertError(resp, 401, "unauthorized", "missing bearer token")
}

// TestListChainTokens_ExternalAPI_Success lists the seeded tokens of a chain of
// the token account's environment: the rows Markets reads to tell which assets a
// chain supports (chain_id, symbol, contract_address, decimals, status).
func (s *ChainsControllerTestSuite) TestListChainTokens_ExternalAPI_Success() {
	bearer := s.externalBearer(models.EnvironmentProd)

	resp := s.External("/api/v1/chains/eth/tokens", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotEmpty(payload.Data, "seeded tokens fixture should list at least one eth token")
	for _, token := range payload.Data {
		s.Equal("eth", token["chain_id"])
		for _, field := range []string{"id", "symbol", "contract_address", "decimals", "status"} {
			s.Contains(token, field)
		}
	}
}

// TestListChainTokens_ExternalAPI_OtherEnvironment refuses a chain of the other
// network kind with 403, as the dashboard does.
func (s *ChainsControllerTestSuite) TestListChainTokens_ExternalAPI_OtherEnvironment() {
	bearer := s.externalBearer(models.EnvironmentTest)

	resp := s.External("/api/v1/chains/eth/tokens", ctltestutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 403, "forbidden", "chain not available in current environment")
}

// TestListChainTokens_ExternalAPI_UnknownChain answers 404 for a chain the
// catalogue does not hold.
func (s *ChainsControllerTestSuite) TestListChainTokens_ExternalAPI_UnknownChain() {
	bearer := s.externalBearer(models.EnvironmentProd)

	resp := s.External("/api/v1/chains/nochain/tokens", ctltestutil.Token{Bearer: bearer}).Get()
	s.AssertError(resp, 404, "not_found", "chain not found")
}

// TestListChainTokens_ExternalAPI_Unauthenticated returns 401 when no bearer is present.
func (s *ChainsControllerTestSuite) TestListChainTokens_ExternalAPI_Unauthenticated() {
	resp := s.External("/api/v1/chains/eth/tokens", ctltestutil.Token{}).Get()
	s.AssertError(resp, 401, "unauthorized", "missing bearer token")
}

// externalBearer seeds the catalogue and an API token whose account is of the
// given environment.
func (s *ChainsControllerTestSuite) externalBearer(environment string) string {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	s.Require().NoError(repositories.NewAccountRepository(nil).SetEnvironment(context.Background(), accountID, environment))
	return bearer
}
