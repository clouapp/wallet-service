package blockstream

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/blockheight"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	httpTimeout      = 5 * time.Second
	maxResponseBytes = 1 << 10
	mainnetTipURL    = "https://blockstream.info/api/blocks/tip/height"
	testnetTipURL    = "https://blockstream.info/testnet/api/blocks/tip/height"
)

// Provider reads Bitcoin mainnet and testnet3 tips from Blockstream. It does
// not serve testnet4. The service keeps the Provider port.
type Provider struct {
	client     *httpclient.Client
	mainnetURL string
	testnetURL string
}

var _ blockheight.Provider = (*Provider)(nil)

// New returns the Blockstream mainnet and testnet3 tip reader.
func New() *Provider {
	return &Provider{
		client:     httpclient.NewClient(httpTimeout),
		mainnetURL: mainnetTipURL,
		testnetURL: testnetTipURL,
	}
}

func (p *Provider) heightURL(chainID string) (string, error) {
	switch chainID {
	case models.ChainBTC:
		return p.mainnetURL, nil
	case models.ChainTBTC:
		return p.testnetURL, nil
	default:
		return "", fmt.Errorf("blockstream: unknown chain_id %q", chainID)
	}
}

func (p *Provider) GetBlockHeight(ctx context.Context, chainID string) (uint64, error) {
	u, err := p.heightURL(chainID)
	if err != nil {
		return 0, err
	}
	return fetchEsploraTipHeight(ctx, p.client, u, "blockstream")
}

// fetchEsploraTipHeight reads an Esplora GET /blocks/tip/height body: a bare decimal.
func fetchEsploraTipHeight(ctx context.Context, client *httpclient.Client, heightURL, source string) (height uint64, err error) {
	defer func() { err = httpclient.RedactURL(err, heightURL) }()
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
	height, err = strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse height %q: %w", source, s, err)
	}
	if height == 0 {
		return 0, fmt.Errorf("%s: tip height is zero", source)
	}

	return height, nil
}
