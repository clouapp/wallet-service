package unspents

import (
	"strconv"

	"github.com/macrowallets/waas/app/models"
)

// UnspentOutput is one spendable output of a UTXO wallet.
type UnspentOutput struct {
	TxHash  string `json:"tx_hash"`
	Vout    uint32 `json:"vout"`
	Value   int64  `json:"value"`
	Height  uint64 `json:"height"`
	Address string `json:"address"`
}

// UnspentOutputs projects the spendable outputs. An output not yet in a block has
// height 0, and a value that is not a whole number is 0. The list is never null.
func UnspentOutputs(utxos []models.WalletUTXO) []UnspentOutput {
	outputs := make([]UnspentOutput, 0, len(utxos))
	for _, utxo := range utxos {
		var height uint64
		if utxo.BlockNumber != nil {
			height = uint64(*utxo.BlockNumber)
		}
		value, _ := strconv.ParseInt(utxo.ValueRaw, 10, 64)

		outputs = append(outputs, UnspentOutput{
			TxHash:  utxo.TxHash,
			Vout:    uint32(utxo.OutputIndex),
			Value:   value,
			Height:  height,
			Address: utxo.Address,
		})
	}
	return outputs
}
