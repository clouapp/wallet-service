package solana

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	httpTimeout = 5 * time.Second
	mainnetRPC  = "https://api.mainnet-beta.solana.com"
	devnetRPC   = "https://api.devnet.solana.com"
	getSlotBody = `{"jsonrpc":"2.0","id":1,"method":"getSlot","params":[{"commitment":"finalized"}]}`
)

// Provider reads Solana mainnet and devnet tips from the public RPC.
// The service keeps the Provider port.
type Provider struct {
	client     *httpclient.Client
	mainnetRPC string
	devnetRPC  string
}

var _ blockheight.Provider = (*Provider)(nil)

// New returns the Solana public RPC tip reader.
func New() *Provider {
	return &Provider{
		client:     httpclient.NewClient(httpTimeout),
		mainnetRPC: mainnetRPC,
		devnetRPC:  devnetRPC,
	}
}

func (p *Provider) rpcURL(chainID string) (string, error) {
	switch chainID {
	case models.ChainSOL:
		return p.mainnetRPC, nil
	case models.ChainTSOL:
		return p.devnetRPC, nil
	default:
		return "", fmt.Errorf("solana: unknown chain_id %q", chainID)
	}
}

type slotResp struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  json.Number `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *Provider) GetBlockHeight(ctx context.Context, chainID string) (height uint64, err error) {
	var u string
	defer func() { err = httpclient.RedactURL(err, u) }()
	u, err = p.rpcURL(chainID)
	if err != nil {
		return 0, err
	}

	resp, err := p.client.Do(ctx, httpclient.Request{
		Method:  httpclient.MethodPost,
		URL:     u,
		Header:  map[string]string{"Content-Type": "application/json"},
		Body:    []byte(getSlotBody),
		HasBody: true,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, fmt.Errorf("solana: build request: %w", err)
		}
		if httpclient.IsRead(err) {
			return 0, fmt.Errorf("solana: read body: %w", err)
		}
		return 0, fmt.Errorf("solana: http: %w", err)
	}

	if resp.StatusCode != httpclient.StatusOK {
		return 0, fmt.Errorf("solana: unexpected status %d: %s", resp.StatusCode, string(resp.Body))
	}

	var out slotResp
	if err := json.Unmarshal(resp.Body, &out); err != nil {
		return 0, fmt.Errorf("solana: decode json: %w", err)
	}

	if out.Error != nil {
		return 0, fmt.Errorf("solana: rpc error %d: %s", out.Error.Code, out.Error.Message)
	}

	if out.Result == "" {
		return 0, fmt.Errorf("solana: empty result")
	}

	v, err := out.Result.Int64()
	if err != nil {
		return 0, fmt.Errorf("solana: result not integer: %w", err)
	}
	if v < 0 {
		return 0, fmt.Errorf("solana: negative slot %d", v)
	}

	return uint64(v), nil
}
