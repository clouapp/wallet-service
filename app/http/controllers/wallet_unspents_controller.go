package controllers

import (
	"strconv"

	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/models"
)

// ListUnspentOutputs godoc
// @Summary      List unspent transaction outputs (UTXOs)
// @Description  Returns UTXOs for a UTXO-model wallet (e.g. bitcoin). Not available for account-model chains. Requires the UTXOOnly middleware to be applied.
// @Tags         Wallet Unspents
// @Security     BearerAuth
// @Produce      json
// @Param        walletId  path    string  true   "Wallet UUID"
// @Success      200  {object}  UnspentOutputListResponse
// @Failure      403  {object}  ErrorResponse
// @Failure      404  {object}  ErrorResponse
// @Failure      422  {object}  ErrorResponse  "Only available for UTXO chains"
// @Router       /wallets/{walletId}/unspents [get]
func ListUnspentOutputs(ctx http.Context) http.Response {
	wallet := ctx.Value("wallet").(*models.Wallet)

	utxos, err := container.Get().WalletUTXORepo.ListSpendable(wallet.ID, wallet.Chain)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to list utxos: " + err.Error()})
	}

	result := make([]UnspentOutput, 0, len(utxos))
	for _, u := range utxos {
		var height uint64
		if u.BlockNumber != nil {
			height = uint64(*u.BlockNumber)
		}

		value, _ := strconv.ParseInt(u.ValueRaw, 10, 64)

		result = append(result, UnspentOutput{
			TxHash:  u.TxHash,
			Vout:    uint32(u.OutputIndex),
			Value:   value,
			Height:  height,
			Address: u.Address,
		})
	}

	return ctx.Response().Json(http.StatusOK, http.Json{"data": result})
}

// ---- Response types ----

// UnspentOutput represents a single UTXO.
type UnspentOutput struct {
	TxHash  string `json:"tx_hash"`
	Vout    uint32 `json:"vout"`
	Value   int64  `json:"value"`
	Height  uint64 `json:"height"`
	Address string `json:"address"`
}

type UnspentOutputListResponse struct {
	Data []UnspentOutput `json:"data"`
}
