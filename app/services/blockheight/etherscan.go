package blockheight

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const etherscanDefaultBase = "https://api.etherscan.io"

type EtherscanProvider struct {
	apiKey   string
	keyAtUse EtherscanKey
	client   *httpclient.Client
	baseURL  string
}

// EtherscanDeps is everything the Etherscan block-height provider uses.
// A nil KeyAtUse keeps APIKey on every height read.
type EtherscanDeps struct {
	APIKey   string
	KeyAtUse EtherscanKey
}

func NewEtherscanProvider(deps EtherscanDeps) *EtherscanProvider {
	return &EtherscanProvider{
		apiKey:   deps.APIKey,
		keyAtUse: deps.KeyAtUse,
		client:   httpclient.NewClient(5 * time.Second),
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

func (p *EtherscanProvider) GetBlockHeight(ctx context.Context, chainID string) (uint64, error) {
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
			return 0, ErrTipFromChainRPC
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
	reqURL := fmt.Sprintf("%s/v2/api?%s", base, q.Encode())

	resp, err := p.client.Do(ctx, httpclient.Request{Method: httpclient.MethodGet, URL: reqURL})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, fmt.Errorf("etherscan: build request: %w", err)
		}
		if httpclient.IsRead(err) {
			return 0, fmt.Errorf("etherscan: read body: %w", err)
		}
		return 0, fmt.Errorf("etherscan: http: %w", err)
	}

	if resp.StatusCode != httpclient.StatusOK {
		return 0, fmt.Errorf("etherscan: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}

	var env etherscanBlockNumberResp
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return 0, fmt.Errorf("etherscan: decode json: %w", err)
	}

	if env.Error != nil {
		return 0, fmt.Errorf("etherscan: rpc error %d: %s", env.Error.Code, env.Error.Message)
	}

	result := strings.TrimSpace(env.Result)
	if result == "" {
		return 0, fmt.Errorf("etherscan: empty result")
	}

	hexStr := strings.TrimPrefix(strings.TrimPrefix(result, "0x"), "0X")
	height, err := strconv.ParseUint(hexStr, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("etherscan: parse block height %q: %w", result, err)
	}

	return height, nil
}
