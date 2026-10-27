package chains

import (
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// TestC_Platform_ListShowsEveryChainWithoutAnyURL: GET /v1/platform/chains lists
// every seeded chain (mainnet and testnet, not filtered by an account's
// environment) with its settings, and never an RPC URL; a member that is not a
// platform admin is refused.
func (s *PlatformChainRPCTestSuite) TestC_Platform_ListShowsEveryChainWithoutAnyURL() {
	member := s.seedUser(false)
	memberSession := s.signIn(member.Email)
	s.Get("/v1/platform/chains", sessionOf(memberSession.AccessToken)).AssertStatus(403)

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	resp := s.Get("/v1/platform/chains", sessionOf(session.AccessToken))
	resp.AssertOk()
	raw, err := resp.Content()
	s.Require().NoError(err)
	s.NotContains(raw, "://", "no URL is returned")
	s.NotContains(raw, "rpc_url")

	var body struct {
		Data []struct {
			ID                    string `json:"id"`
			Name                  string `json:"name"`
			AdapterType           string `json:"adapter_type"`
			IsTestnet             bool   `json:"is_testnet"`
			Status                string `json:"status"`
			RequiredConfirmations int    `json:"required_confirmations"`
			Finality              string `json:"finality"`
		} `json:"data"`
	}
	s.decode(resp, &body)
	s.Require().NotEmpty(body.Data)
	byID := map[string]bool{}
	sawTestnet, sawMainnet := false, false
	for _, chain := range body.Data {
		byID[chain.ID] = true
		sawTestnet = sawTestnet || chain.IsTestnet
		sawMainnet = sawMainnet || !chain.IsTestnet
		s.NotEmpty(strings.TrimSpace(chain.Name))
		s.NotEmpty(chain.AdapterType)
		s.NotEmpty(chain.Finality)
		s.Positive(chain.RequiredConfirmations)
	}
	s.True(byID[models.ChainETH] && byID[models.ChainTETH], "both the mainnet and the testnet record are listed")
	s.True(sawTestnet && sawMainnet)
}
