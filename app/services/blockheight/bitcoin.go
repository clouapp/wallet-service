package blockheight

import (
	"context"
	"fmt"
)

// TipSourceBitcoinTestnet4 is the provider key for the Bitcoin testnet4 tip. No
// chain record has this id: NetworkRouted maps records resolving to testnet4 to it.
const TipSourceBitcoinTestnet4 = "btc-testnet4"

// BitcoinProvider serves every Bitcoin tip: testnet4 from the injected reader
// (mempool.space), mainnet and testnet3 from Blockstream. Blockstream only knows
// testnet3, so a testnet4 key never reaches it.
type BitcoinProvider struct {
	blockstream *BlockstreamProvider
	testnet4    Provider
}

// BitcoinDeps is the testnet4 tip reader. A nil Testnet4 leaves that key unconfigured.
type BitcoinDeps struct {
	Testnet4 Provider
}

func NewBitcoinProvider(deps BitcoinDeps) *BitcoinProvider {
	return &BitcoinProvider{
		blockstream: NewBlockstreamProvider(),
		testnet4:    deps.Testnet4,
	}
}

func (p *BitcoinProvider) GetBlockHeight(ctx context.Context, key string) (uint64, error) {
	if key == TipSourceBitcoinTestnet4 {
		if p.testnet4 == nil {
			return 0, fmt.Errorf("mempool testnet4: provider is not configured")
		}
		return p.testnet4.GetBlockHeight(ctx, key)
	}
	return p.blockstream.GetBlockHeight(ctx, key)
}
