package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	extaddresses "github.com/macrowallets/waas/app/http/controllers/external/addresses"
	extchains "github.com/macrowallets/waas/app/http/controllers/external/chains"
	extsweep "github.com/macrowallets/waas/app/http/controllers/external/sweep"
	exttransactions "github.com/macrowallets/waas/app/http/controllers/external/transactions"
	extwallets "github.com/macrowallets/waas/app/http/controllers/external/wallets"
	extwebhooks "github.com/macrowallets/waas/app/http/controllers/external/webhooks"
	extwithdrawals "github.com/macrowallets/waas/app/http/controllers/external/withdrawals"
	"github.com/macrowallets/waas/app/http/middleware"
)

// RegisterExternalAPI registers Bearer API-token routes under /api/v1.
//
// All per-wallet routes live inside a nested group that applies
// APIWalletContext() so the walletId in the URL is verified to belong to the
// authenticated account before any controller runs. This prevents IDOR: a
// valid API token cannot operate on another account's wallets.
func RegisterExternalAPI() {
	noCache := middleware.CacheControl(0)

	facades.Route().Prefix("/api/v1").Middleware(middleware.APITokenAuth(), noCache).Group(func(router route.Router) {
		router.Get("/chains", extchains.ListChains)

		router.Post("/wallets", extwallets.CreateWallet)
		router.Get("/wallets", extwallets.ListWallets)

		router.Get("/addresses/{address}", extaddresses.LookupAddress)
		router.Get("/users/{external_id}/addresses", extaddresses.ListUserAddresses)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext()).Group(func(r route.Router) {
			r.Get("", extwallets.GetWallet)

			r.Post("/addresses", extaddresses.GenerateAddress)
			r.Get("/addresses", extaddresses.ListWalletAddresses)
			r.Patch("/addresses/{addressId}", extaddresses.UpdateAddress)

			r.Post("/consolidate", extsweep.ConsolidateWallet)
			r.Get("/gas-status", extsweep.GetGasStatus)
			r.Post("/gas-check", extsweep.ForceGasCheck)
			r.Post("/withdraw/preview", extsweep.PreviewWithdraw)
			r.Post("/withdrawals", extwithdrawals.CreateWalletWithdrawal)
			r.Get("/withdrawals/{idempotencyKey}", extwithdrawals.GetWalletWithdrawalByIdempotencyKey)
		})

		router.Get("/transactions", exttransactions.ListTransactions)
		router.Get("/transactions/{id}", exttransactions.GetTransaction)
		router.Get("/users/{external_id}/transactions", exttransactions.ListUserTransactions)

		router.Post("/webhooks", extwebhooks.CreateWebhook)
		router.Get("/webhooks", extwebhooks.ListWebhooks)
		router.Patch("/webhooks/{webhookId}", extwebhooks.UpdateWebhook)
	})
}
