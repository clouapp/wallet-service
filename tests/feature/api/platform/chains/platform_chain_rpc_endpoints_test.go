package chains

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/responses"
	"github.com/macrowallets/waas/app/models"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support"
)

// endpointNode is a JSON-RPC endpoint that answers eth_chainId with chainIDHex
// and, unless failing, eth_blockNumber with 0x20. It counts the block number
// calls that reached the path holding its token.
type endpointNode struct {
	server *httptest.Server
	token  string
	served atomic.Int32
}

func newEndpointNode(chainIDHex string, failing bool) *endpointNode {
	node := &endpointNode{token: "k" + strings.ReplaceAll(uuid.NewString(), "-", "")}
	node.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == "eth_chainId":
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"` + chainIDHex + `"}`))
		case failing:
			w.WriteHeader(http.StatusServiceUnavailable)
		default:
			if strings.Contains(r.URL.Path, node.token) {
				node.served.Add(1)
			}
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"0x20"}`))
		}
	}))
	return node
}

func (n *endpointNode) URL() string { return n.server.URL + "/v2/" + n.token }

func (n *endpointNode) Host() string {
	parsed, _ := url.Parse(n.server.URL)
	return parsed.Host
}

type endpointListBody struct {
	ChainID   string `json:"chainId"`
	FailsOver bool   `json:"failsOver"`
	Changed   bool   `json:"changed"`
	Reloaded  bool   `json:"reloaded"`
	Endpoints []struct {
		Position int    `json:"position"`
		Primary  bool   `json:"primary"`
		Host     string `json:"host"`
		Enabled  bool   `json:"enabled"`
	} `json:"endpoints"`
}

func (s *PlatformChainRPCTestSuite) seededChainIDHex() string {
	s.T().Helper()
	seeded := s.loadChain(models.ChainETH)
	s.Require().NotNil(seeded.NetworkID)
	return fmt.Sprintf("0x%x", *seeded.NetworkID)
}

func (s *PlatformChainRPCTestSuite) endpointList(resp interface {
	Content() (string, error)
}, secrets ...string) endpointListBody {
	s.T().Helper()
	content, err := resp.Content()
	s.Require().NoError(err)
	for _, secret := range secrets {
		s.NotContains(content, secret, "a response carried an endpoint secret")
	}
	s.NotContains(content, "://", "a response carried a URL")
	var body endpointListBody
	s.Require().NoError(json.Unmarshal([]byte(content), &body), content)
	return body
}

func (s *PlatformChainRPCTestSuite) TestB_Platform_AdminBuildsTheListTheAdapterFailsOverThrough() {
	chainIDHex := s.seededChainIDHex()
	primary := newEndpointNode(chainIDHex, true)
	defer primary.server.Close()
	fallback := newEndpointNode(chainIDHex, false)
	defer fallback.server.Close()
	restore := s.installEthDialer()
	defer restore()

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	base := "/v1/platform/chains/" + models.ChainETH

	s.patchRaw(session.AccessToken, base+"/rpc", `{"rpcUrl":"`+primary.URL()+`"}`).AssertOk()

	added := s.Post(base+"/rpc-endpoints", sessionOf(session.AccessToken), `{"url":"`+fallback.URL()+`"}`)
	added.AssertOk()
	body := s.endpointList(added, primary.token, fallback.token)
	s.True(body.Changed)
	s.True(body.Reloaded, "the API process uses the new list at once")
	s.Require().Len(body.Endpoints, 2)
	s.Equal(primary.Host(), body.Endpoints[0].Host)
	s.True(body.Endpoints[0].Primary)
	s.Equal(fallback.Host(), body.Endpoints[1].Host)
	s.Equal(2, body.Endpoints[1].Position)

	var stored []models.ChainRPCEndpoint
	s.Require().NoError(facades.Orm().Query().Where("chain_id", models.ChainETH).Find(&stored))
	s.Require().Len(stored, 1)
	s.True(settings.IsSealed(stored[0].URL))
	s.NotContains(stored[0].URL, fallback.token)
	opened, err := settings.OpenStored(facades.Crypt(), stored[0].URL)
	s.Require().NoError(err)
	s.Equal(fallback.URL(), opened)

	adapter, err := container.MustMake[*chainpkg.Registry]().Chain(models.ChainETH)
	s.Require().NoError(err)
	block, err := adapter.GetLatestBlock(context.Background())
	s.Require().NoError(err)
	s.Equal(uint64(0x20), block)
	s.Equal(int32(1), fallback.served.Load(), "the primary failed and the fallback served the call")

	s.Equal(int64(1), s.count(
		`SELECT count(*) FROM account_activity
		 WHERE action = 'chains.updated' AND target_id = ? AND actor_user_id = ?
		   AND metadata = '{"fields":["rpc_endpoints"],"key":"eth"}'::jsonb`,
		models.ChainETH, admin.ID,
	))
	s.Equal(int64(0), s.count(`SELECT count(*) FROM account_activity WHERE metadata::text LIKE '%://%'`))

	listed := s.Get(base+"/rpc-endpoints", sessionOf(session.AccessToken))
	listed.AssertOk()
	s.Len(s.endpointList(listed, primary.token, fallback.token).Endpoints, 2)

	disabled := s.Patch(base+"/rpc-endpoints/2", sessionOf(session.AccessToken), `{"enabled":false}`)
	disabled.AssertOk()
	s.False(s.endpointList(disabled).Endpoints[1].Enabled)

	promoted := s.Post(base+"/rpc-endpoints/order", sessionOf(session.AccessToken), `{"order":[2,1]}`)
	promoted.AssertUnprocessableEntity()
	s.assertValidation(promoted, "order", "a disabled endpoint cannot be the primary; enable it first")

	primaryRemoval := s.Delete(base+"/rpc-endpoints/1", sessionOf(session.AccessToken), nil)
	primaryRemoval.AssertUnprocessableEntity()

	removed := s.Delete(base+"/rpc-endpoints/2", sessionOf(session.AccessToken), nil)
	removed.AssertOk()
	s.Len(s.endpointList(removed).Endpoints, 1)
	s.Equal(int64(0), s.count(`SELECT count(*) FROM chain_rpc_endpoints`))

	replaced := s.Put(base+"/rpc-endpoints", sessionOf(session.AccessToken), `{"urls":["`+fallback.URL()+`","`+primary.URL()+`"]}`)
	replaced.AssertOk()
	after := s.endpointList(replaced, primary.token, fallback.token)
	s.Equal(fallback.Host(), after.Endpoints[0].Host, "the first URL is the new primary")
	reopened, err := settings.OpenStored(facades.Crypt(), s.loadChain(models.ChainETH).RpcURL)
	s.Require().NoError(err)
	s.Equal(fallback.URL(), reopened)
}

func (s *PlatformChainRPCTestSuite) TestB_Platform_ListRefusesAnotherNetworkANonAdminAndAnUnknownChain() {
	chainIDHex := s.seededChainIDHex()
	primary := newEndpointNode(chainIDHex, true)
	defer primary.server.Close()
	otherNetwork := newEndpointNode("0x89", false)
	defer otherNetwork.server.Close()

	member := s.seedUser(false)
	memberSession := s.signIn(member.Email)
	forbidden := s.Post("/v1/platform/chains/eth/rpc-endpoints", sessionOf(memberSession.AccessToken), `{"url":"`+otherNetwork.URL()+`"}`)
	s.AssertError(forbidden, 403, responses.CodeForbidden, "you do not have permission to update chains")
	s.AssertError(s.Get("/v1/platform/chains/eth/rpc-endpoints", sessionOf(memberSession.AccessToken)), 403, responses.CodeForbidden, "you do not have permission to update chains")

	admin := s.seedUser(false)
	s.grantPlatformAdmin(admin.ID)
	session := s.signIn(admin.Email)
	s.AssertError(s.Get("/v1/platform/chains/no-such-chain/rpc-endpoints", sessionOf(session.AccessToken)), 404, responses.CodeNotFound, "chain not found")
	s.patchRaw(session.AccessToken, "/v1/platform/chains/eth/rpc", `{"rpcUrl":"`+primary.URL()+`"}`).AssertOk()

	wrong := s.Post("/v1/platform/chains/eth/rpc-endpoints", sessionOf(session.AccessToken), `{"url":"`+otherNetwork.URL()+`"}`)
	wrong.AssertUnprocessableEntity()
	content := s.quietBody(wrong, otherNetwork.token)
	s.Contains(content, "serves another network")
	s.Equal(int64(0), s.count(`SELECT count(*) FROM chain_rpc_endpoints`))

	badBody := s.Post("/v1/platform/chains/eth/rpc-endpoints", sessionOf(session.AccessToken), `{"rpcUrl":"x"}`)
	badBody.AssertUnprocessableEntity()
	s.assertValidation(badBody, "url", "is required")
	s.AssertError(s.Patch("/v1/platform/chains/eth/rpc-endpoints/two", sessionOf(session.AccessToken), `{"enabled":true}`),
		400, responses.CodeInvalidRequest, "position must be a positive integer")
	s.Equal(int64(1), s.count(`SELECT count(*) FROM account_activity WHERE action = 'chains.updated'`), "only the rpc_url change was recorded")
}

func sessionOf(accessToken string) support.Session {
	return support.Session{AccessToken: accessToken}
}
