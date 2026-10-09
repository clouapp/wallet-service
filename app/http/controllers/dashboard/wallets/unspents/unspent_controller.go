package unspents

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
	unspentresources "github.com/macrowallets/waas/app/http/resources/dashboard/wallets/unspents"
	"github.com/macrowallets/waas/app/services/walletrecords"
)

// UnspentController serves the dashboard UTXO list route.
type UnspentController struct {
	utxos *walletrecords.UTXOs
}

// NewUnspentController wires the controller with the UTXO records.
func NewUnspentController(utxos *walletrecords.UTXOs) *UnspentController {
	if utxos == nil {
		panic("dashboard unspents controller: utxo service is required")
	}
	return &UnspentController{utxos: utxos}
}

// Index godoc
//
//	@Summary		List unspent transaction outputs (UTXOs)
//	@Description	Returns UTXOs for a UTXO-model wallet (e.g. bitcoin). Not available for account-model chains. Requires the UTXOOnly middleware to be applied.
//	@Tags			Wallet Unspents
//	@Security		BearerAuth
//	@Produce		json
//	@Param			walletId	path		string	true	"Wallet UUID"
//	@Success		200			{object}	UnspentOutputListResponse
//	@Failure		403			{object}	responses.ErrorBody
//	@Failure		404			{object}	responses.ErrorBody
//	@Failure		422			{object}	responses.ErrorBody	"Only available for UTXO chains"
//	@Router			/wallets/{walletId}/unspents [get]
func (c *UnspentController) Index(ctx http.Context) http.Response {
	wallet := requestctx.MustWallet(ctx)

	utxos, err := c.utxos.ListSpendable(ctx.Context(), wallet.ID, wallet.Chain)
	if err != nil {
		return mapError(ctx, err, "list unspent outputs")
	}

	return ctx.Response().Success().Json(http.Json{"data": unspentresources.UnspentOutputs(utxos)})
}

// UnspentOutputListResponse documents the UTXO list.
type UnspentOutputListResponse struct {
	Data []unspentresources.UnspentOutput `json:"data"`
}
