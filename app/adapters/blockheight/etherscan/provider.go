package etherscan

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	httpTimeout          = 5 * time.Second
	etherscanDefaultBase = "https://api.etherscan.io"
)

// Provider reads EVM tips from Etherscan. The service keeps the Provider port.
type Provider struct {
	apiKey   string
	keyAtUse blockheight.EtherscanKey
	client   *httpclient.Client
	baseURL  string
}

var _ blockheight.Provider = (*Provider)(nil)

// New returns the Etherscan tip reader. A nil KeyAtUse keeps APIKey on every
// height read. A blank key from KeyAtUse sends that call to the chain RPC.
func New(deps blockheight.EtherscanDeps) *Provider {
	return &Provider{
		apiKey:   deps.APIKey,
		keyAtUse: deps.KeyAtUse,
		client:   httpclient.NewClient(httpTimeout),
		baseURL:  etherscanDefaultBase,
	}
}

func etherscanChainID(internal string) (string, error) {
	switch internal {
	case models.ChainETH:
		return "1", nil
	case models.ChainPolygon:
		return "137", nil
	case models.ChainTETH:
		return "11155111", nil
	case models.ChainTPolygon:
		return "80002", nil
	default:
		return "", fmt.Errorf("etherscan: unknown chain_id %q", internal)
	}
}

type etherscanBlockNumberResp struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
	Result  string `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *Provider) GetBlockHeight(ctx context.Context, chainID string) (height uint64, err error) {
	var reqURL string
	defer func() { err = httpclient.RedactURL(err, reqURL) }()
	eid, err := etherscanChainID(chainID)
	if err != nil {
		return 0, err
	}

	apiKey := strings.TrimSpace(p.apiKey)
	if p.keyAtUse != nil {
		if ctx == nil {
			ctx = context.Background()
		}
		apiKey = strings.TrimSpace(p.keyAtUse(ctx))
		if apiKey == "" {
			return 0, blockheight.ErrTipFromChainRPC
		}
	}

	q := url.Values{}
	q.Set("chainid", eid)
	q.Set("module", "proxy")
	q.Set("action", "eth_blockNumber")
	if apiKey != "" {
		q.Set("apikey", apiKey)
	}

	base := strings.TrimSuffix(p.baseURL, "/")
	reqURL = fmt.Sprintf("%s/v2/api?%s", base, q.Encode())

	resp, err := p.client.Do(ctx, httpclient.Request{Method: httpclient.MethodGet, URL: reqURL})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, fmt.Errorf("etherscan: build request: %w", err)
		}
		return 0, chain.Unavailable(fmt.Errorf("etherscan: http: %w", err))
	}

	if resp.StatusCode != httpclient.StatusOK {
		return 0, chain.FromProviderHTTP(resp.StatusCode, httpclient.RedactURLText(strings.TrimSpace(string(resp.Body)), reqURL))
	}

	var env etherscanBlockNumberResp
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return 0, fmt.Errorf("etherscan: decode json: %w", err)
	}

	if env.Error != nil {
		return 0, chain.Wrap(chain.KindOrProvider(0, env.Error.Message), fmt.Errorf("etherscan: rpc error %d: %s", env.Error.Code, httpclient.RedactURLText(env.Error.Message, reqURL)))
	}

	result := strings.TrimSpace(env.Result)
	if result == "" {
		return 0, fmt.Errorf("etherscan: empty result")
	}

	hexStr := strings.TrimPrefix(strings.TrimPrefix(result, "0x"), "0X")
	height, err = strconv.ParseUint(hexStr, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("etherscan: parse block height %q: %w", result, err)
	}

	return height, nil
}
