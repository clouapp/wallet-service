package blockheight

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const (
	esploraHTTPTimeout      = 5 * time.Second
	esploraMaxResponseBytes = 1 << 10
)

// BlockstreamProvider reads Bitcoin mainnet and testnet3 tips from Blockstream. It
// does not serve testnet4: see BitcoinProvider.
type BlockstreamProvider struct {
	client     *http.Client
	mainnetURL string
	testnetURL string
}

func NewBlockstreamProvider() *BlockstreamProvider {
	return &BlockstreamProvider{
		client:     httpclient.New(esploraHTTPTimeout),
		mainnetURL: "https://blockstream.info/api/blocks/tip/height",
		testnetURL: "https://blockstream.info/testnet/api/blocks/tip/height",
	}
}

func (p *BlockstreamProvider) heightURL(chainID string) (string, error) {
	switch chainID {
	case models.ChainBTC:
		return p.mainnetURL, nil
	case models.ChainTBTC:
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
	return fetchEsploraTipHeight(ctx, p.client, u, "blockstream")
}

// fetchEsploraTipHeight reads an Esplora GET /blocks/tip/height body: a bare decimal.
func fetchEsploraTipHeight(ctx context.Context, client *http.Client, heightURL, source string) (uint64, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, heightURL, nil)
	if err != nil {
		return 0, fmt.Errorf("%s: build request: %w", source, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("%s: http: %w", source, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, esploraMaxResponseBytes))
	if err != nil {
		return 0, fmt.Errorf("%s: read body: %w", source, err)
	}

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("%s: unexpected status %d: %s", source, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	s := strings.TrimSpace(string(body))
	height, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s: parse height %q: %w", source, s, err)
	}
	if height == 0 {
		return 0, fmt.Errorf("%s: tip height is zero", source)
	}

	return height, nil
}
