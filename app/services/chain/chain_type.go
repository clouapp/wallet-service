package chain

// UTXOChains is the set of chain identifiers that use the UTXO model.
// All Bitcoin-family chains belong here; account-model chains (EVM, Solana) do not.
var UTXOChains = map[string]bool{
	"bitcoin":      true,
	"bitcoin-cash": true,
	"litecoin":     true,
	"dogecoin":     true,
	"dash":         true,
}

// IsUTXO returns true if the given chain identifier uses the UTXO model.
func IsUTXO(chainID string) bool {
	return UTXOChains[chainID]
}
