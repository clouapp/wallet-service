package controllers

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/container"
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
	wallet, _, _, errResp := walletFromParam(ctx)
	if errResp != nil {
		return errResp
	}

	c := container.Get()
	adapter, err := c.Registry.Chain(wallet.Chain)
	if err != nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{
			"error": "chain adapter not available for: " + wallet.Chain,
		})
	}

	if wallet.DepositAddress == nil {
		return ctx.Response().Json(http.StatusUnprocessableEntity, http.Json{"error": "wallet has no deposit address"})
	}
	depositAddr := wallet.DepositAddress.Address

	balance, err := adapter.GetBalance(ctx.Context(), depositAddr)
	if err != nil {
		return ctx.Response().Json(http.StatusInternalServerError, http.Json{"error": "failed to fetch balance: " + err.Error()})
	}

	result := []UnspentOutput{}
	if balance != nil && balance.Amount != nil && balance.Amount.Sign() > 0 {
		result = append(result, UnspentOutput{
			TxHash:  "",
			Vout:    0,
			Value:   balance.Amount.Int64(),
			Height:  0,
			Address: depositAddr,
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
