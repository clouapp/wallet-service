package blockheight

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type BlockstreamProvider struct {
	client     *http.Client
	mainnetURL string
	testnetURL string
}

func NewBlockstreamProvider() *BlockstreamProvider {
	return &BlockstreamProvider{
		client:     &http.Client{Timeout: 5 * time.Second},
		mainnetURL: "https://blockstream.info/api/blocks/tip/height",
		testnetURL: "https://blockstream.info/testnet/api/blocks/tip/height",
	}
}

func (p *BlockstreamProvider) heightURL(chainID string) (string, error) {
	switch chainID {
	case "btc":
		return p.mainnetURL, nil
	case "tbtc":
		return p.testnetURL, nil
	default:
		return "", fmt.Errorf("blockstream: unknown chain_id %q", chainID)
	}
}

func (p *BlockstreamProvider) GetBlockHeight(ctx context.Context, chainID string) (uint64, error) {
	u, err := p.heightURL(chainID)
	if err != nil {
		return 0, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return 0, fmt.Errorf("blockstream: build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("blockstream: http: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("blockstream: read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("blockstream: unexpected status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	s := strings.TrimSpace(string(body))
	height, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("blockstream: parse height %q: %w", s, err)
	}

	return height, nil
}
