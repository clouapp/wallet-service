package blockheight

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/macrowallets/waas/app/models"
)

const solanaGetSlotBody = `{"jsonrpc":"2.0","id":1,"method":"getSlot","params":[{"commitment":"finalized"}]}`

type SolanaPublicProvider struct {
	client     *http.Client
	mainnetRPC string
	devnetRPC  string
}

func NewSolanaPublicProvider() *SolanaPublicProvider {
	return &SolanaPublicProvider{
		client:     &http.Client{Timeout: 5 * time.Second},
		mainnetRPC: "https://api.mainnet-beta.solana.com",
		devnetRPC:  "https://api.devnet.solana.com",
	}
}

func (p *SolanaPublicProvider) rpcURL(chainID string) (string, error) {
	switch chainID {
	case models.ChainSOL:
		return p.mainnetRPC, nil
	case models.ChainTSOL:
		return p.devnetRPC, nil
	default:
		return "", fmt.Errorf("solana: unknown chain_id %q", chainID)
	}
}

type solanaSlotResp struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  json.Number `json:"result"`
	Error   *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func (p *SolanaPublicProvider) GetBlockHeight(ctx context.Context, chainID string) (uint64, error) {
	u, err := p.rpcURL(chainID)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader([]byte(solanaGetSlotBody)))
	if err != nil {
		return 0, fmt.Errorf("solana: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("solana: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("solana: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("solana: unexpected status %d: %s", resp.StatusCode, string(body))
	}

	var out solanaSlotResp
	if err := json.Unmarshal(body, &out); err != nil {
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
