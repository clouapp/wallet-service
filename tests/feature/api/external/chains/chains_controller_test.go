package chains

import (
	"encoding/json"
	"testing"

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
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	resp := s.External("/api/v1/chains", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []map[string]any `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotEmpty(payload.Data, "seeded chains fixture should return at least one chain")
}

// TestListChains_ExternalAPI_Unauthenticated returns 401 when no bearer is present.
func (s *ChainsControllerTestSuite) TestListChains_ExternalAPI_Unauthenticated() {
	resp := s.External("/api/v1/chains", ctltestutil.Token{}).Get()
	s.AssertError(resp, 401, "unauthorized", "missing bearer token")
}
