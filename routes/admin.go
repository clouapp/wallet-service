package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	dashaccounts "github.com/macrowallets/waas/app/http/controllers/dashboard/accounts"
	dashaddresses "github.com/macrowallets/waas/app/http/controllers/dashboard/addresses"
	dashauth "github.com/macrowallets/waas/app/http/controllers/dashboard/auth"
	dashchains "github.com/macrowallets/waas/app/http/controllers/dashboard/chains"
	dashcurrencies "github.com/macrowallets/waas/app/http/controllers/dashboard/currencies"
	dashfeatures "github.com/macrowallets/waas/app/http/controllers/dashboard/features"
	dashpreferences "github.com/macrowallets/waas/app/http/controllers/dashboard/preferences"
	dashsettings "github.com/macrowallets/waas/app/http/controllers/dashboard/settings"
	dashsweep "github.com/macrowallets/waas/app/http/controllers/dashboard/sweep"
	dashusers "github.com/macrowallets/waas/app/http/controllers/dashboard/users"
	dashwallets "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets"
	dashwithdrawals "github.com/macrowallets/waas/app/http/controllers/dashboard/withdrawals"
	platformfeatures "github.com/macrowallets/waas/app/http/controllers/platform/features"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/repositories"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
	walletsvc "github.com/macrowallets/waas/app/services/wallet"
)

// RegisterAdminRoutes registers dashboard session-auth routes under /v1.
func RegisterAdminRoutes() {
	noCache := middleware.CacheControl(0)
	chainCtrl := newDashboardChainsController()
	currencyCtrl := newDashboardCurrenciesController()
	preferencesCtrl := newDashboardPreferencesController()
	authCtrl := newDashboardAuthController()
	usersCtrl := newDashboardUsersController()
	accountsCtrl := newDashboardAccountsController()
	accountSettingsCtrl := newDashboardAccountSettingsController()
	accountFeaturesCtrl := newDashboardAccountFeaturesController()
	platformFeaturesCtrl := newPlatformFeaturesController()
	walletCtrl := newDashboardWalletsController()
	walletUsersCtrl := newDashboardWalletUsersController()
	whitelistCtrl := newDashboardWhitelistController()
	walletWebhooksCtrl := newDashboardWalletWebhooksController()
	walletSettingsCtrl := newDashboardWalletSettingsController()
	balancesCtrl := newDashboardBalancesController()
	walletTxCtrl := newDashboardWalletTransactionsController()
	unspentsCtrl := newDashboardUnspentsController()
	addressCtrl := newDashboardAddressesController()
	withdrawalCtrl := newDashboardWithdrawalsController()
	sweepCtrl := newDashboardSweepController()

	facades.Route().Prefix("/v1/auth").Middleware(noCache).Group(func(router route.Router) {
		router.Post("/register", authCtrl.Register)
		router.Post("/login", authCtrl.Login)
		router.Post("/2fa/verify", authCtrl.VerifyTwoFactor)
		router.Post("/refresh", authCtrl.RefreshToken)
		router.Post("/recover", authCtrl.ForgotPassword)
		router.Post("/recover/confirm", authCtrl.ResetPassword)
	})
	facades.Route().Prefix("/v1/auth").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("/logout", authCtrl.Logout)
	})

	facades.Route().Prefix("/v1/users").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/me", usersCtrl.GetMe)
		router.Patch("/me", usersCtrl.UpdateMe)
		router.Post("/me/password", usersCtrl.ChangePassword)
		router.Get("/me/accounts", usersCtrl.ListMyAccounts)
		router.Patch("/me/default-account", usersCtrl.UpdateDefaultAccount)
		router.Post("/me/totp/setup", usersCtrl.SetupTOTP)
		router.Post("/me/totp/verify", usersCtrl.ConfirmTOTP)
		router.Delete("/me/totp", usersCtrl.DisableTOTP)
	})

	facades.Route().Prefix("/v1/accounts").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Post("", accountsCtrl.CreateAccount)
		router.Prefix("/{accountId}").Middleware(middleware.AccountContext()).Group(func(r route.Router) {
			r.Get("", accountsCtrl.GetAccount)
			r.Patch("", accountsCtrl.UpdateAccount)
			r.Post("/archive", accountsCtrl.ArchiveAccount)
			r.Post("/freeze", accountsCtrl.FreezeAccount)

			r.Get("/users", accountsCtrl.ListAccountUsers)
			r.Post("/users", accountsCtrl.AddAccountUser)
			r.Patch("/users/{userId}", accountsCtrl.UpdateAccountUser)
			r.Delete("/users/{userId}", accountsCtrl.RemoveAccountUser)

			r.Get("/tokens", accountsCtrl.ListAccountTokens)
			r.Post("/tokens", accountsCtrl.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", accountsCtrl.RevokeAccountToken)

			r.Get("/settings", accountSettingsCtrl.Show)
			r.Patch("/settings/{group}", accountSettingsCtrl.Update)

			r.Get("/features", accountFeaturesCtrl.Index)
			r.Patch("/features/{key}", accountFeaturesCtrl.Update)
		})
	})

	facades.Route().Prefix("/v1/platform").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/features", platformFeaturesCtrl.Index)
		router.Patch("/features/{key}", platformFeaturesCtrl.Update)
	})

	facades.Route().Prefix("/v1/chains").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", chainCtrl.ListChains)
		router.Get("/{chainId}", chainCtrl.GetChain)
		router.Get("/{chainId}/tokens", chainCtrl.ListChainTokens)
		router.Get("/{chainId}/resources", chainCtrl.ListChainResources)
	})

	facades.Route().Prefix("/v1/currencies").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", currencyCtrl.ListCurrencies)
		router.Get("/{code}", currencyCtrl.GetCurrency)
	})

	facades.Route().Prefix("/v1/me").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/preferences", preferencesCtrl.GetPreferences)
		router.Put("/preferences", preferencesCtrl.UpdatePreferences)
	})

	facades.Route().Prefix("/v1/convert").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", currencyCtrl.ConvertCurrency)
	})

	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth(), middleware.AccountHeader(), noCache).Group(func(router route.Router) {
		router.Get("", walletCtrl.ListWallets)
		router.Post("", walletCtrl.CreateWalletAdmin)
		router.Get("/{walletId}", walletCtrl.GetWallet)
		router.Prefix("/{walletId}").Middleware(middleware.WalletContext()).Group(func(r route.Router) {
			r.Post("/activate", walletCtrl.ActivateWallet)

			r.Get("/addresses", addressCtrl.ListWalletAddresses)
			r.Post("/addresses", addressCtrl.GenerateAddress)
			r.Patch("/addresses/{addressId}", addressCtrl.UpdateAddress)

			r.Get("/users", walletUsersCtrl.ListWalletUsers)
			r.Post("/users", walletUsersCtrl.AddWalletUser)
			r.Delete("/users/{userId}", walletUsersCtrl.RemoveWalletUser)

			r.Get("/whitelist", whitelistCtrl.ListWhitelistEntries)
			r.Post("/whitelist", whitelistCtrl.AddWhitelistEntry)
			r.Delete("/whitelist/{entryId}", whitelistCtrl.DeleteWhitelistEntry)

			r.Get("/webhooks", walletWebhooksCtrl.ListWalletWebhooks)
			r.Post("/webhooks", walletWebhooksCtrl.CreateWalletWebhook)
			r.Delete("/webhooks/{webhookId}", walletWebhooksCtrl.DeleteWalletWebhook)

			r.Get("/settings", walletSettingsCtrl.GetWalletSettings)
			r.Patch("/settings", walletSettingsCtrl.UpdateWalletSettings)
			r.Post("/freeze", walletSettingsCtrl.FreezeWallet)

			r.Get("/balances", balancesCtrl.ListWalletBalances)

			r.Get("/transactions", walletTxCtrl.ListWalletTransactions)
			r.Get("/transactions/{txId}", walletTxCtrl.GetWalletTransaction)

			r.Get("/withdrawals", withdrawalCtrl.ListWalletWithdrawals)
			r.Post("/withdrawals", withdrawalCtrl.CreateWalletWithdrawal)
			r.Post("/withdrawals/estimate", withdrawalCtrl.EstimateWithdrawalFee)
			r.Get("/withdrawals/{withdrawalId}", withdrawalCtrl.GetWalletWithdrawal)
			r.Post("/withdrawals/{withdrawalId}/cancel", withdrawalCtrl.CancelWalletWithdrawal)

			r.Post("/consolidate", sweepCtrl.ConsolidateWallet)
			r.Get("/gas-status", sweepCtrl.GetGasStatus)
			r.Post("/gas-check", sweepCtrl.ForceGasCheck)
			r.Post("/withdraw/preview", sweepCtrl.PreviewWithdraw)

			r.Prefix("/unspents").Middleware(middleware.UTXOOnly()).Group(func(ur route.Router) {
				ur.Get("", unspentsCtrl.ListUnspentOutputs)
			})
		})
	})
}

func newDashboardAuthController() *dashauth.AuthController {
	return dashauth.NewAuthController(
		container.MustMake[*repositories.UserRepository](),
		container.MustMake[*repositories.AccountRepository](),
		container.MustMake[*repositories.AccountUserRepository](),
		container.MustMake[*repositories.RefreshTokenRepository](),
		container.MustMake[*repositories.TotpRecoveryCodeRepository](),
		container.MustMake[*repositories.PasswordResetTokenRepository](),
		container.MustMake[*authsvc.Service](),
	)
}

func newDashboardUsersController() *dashusers.UsersController {
	return dashusers.NewUsersController(
		container.MustMake[*repositories.UserRepository](),
		container.MustMake[*repositories.AccountRepository](),
		container.MustMake[*repositories.AccountUserRepository](),
		container.MustMake[*repositories.TotpRecoveryCodeRepository](),
		container.MustMake[*authsvc.Service](),
	)
}

func currentWalletService() *walletsvc.Service {
	return container.Get().WalletService
}

func newDashboardWalletsController() *dashwallets.WalletsController {
	return dashwallets.NewWalletsController(
		container.MustMake[*repositories.WalletRepository](),
		container.MustMake[*repositories.ChainRepository](),
		currentWalletService,
	)
}

func newDashboardWalletUsersController() *dashwallets.UsersController {
	return dashwallets.NewUsersController(
		container.MustMake[*repositories.WalletUserRepository](),
	)
}

func newDashboardWhitelistController() *dashwallets.WhitelistController {
	return dashwallets.NewWhitelistController(
		container.MustMake[*repositories.WhitelistEntryRepository](),
	)
}

func newDashboardWalletWebhooksController() *dashwallets.WebhooksController {
	return dashwallets.NewWebhooksController(
		container.MustMake[*repositories.WebhookConfigRepository](),
	)
}

func newDashboardWalletSettingsController() *dashwallets.SettingsController {
	return dashwallets.NewSettingsController(
		container.MustMake[*repositories.WalletRepository](),
	)
}

func newDashboardBalancesController() *dashwallets.BalancesController {
	return dashwallets.NewBalancesController(
		container.MustMake[*repositories.WalletAssetBalanceRepository](),
		container.MustMake[*repositories.TokenRepository](),
	)
}

func newDashboardWalletTransactionsController() *dashwallets.TransactionsController {
	return dashwallets.NewTransactionsController(
		container.MustMake[*repositories.TransactionRepository](),
	)
}

func newDashboardChainsController() *dashchains.ChainsController {
	return dashchains.NewChainsController(
		container.MustMake[*repositories.ChainRepository](),
		container.MustMake[*repositories.TokenRepository](),
		container.MustMake[*repositories.ChainResourceRepository](),
	)
}

func newDashboardCurrenciesController() *dashcurrencies.CurrenciesController {
	return dashcurrencies.NewCurrenciesController(
		container.MustMake[*repositories.CurrencyRepository](),
		container.Get().PriceService,
	)
}

func newDashboardPreferencesController() *dashpreferences.PreferencesController {
	return dashpreferences.NewPreferencesController(
		container.MustMake[*repositories.UserRepository](),
		container.MustMake[*repositories.CurrencyRepository](),
	)
}

func newDashboardAddressesController() *dashaddresses.AddressesController {
	return dashaddresses.NewAddressesController(
		container.MustMake[*repositories.AddressRepository](),
		currentWalletService,
		container.Get().DepositService,
	)
}

func newDashboardWithdrawalsController() *dashwithdrawals.WithdrawalsController {
	return dashwithdrawals.NewWithdrawalsController(
		container.MustMake[*repositories.WithdrawalRepository](),
		container.MustMake[*repositories.ChainRepository](),
		container.MustMake[*repositories.UserRepository](),
		container.Get().Registry,
		container.Get().WithdrawalService,
		container.MustMake[*authsvc.Service](),
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardSweepController() *dashsweep.SweepController {
	return dashsweep.NewSweepController(
		container.Get().SweepService,
		container.Get().Redis,
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardUnspentsController() *dashwallets.UnspentsController {
	return dashwallets.NewUnspentsController(
		container.MustMake[*repositories.WalletUTXORepository](),
	)
}

func newDashboardAccountSettingsController() *dashsettings.SettingsController {
	return dashsettings.NewSettingsController(
		container.MustMake[*settingssvc.Service](),
	)
}

func newDashboardAccountFeaturesController() *dashfeatures.FeaturesController {
	return dashfeatures.NewFeaturesController(
		container.MustMake[*featuressvc.Service](),
	)
}

func newPlatformFeaturesController() *platformfeatures.FeaturesController {
	return platformfeatures.NewFeaturesController(
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardAccountsController() *dashaccounts.AccountsController {
	return dashaccounts.NewAccountsController(
		container.MustMake[*repositories.AccountRepository](),
		container.MustMake[*repositories.AccountUserRepository](),
		container.MustMake[*repositories.UserRepository](),
		container.MustMake[*repositories.AccessTokenRepository](),
		container.MustMake[*accountsvc.Service](),
		container.MustMake[*authsvc.Service](),
	)
}
