package blockheight

import (
	"context"
	"fmt"
)

// TipSourceBitcoinTestnet4 is the provider key for the Bitcoin testnet4 tip. No
// chain record has this id: NetworkRouted maps records resolving to testnet4 to it.
const TipSourceBitcoinTestnet4 = "btc-testnet4"

// BitcoinProvider serves every Bitcoin tip: testnet4 from the injected reader
// (mempool.space), mainnet and testnet3 from the injected Blockstream reader.
// Blockstream only knows testnet3, so a testnet4 key never reaches it.
type BitcoinProvider struct {
	blockstream Provider
	testnet4    Provider
}

// BitcoinDeps is the Bitcoin tip readers. A nil Blockstream leaves mainnet and
// testnet3 unconfigured. A nil Testnet4 leaves that key unconfigured.
type BitcoinDeps struct {
	Blockstream Provider
	Testnet4    Provider
}

func NewBitcoinProvider(deps BitcoinDeps) *BitcoinProvider {
	return &BitcoinProvider{
		blockstream: deps.Blockstream,
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
	if p.blockstream == nil {
		return 0, fmt.Errorf("blockstream: provider is not configured")
	}
	return p.blockstream.GetBlockHeight(ctx, key)
}
