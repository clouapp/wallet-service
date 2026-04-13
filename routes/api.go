package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
)

// RegisterExternalAPI registers Bearer API-token routes under /api/v1.
func RegisterExternalAPI() {
	noCache := middleware.CacheControl(0)

	facades.Route().Prefix("/api/v1").Middleware(middleware.APITokenAuth(), noCache).Group(func(router route.Router) {
		router.Get("/chains", controllers.ListChains)

		router.Post("/wallets", controllers.CreateWallet)
		router.Get("/wallets", controllers.ListWallets)
		router.Get("/wallets/{walletId}", controllers.GetWallet)

		router.Post("/wallets/{walletId}/addresses", controllers.GenerateAddress)
		router.Get("/wallets/{walletId}/addresses", controllers.ListWalletAddresses)
		router.Patch("/wallets/{walletId}/addresses/{addressId}", controllers.UpdateAddress)
		router.Get("/addresses/{address}", controllers.LookupAddress)
		router.Get("/users/{external_id}/addresses", controllers.ListUserAddresses)

		router.Get("/transactions", controllers.ListTransactions)
		router.Get("/transactions/{id}", controllers.GetTransaction)
		router.Get("/users/{external_id}/transactions", controllers.ListUserTransactions)

		router.Post("/webhooks", controllers.CreateWebhook)
		router.Get("/webhooks", controllers.ListWebhooks)
	})
}
