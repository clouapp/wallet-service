package blockheight

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const etherscanDefaultBase = "https://api.etherscan.io"

type EtherscanProvider struct {
	apiKey  string
	client  *http.Client
	baseURL string
}

func NewEtherscanProvider(apiKey string) *EtherscanProvider {
	return &EtherscanProvider{
		apiKey:  apiKey,
		client:  &http.Client{Timeout: 5 * time.Second},
		baseURL: etherscanDefaultBase,
	}
}

func etherscanChainID(internal string) (string, error) {
	switch internal {
	case "eth":
		return "1", nil
	case "polygon":
		return "137", nil
	case "teth":
		return "11155111", nil
	case "tpolygon":
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

	q := url.Values{}
	q.Set("chainid", eid)
	q.Set("module", "proxy")
	q.Set("action", "eth_blockNumber")
	if p.apiKey != "" {
		q.Set("apikey", p.apiKey)
	}

	base := strings.TrimSuffix(p.baseURL, "/")
	reqURL := fmt.Sprintf("%s/v2/api?%s", base, q.Encode())

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("etherscan: build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("etherscan: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("etherscan: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("etherscan: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var env etherscanBlockNumberResp
	if err := json.Unmarshal(body, &env); err != nil {
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
