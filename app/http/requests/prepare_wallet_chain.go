package requests

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/validation"

	"github.com/macrowallets/waas/app/http/middleware/requestctx"
)

// PrepareWalletChain copies the chain from the wallet middleware already stored.
// A missing wallet leaves the chain unset.
func PrepareWalletChain(ctx http.Context, data validation.Data) error {
	wallet, ok := requestctx.Wallet(ctx)
	if !ok || wallet == nil {
		return nil
	}
	return data.Set("_chain", wallet.Chain)
}
