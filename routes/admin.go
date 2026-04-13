package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/http/controllers"
	"github.com/macrowallets/waas/app/http/middleware"
)

// RegisterAdminRoutes registers dashboard session-auth routes under /v1.
func RegisterAdminRoutes() {
	noCache := middleware.CacheControl(0)

	facades.Route().Prefix("/v1/auth").Middleware(noCache).Group(func(router route.Router) {
		router.Post("/register", controllers.Register)
		router.Post("/login", controllers.Login)
		router.Post("/2fa/verify", controllers.VerifyTwoFactor)
		router.Post("/refresh", controllers.RefreshToken)
		router.Post("/recover", controllers.ForgotPassword)
		router.Post("/recover/confirm", controllers.ResetPassword)
	})
	facades.Route().Prefix("/v1/auth").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("/logout", controllers.Logout)
	})

	facades.Route().Prefix("/v1/users").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/me", controllers.GetMe)
		router.Patch("/me", controllers.UpdateMe)
		router.Post("/me/password", controllers.ChangePassword)
		router.Get("/me/accounts", controllers.ListMyAccounts)
		router.Patch("/me/default-account", controllers.UpdateDefaultAccount)
		router.Post("/me/totp/setup", controllers.SetupTOTP)
		router.Post("/me/totp/verify", controllers.ConfirmTOTP)
		router.Delete("/me/totp", controllers.DisableTOTP)
	})

	facades.Route().Prefix("/v1/accounts").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("", controllers.CreateAccount)
		router.Prefix("/{accountId}").Middleware(middleware.AccountContext()).Group(func(r route.Router) {
			r.Get("", controllers.GetAccount)
			r.Patch("", controllers.UpdateAccount)
			r.Post("/archive", controllers.ArchiveAccount)
			r.Post("/freeze", controllers.FreezeAccount)

			r.Get("/users", controllers.ListAccountUsers)
			r.Post("/users", controllers.AddAccountUser)
			r.Delete("/users/{userId}", controllers.RemoveAccountUser)

			r.Get("/tokens", controllers.ListAccountTokens)
			r.Post("/tokens", controllers.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", controllers.RevokeAccountToken)
		})
	})

	facades.Route().Prefix("/v1/chains").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", controllers.ListChains)
		router.Get("/{chainId}", controllers.GetChain)
		router.Get("/{chainId}/tokens", controllers.ListChainTokens)
		router.Get("/{chainId}/resources", controllers.ListChainResources)
	})

	facades.Route().Prefix("/v1/currencies").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", controllers.ListCurrencies)
		router.Get("/{code}", controllers.GetCurrency)
	})

	facades.Route().Prefix("/v1/me").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/preferences", controllers.GetPreferences)
		router.Put("/preferences", controllers.UpdatePreferences)
	})

	facades.Route().Prefix("/v1/convert").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", controllers.ConvertCurrency)
	})

	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", controllers.ListWallets)
		router.Post("", controllers.CreateWalletAdmin)
		router.Get("/{walletId}", controllers.GetWallet)
		router.Prefix("/{walletId}").Middleware(middleware.WalletContext()).Group(func(r route.Router) {
			r.Post("/activate", controllers.ActivateWallet)

			r.Get("/addresses", controllers.ListWalletAddresses)
			r.Post("/addresses", controllers.GenerateAddress)

			r.Get("/users", controllers.ListWalletUsers)
			r.Post("/users", controllers.AddWalletUser)
			r.Delete("/users/{userId}", controllers.RemoveWalletUser)

			r.Get("/whitelist", controllers.ListWhitelistEntries)
			r.Post("/whitelist", controllers.AddWhitelistEntry)
			r.Delete("/whitelist/{entryId}", controllers.DeleteWhitelistEntry)

			r.Get("/webhooks", controllers.ListWalletWebhooks)
			r.Post("/webhooks", controllers.CreateWalletWebhook)
			r.Delete("/webhooks/{webhookId}", controllers.DeleteWalletWebhook)

			r.Get("/settings", controllers.GetWalletSettings)
			r.Patch("/settings", controllers.UpdateWalletSettings)
			r.Post("/freeze", controllers.FreezeWallet)

			r.Get("/transactions", controllers.ListWalletTransactions)
			r.Get("/transactions/{txId}", controllers.GetWalletTransaction)

			r.Get("/withdrawals", controllers.ListWalletWithdrawals)
			r.Post("/withdrawals", controllers.CreateWalletWithdrawal)
			r.Post("/withdrawals/estimate", controllers.EstimateWithdrawalFee)
			r.Get("/withdrawals/{withdrawalId}", controllers.GetWalletWithdrawal)
			r.Post("/withdrawals/{withdrawalId}/cancel", controllers.CancelWalletWithdrawal)

			r.Prefix("/unspents").Middleware(middleware.UTXOOnly()).Group(func(ur route.Router) {
				ur.Get("", controllers.ListUnspentOutputs)
			})
		})
	})
}
