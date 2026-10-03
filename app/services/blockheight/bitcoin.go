package blockheight

import (
	"context"
	"fmt"

	"github.com/macrowallets/waas/pkg/httpclient"
)

// TipSourceBitcoinTestnet4 is the provider key for the Bitcoin testnet4 tip. No
// chain record has this id: NetworkRouted maps records resolving to testnet4 to it.
const TipSourceBitcoinTestnet4 = "btc-testnet4"

// MempoolTestnet4Provider reads the Bitcoin testnet4 tip from mempool.space.
type MempoolTestnet4Provider struct {
	client *httpclient.Client
	url    string
}

func NewMempoolTestnet4Provider() *MempoolTestnet4Provider {
	return &MempoolTestnet4Provider{
		client: httpclient.NewClient(esploraHTTPTimeout),
		url:    "https://mempool.space/testnet4/api/blocks/tip/height",
	}
}

func (p *MempoolTestnet4Provider) GetBlockHeight(ctx context.Context, key string) (uint64, error) {
	if key != TipSourceBitcoinTestnet4 {
		return 0, fmt.Errorf("mempool testnet4: unknown chain_id %q", key)
	}
	return fetchEsploraTipHeight(ctx, p.client, p.url, "mempool testnet4")
}

// BitcoinProvider serves every Bitcoin tip: testnet4 from mempool.space, mainnet and
// testnet3 from Blockstream. Blockstream only knows testnet3, so a testnet4 key never
// reaches it.
type BitcoinProvider struct {
	blockstream *BlockstreamProvider
	testnet4    *MempoolTestnet4Provider
}

func NewBitcoinProvider() *BitcoinProvider {
	return &BitcoinProvider{
		blockstream: NewBlockstreamProvider(),
		testnet4:    NewMempoolTestnet4Provider(),
	}
}

func (p *BitcoinProvider) GetBlockHeight(ctx context.Context, key string) (uint64, error) {
	if key == TipSourceBitcoinTestnet4 {
		return p.testnet4.GetBlockHeight(ctx, key)
	}
	return p.blockstream.GetBlockHeight(ctx, key)
}
