package routes

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/http/controllers"
	"github.com/macromarkets/vault/app/http/middleware"
)

func Api() {
	// Health check - no auth
	facades.Route().Get("/health", func(ctx http.Context) http.Response {
		return ctx.Response().Json(http.StatusOK, http.Json{
			"status":  "ok",
			"version": "0.1.0",
		})
	})

	// Swagger documentation - no auth
	facades.Route().Get("/swagger/*any", func(ctx http.Context) http.Response {
		// Swagger will be handled separately
		return ctx.Response().Success().String("Swagger UI")
	})

	// API v1 group - authenticated
	facades.Route().Prefix("/v1").Middleware(middleware.HMACAuth).Group(func(router route.Router) {
		// Chains
		router.Get("/chains", controllers.ListChains)

		// Wallets
		router.Post("/wallets", controllers.CreateWallet)
		router.Get("/wallets", controllers.ListWallets)
		router.Get("/wallets/{id}", controllers.GetWallet)

		// Addresses
		router.Post("/wallets/{id}/addresses", controllers.GenerateAddress)
		router.Get("/wallets/{id}/addresses", controllers.ListWalletAddresses)
		router.Get("/addresses/{address}", controllers.LookupAddress)
		router.Get("/users/{external_id}/addresses", controllers.ListUserAddresses)

		// Withdrawals
		router.Post("/wallets/{id}/withdrawals", controllers.CreateWithdrawal)

		// Transactions
		router.Get("/transactions", controllers.ListTransactions)
		router.Get("/transactions/{id}", controllers.GetTransaction)
		router.Get("/users/{external_id}/transactions", controllers.ListUserTransactions)

		// Webhooks
		router.Post("/webhooks", controllers.CreateWebhook)
		router.Get("/webhooks", controllers.ListWebhooks)
	})
}
