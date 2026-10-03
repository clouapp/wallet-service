package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	dashaccounts "github.com/macrowallets/waas/app/http/controllers/dashboard/accounts"
	dashaddresses "github.com/macrowallets/waas/app/http/controllers/dashboard/addresses"
	dashauth "github.com/macrowallets/waas/app/http/controllers/dashboard/auth"
	dashchains "github.com/macrowallets/waas/app/http/controllers/dashboard/chains"
	dashcurrencies "github.com/macrowallets/waas/app/http/controllers/dashboard/currencies"
	dashpreferences "github.com/macrowallets/waas/app/http/controllers/dashboard/preferences"
	dashsweep "github.com/macrowallets/waas/app/http/controllers/dashboard/sweep"
	dashusers "github.com/macrowallets/waas/app/http/controllers/dashboard/users"
	dashwallets "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets"
	dashwithdrawals "github.com/macrowallets/waas/app/http/controllers/dashboard/withdrawals"
	"github.com/macrowallets/waas/app/http/middleware"
)

// RegisterAdminRoutes registers dashboard session-auth routes under /v1.
func RegisterAdminRoutes() {
	noCache := middleware.CacheControl(0)

	facades.Route().Prefix("/v1/auth").Middleware(noCache).Group(func(router route.Router) {
		router.Post("/register", dashauth.Register)
		router.Post("/login", dashauth.Login)
		router.Post("/2fa/verify", dashauth.VerifyTwoFactor)
		router.Post("/refresh", dashauth.RefreshToken)
		router.Post("/recover", dashauth.ForgotPassword)
		router.Post("/recover/confirm", dashauth.ResetPassword)
	})
	facades.Route().Prefix("/v1/auth").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("/logout", dashauth.Logout)
	})

	facades.Route().Prefix("/v1/users").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/me", dashusers.GetMe)
		router.Patch("/me", dashusers.UpdateMe)
		router.Post("/me/password", dashusers.ChangePassword)
		router.Get("/me/accounts", dashusers.ListMyAccounts)
		router.Patch("/me/default-account", dashusers.UpdateDefaultAccount)
		router.Post("/me/totp/setup", dashusers.SetupTOTP)
		router.Post("/me/totp/verify", dashusers.ConfirmTOTP)
		router.Delete("/me/totp", dashusers.DisableTOTP)
	})

	facades.Route().Prefix("/v1/accounts").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("", dashaccounts.CreateAccount)
		router.Prefix("/{accountId}").Middleware(middleware.AccountContext()).Group(func(r route.Router) {
			r.Get("", dashaccounts.GetAccount)
			r.Patch("", dashaccounts.UpdateAccount)
			r.Post("/archive", dashaccounts.ArchiveAccount)
			r.Post("/freeze", dashaccounts.FreezeAccount)

			r.Get("/users", dashaccounts.ListAccountUsers)
			r.Post("/users", dashaccounts.AddAccountUser)
			r.Delete("/users/{userId}", dashaccounts.RemoveAccountUser)

			r.Get("/tokens", dashaccounts.ListAccountTokens)
			r.Post("/tokens", dashaccounts.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", dashaccounts.RevokeAccountToken)
		})
	})

	facades.Route().Prefix("/v1/chains").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", dashchains.ListChains)
		router.Get("/{chainId}", dashchains.GetChain)
		router.Get("/{chainId}/tokens", dashchains.ListChainTokens)
		router.Get("/{chainId}/resources", dashchains.ListChainResources)
	})

	facades.Route().Prefix("/v1/currencies").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", dashcurrencies.ListCurrencies)
		router.Get("/{code}", dashcurrencies.GetCurrency)
	})

	facades.Route().Prefix("/v1/me").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/preferences", dashpreferences.GetPreferences)
		router.Put("/preferences", dashpreferences.UpdatePreferences)
	})

	facades.Route().Prefix("/v1/convert").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", dashcurrencies.ConvertCurrency)
	})

	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", dashwallets.ListWallets)
		router.Post("", dashwallets.CreateWalletAdmin)
		router.Get("/{walletId}", dashwallets.GetWallet)
		router.Prefix("/{walletId}").Middleware(middleware.WalletContext()).Group(func(r route.Router) {
			r.Post("/activate", dashwallets.ActivateWallet)

			r.Get("/addresses", dashaddresses.ListWalletAddresses)
			r.Post("/addresses", dashaddresses.GenerateAddress)
			r.Patch("/addresses/{addressId}", dashaddresses.UpdateAddress)

			r.Get("/users", dashwallets.ListWalletUsers)
			r.Post("/users", dashwallets.AddWalletUser)
			r.Delete("/users/{userId}", dashwallets.RemoveWalletUser)

			r.Get("/whitelist", dashwallets.ListWhitelistEntries)
			r.Post("/whitelist", dashwallets.AddWhitelistEntry)
			r.Delete("/whitelist/{entryId}", dashwallets.DeleteWhitelistEntry)

			r.Get("/webhooks", dashwallets.ListWalletWebhooks)
			r.Post("/webhooks", dashwallets.CreateWalletWebhook)
			r.Delete("/webhooks/{webhookId}", dashwallets.DeleteWalletWebhook)

			r.Get("/settings", dashwallets.GetWalletSettings)
			r.Patch("/settings", dashwallets.UpdateWalletSettings)
			r.Post("/freeze", dashwallets.FreezeWallet)

			r.Get("/balances", dashwallets.ListWalletBalances)

			r.Get("/transactions", dashwallets.ListWalletTransactions)
			r.Get("/transactions/{txId}", dashwallets.GetWalletTransaction)

			r.Get("/withdrawals", dashwithdrawals.ListWalletWithdrawals)
			r.Post("/withdrawals", dashwithdrawals.CreateWalletWithdrawal)
			r.Post("/withdrawals/estimate", dashwithdrawals.EstimateWithdrawalFee)
			r.Get("/withdrawals/{withdrawalId}", dashwithdrawals.GetWalletWithdrawal)
			r.Post("/withdrawals/{withdrawalId}/cancel", dashwithdrawals.CancelWalletWithdrawal)

			r.Post("/consolidate", dashsweep.ConsolidateWallet)
			r.Get("/gas-status", dashsweep.GetGasStatus)
			r.Post("/gas-check", dashsweep.ForceGasCheck)
			r.Post("/withdraw/preview", dashsweep.PreviewWithdraw)

			r.Prefix("/unspents").Middleware(middleware.UTXOOnly()).Group(func(ur route.Router) {
				ur.Get("", dashwallets.ListUnspentOutputs)
			})
		})
	})
}
