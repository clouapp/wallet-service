package routes

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/http/controllers"
	"github.com/macromarkets/vault/app/http/middleware"
	"github.com/macromarkets/vault/docs"
)

func Api() {
	// Health check - no auth
	facades.Route().Get("/health", controllers.Health)

	// Swagger spec
	facades.Route().Get("/swagger/doc.json", func(ctx http.Context) http.Response {
		return ctx.Response().
			Header("Content-Type", "application/json").
			String(http.StatusOK, docs.SwaggerInfo.ReadDoc())
	})

	// Swagger UI (CDN-hosted)
	facades.Route().Get("/swagger/index.html", func(ctx http.Context) http.Response {
		html := `<!DOCTYPE html>
<html>
<head>
  <title>Vault API - Swagger UI</title>
  <meta charset="utf-8"/>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
</head>
<body>
<div id="swagger-ui"></div>
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
  SwaggerUIBundle({
    url: "/swagger/doc.json",
    dom_id: '#swagger-ui',
    presets: [SwaggerUIBundle.presets.apis, SwaggerUIBundle.SwaggerUIStandalonePreset],
    layout: "BaseLayout",
    deepLinking: true
  })
</script>
</body>
</html>`
		return ctx.Response().
			Header("Content-Type", "text/html; charset=utf-8").
			String(http.StatusOK, html)
	})

	// Auth routes — no HMAC, uses JWT
	facades.Route().Prefix("/v1/auth").Group(func(router route.Router) {
		router.Post("/register", controllers.Register)
		router.Post("/login", controllers.Login)
		router.Post("/2fa/verify", controllers.VerifyTwoFactor)
		router.Post("/refresh", controllers.RefreshToken)
		router.Post("/forgot-password", controllers.ForgotPassword)
		router.Post("/reset-password", controllers.ResetPassword)
	})
	// Logout requires session auth — separate group
	facades.Route().Prefix("/v1/auth").Middleware(middleware.SessionAuth).Group(func(router route.Router) {
		router.Post("/logout", controllers.Logout)
	})

	// User routes — JWT auth
	facades.Route().Prefix("/v1/users").Middleware(middleware.SessionAuth).Group(func(router route.Router) {
		router.Get("/me", controllers.GetMe)
		router.Patch("/me", controllers.UpdateMe)
		router.Post("/me/password", controllers.ChangePassword)
		router.Get("/me/accounts", controllers.ListMyAccounts)
	})

	// Account routes — JWT auth + account membership
	facades.Route().Prefix("/v1/accounts").Middleware(middleware.SessionAuth).Group(func(router route.Router) {
		router.Post("", controllers.CreateAccount)
		router.Prefix("/{accountId}").Middleware(middleware.AccountContext).Group(func(r route.Router) {
			r.Get("", controllers.GetAccount)
			r.Patch("", controllers.UpdateAccount)
			r.Post("/archive", controllers.ArchiveAccount)
			r.Post("/freeze", controllers.FreezeAccount)

			// Account users
			r.Get("/users", controllers.ListAccountUsers)
			r.Post("/users", controllers.AddAccountUser)
			r.Delete("/users/{userId}", controllers.RemoveAccountUser)

			// API tokens
			r.Get("/tokens", controllers.ListAccountTokens)
			r.Post("/tokens", controllers.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", controllers.RevokeAccountToken)
		})
	})

	// Wallet sub-resource routes — JWT auth
	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth).Group(func(router route.Router) {
		router.Prefix("/{walletId}").Group(func(r route.Router) {
			// Wallet users
			r.Get("/users", controllers.ListWalletUsers)
			r.Post("/users", controllers.AddWalletUser)
			r.Delete("/users/{userId}", controllers.RemoveWalletUser)

			// Whitelist
			r.Get("/whitelist", controllers.ListWhitelistEntries)
			r.Post("/whitelist", controllers.AddWhitelistEntry)
			r.Delete("/whitelist/{entryId}", controllers.DeleteWhitelistEntry)

			// Wallet-scoped webhooks
			r.Get("/webhooks", controllers.ListWalletWebhooks)
			r.Post("/webhooks", controllers.CreateWalletWebhook)
			r.Delete("/webhooks/{webhookId}", controllers.DeleteWalletWebhook)

			// Settings and freeze
			r.Get("/settings", controllers.GetWalletSettings)
			r.Patch("/settings", controllers.UpdateWalletSettings)
			r.Post("/freeze", controllers.FreezeWallet)

			// Wallet transactions (admin panel view)
			r.Get("/transactions", controllers.ListWalletTransactions)
			r.Get("/transactions/{txId}", controllers.GetWalletTransaction)

			// Wallet withdrawals (admin panel view)
			r.Get("/withdrawals", controllers.ListWalletWithdrawals)
			r.Post("/withdrawals", controllers.CreateWalletWithdrawal)
			r.Get("/withdrawals/{withdrawalId}", controllers.GetWalletWithdrawal)
			r.Post("/withdrawals/{withdrawalId}/cancel", controllers.CancelWalletWithdrawal)

			// UTXOs — UTXOOnly middleware rejects non-UTXO chains
			r.Prefix("/unspents").Middleware(middleware.UTXOOnly).Group(func(ur route.Router) {
				ur.Get("", controllers.ListUnspentOutputs)
			})
		})
	})

	// API v1 group — HMAC-authenticated (legacy/external API)
	facades.Route().Prefix("/v1").Middleware(middleware.HMACAuth).Group(func(router route.Router) {
		// Chains
		router.Get("/chains", controllers.ListChains)

		// Wallets
		router.Post("/wallets", controllers.CreateWallet)
		router.Get("/wallets", controllers.ListWallets)
		router.Get("/wallets/{walletId}", controllers.GetWallet)

		// Addresses
		router.Post("/wallets/{walletId}/addresses", controllers.GenerateAddress)
		router.Get("/wallets/{walletId}/addresses", controllers.ListWalletAddresses)
		router.Get("/addresses/{address}", controllers.LookupAddress)
		router.Get("/users/{external_id}/addresses", controllers.ListUserAddresses)

		// Transactions (flat — deprecated, prefer /wallets/{walletId}/transactions)
		router.Get("/transactions", controllers.ListTransactions)
		router.Get("/transactions/{id}", controllers.GetTransaction)
		router.Get("/users/{external_id}/transactions", controllers.ListUserTransactions)

		// Webhooks (flat — deprecated, prefer /wallets/{walletId}/webhooks)
		router.Post("/webhooks", controllers.CreateWebhook)
		router.Get("/webhooks", controllers.ListWebhooks)
	})
}
