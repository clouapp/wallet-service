package mempool

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	httpTimeout      = 5 * time.Second
	maxResponseBytes = 1 << 10
	tipHeightURL     = "https://mempool.space/testnet4/api/blocks/tip/height"
)

// Provider reads the Bitcoin testnet4 tip from mempool.space.
// The service keeps the Provider port.
type Provider struct {
	client *httpclient.Client
	url    string
}

var _ blockheight.Provider = (*Provider)(nil)

// New returns the mempool.space testnet4 tip reader.
func New() *Provider {
	return &Provider{
		client: httpclient.NewClient(httpTimeout),
		url:    tipHeightURL,
	}
}

func (p *Provider) GetBlockHeight(ctx context.Context, key string) (uint64, error) {
	if key != blockheight.TipSourceBitcoinTestnet4 {
		return 0, fmt.Errorf("mempool testnet4: unknown chain_id %q", key)
	}
	return fetchEsploraTipHeight(ctx, p.client, p.url, "mempool testnet4")
}

// fetchEsploraTipHeight reads an Esplora GET /blocks/tip/height body: a bare decimal.
func fetchEsploraTipHeight(ctx context.Context, client *httpclient.Client, heightURL, source string) (uint64, error) {
	resp, err := client.Do(ctx, httpclient.Request{
		Method:   httpclient.MethodGet,
		URL:      heightURL,
		MaxBytes: maxResponseBytes,
	})
	if err != nil {
		if httpclient.IsBuild(err) {
			return 0, fmt.Errorf("%s: build request: %w", source, err)
		}
		if httpclient.IsRead(err) {
			return 0, fmt.Errorf("%s: read body: %w", source, err)
		}
		return 0, fmt.Errorf("%s: http: %w", source, err)
	}

	if resp.StatusCode != httpclient.StatusOK {
		return 0, fmt.Errorf("%s: unexpected status %d: %s", source, resp.StatusCode, strings.TrimSpace(string(resp.Body)))
	}

	s := strings.TrimSpace(string(resp.Body))
	height, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse height %q: %w", source, s, err)
	}
	if height == 0 {
		return 0, fmt.Errorf("%s: tip height is zero", source)
	}

	return height, nil
}
