package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
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
		router.Get("/chains", controllers.ListChains)

		router.Post("/wallets", controllers.CreateWallet)
		router.Get("/wallets", controllers.ListWallets)

		router.Get("/addresses/{address}", controllers.LookupAddress)
		router.Get("/users/{external_id}/addresses", controllers.ListUserAddresses)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext()).Group(func(r route.Router) {
			r.Get("", controllers.GetWallet)

			r.Post("/addresses", controllers.GenerateAddress)
			r.Get("/addresses", controllers.ListWalletAddresses)
			r.Patch("/addresses/{addressId}", controllers.UpdateAddress)

			r.Post("/consolidate", controllers.ConsolidateWallet)
			r.Get("/gas-status", controllers.GetGasStatus)
			r.Post("/gas-check", controllers.ForceGasCheck)
			r.Post("/withdraw/preview", controllers.PreviewWithdraw)
			r.Post("/withdrawals", controllers.CreateWalletWithdrawal)
			r.Get("/withdrawals/{idempotencyKey}", controllers.GetWalletWithdrawalByIdempotencyKey)
		})

		router.Get("/transactions", controllers.ListTransactions)
		router.Get("/transactions/{id}", controllers.GetTransaction)
		router.Get("/users/{external_id}/transactions", controllers.ListUserTransactions)

		router.Post("/webhooks", controllers.CreateWebhook)
		router.Get("/webhooks", controllers.ListWebhooks)
		router.Patch("/webhooks/{webhookId}", controllers.UpdateWebhook)
	})
}
