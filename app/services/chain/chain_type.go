package chain

import "github.com/macrowallets/waas/app/models"

// UTXOChains is the set of chain family names that use the UTXO model.
// All Bitcoin-family chains belong here; account-model chains (EVM, Solana) do not.
var UTXOChains = map[string]bool{
	"bitcoin":      true,
	"bitcoin-cash": true,
	"litecoin":     true,
	"dogecoin":     true,
	"dash":         true,
}

// IsUTXO returns true if the given chain identifier uses the UTXO model: a chain
// record id served by the Bitcoin adapter (btc, tbtc, ltc, tltc) or a family name.
func IsUTXO(chainID string) bool {
	return models.IsBitcoinFamilyChainID(chainID) || UTXOChains[chainID]
}
