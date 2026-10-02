package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/models"
)

// RegisterExternalAPI registers Bearer API-token routes under /api/v1.
//
// All per-wallet routes live inside a nested group that applies
// APIWalletContext() so the walletId in the URL is verified to belong to the
// authenticated account before any controller runs. This prevents IDOR: a
// valid API token cannot operate on another account's wallets.
// Each route also requires the scope the dashboard stores on the token.
func RegisterExternalAPI() {
	noCache := middleware.CacheControl(0)
	readWallets := middleware.APIScope(models.APIPermWalletsRead)
	writeWallets := middleware.APIScope(models.APIPermWalletsWrite)
	readTransactions := middleware.APIScope(models.APIPermTransactionsRead)
	writeWithdrawals := middleware.APIScope(models.APIPermWithdrawalsWrite)
	readWebhooks := middleware.APIScope(models.APIPermWebhooksRead)
	writeWebhooks := middleware.APIScope(models.APIPermWebhooksWrite)

	facades.Route().Prefix("/api/v1").Middleware(middleware.APITokenAuth(), noCache).Group(func(router route.Router) {
		router.Middleware(readWallets).Get("/chains", controllers.ListChains)

		router.Middleware(writeWallets).Post("/wallets", controllers.CreateWallet)
		router.Middleware(readWallets).Get("/wallets", controllers.ListWallets)

		router.Middleware(readWallets).Get("/addresses/{address}", controllers.LookupAddress)
		router.Middleware(readWallets).Get("/users/{external_id}/addresses", controllers.ListUserAddresses)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext()).Group(func(r route.Router) {
			r.Middleware(readWallets).Get("", controllers.GetWallet)

			r.Middleware(writeWallets).Post("/addresses", controllers.GenerateAddress)
			r.Middleware(readWallets).Get("/addresses", controllers.ListWalletAddresses)
			r.Middleware(writeWallets).Patch("/addresses/{addressId}", controllers.UpdateAddress)

			r.Middleware(writeWallets).Post("/consolidate", controllers.ConsolidateWallet)
			r.Middleware(readWallets).Get("/gas-status", controllers.GetGasStatus)
			r.Middleware(writeWallets).Post("/gas-check", controllers.ForceGasCheck)
			r.Middleware(writeWithdrawals).Post("/withdraw/preview", controllers.PreviewWithdraw)
			r.Middleware(writeWithdrawals).Post("/withdrawals", controllers.CreateWalletWithdrawal)
			r.Middleware(writeWithdrawals).Get("/withdrawals/{idempotencyKey}", controllers.GetWalletWithdrawalByIdempotencyKey)
		})

		router.Middleware(readTransactions).Get("/transactions", controllers.ListTransactions)
		router.Middleware(readTransactions).Get("/transactions/{id}", controllers.GetTransaction)
		router.Middleware(readTransactions).Get("/users/{external_id}/transactions", controllers.ListUserTransactions)

		router.Middleware(writeWebhooks).Post("/webhooks", controllers.CreateWebhook)
		router.Middleware(readWebhooks).Get("/webhooks", controllers.ListWebhooks)
		router.Middleware(writeWebhooks).Patch("/webhooks/{webhookId}", controllers.UpdateWebhook)
	})
}
