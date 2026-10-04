package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	dashaccounts "github.com/macrowallets/waas/app/http/controllers/dashboard/accounts"
	dashactivity "github.com/macrowallets/waas/app/http/controllers/dashboard/activity"
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
	platformchains "github.com/macrowallets/waas/app/http/controllers/platform/chains"
	platformfeatures "github.com/macrowallets/waas/app/http/controllers/platform/features"
	platformsettings "github.com/macrowallets/waas/app/http/controllers/platform/settings"
	platformusers "github.com/macrowallets/waas/app/http/controllers/platform/users"
	"github.com/macrowallets/waas/app/http/middleware"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	activitysvc "github.com/macrowallets/waas/app/services/activity"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/currencies"
	"github.com/macrowallets/waas/app/services/deposit"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/sessions"
	settingssvc "github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	usersvc "github.com/macrowallets/waas/app/services/users"
	walletsvc "github.com/macrowallets/waas/app/services/wallet"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// RegisterAdminRoutes registers dashboard session-auth routes under /v1.
func RegisterAdminRoutes() {
	noCache := middleware.CacheControl(0)
	accounts := container.MustMake[*accountsvc.Service]()
	accountHeader := middleware.AccountHeader(accounts)
	inviteCtrl := newDashboardInvitesController()
	totpEnrollment := middleware.TOTPEnrollment(
		container.MustMake[*featuressvc.Service](),
		container.MustMake[*settingssvc.Service](),
	)
	chainCtrl := newDashboardChainsController()
	currencyCtrl := newDashboardCurrenciesController()
	preferencesCtrl := newDashboardPreferencesController()
	authCtrl := newDashboardAuthController()
	usersCtrl := newDashboardUsersController()
	accountsCtrl := newDashboardAccountsController()
	accountSettingsCtrl := newDashboardAccountSettingsController()
	accountActivityCtrl := newDashboardAccountActivityController()
	accountFeaturesCtrl := newDashboardAccountFeaturesController()
	platformFeaturesCtrl := newPlatformFeaturesController()
	platformChainsCtrl := newPlatformChainsController()
	platformSettingsCtrl := newPlatformSettingsController()
	platformUsersCtrl := newPlatformUsersController()
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
	feeEstimateCtrl := newFeeEstimateController()

	facades.Route().Prefix("/v1/auth").Middleware(noCache).Group(func(router route.Router) {
		router.Post("/register", authCtrl.Register)
		router.Post("/login", authCtrl.Login)
		router.Post("/2fa/verify", authCtrl.VerifyTwoFactor)
		router.Post("/refresh", authCtrl.RefreshToken)
		router.Post("/recover", authCtrl.ForgotPassword)
		router.Post("/recover/confirm", authCtrl.ResetPassword)
		router.Get("/invites/{token}", inviteCtrl.Preview)
		router.Post("/invites/accept", inviteCtrl.Accept)
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
		router.Prefix("/{accountId}").Middleware(middleware.AccountContext(accounts), totpEnrollment).Group(func(r route.Router) {
			r.Get("", accountsCtrl.GetAccount)
			r.Patch("", accountsCtrl.UpdateAccount)
			r.Post("/archive", accountsCtrl.ArchiveAccount)
			r.Post("/freeze", accountsCtrl.FreezeAccount)

			r.Middleware(middleware.Can(middleware.PermUsersRead)).Get("/users", accountsCtrl.ListAccountUsers)
			r.Post("/users", accountsCtrl.AddAccountUser)
			r.Patch("/users/{userId}", accountsCtrl.UpdateAccountUser)
			r.Delete("/users/{userId}", accountsCtrl.RemoveAccountUser)
			r.Middleware(middleware.Can(middleware.PermUsersRead)).Get("/invites", inviteCtrl.List)
			r.Middleware(middleware.Can(middleware.PermUsersWrite)).Post("/invites", inviteCtrl.Create)

			r.Get("/tokens", accountsCtrl.ListAccountTokens)
			r.Post("/tokens", accountsCtrl.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", accountsCtrl.RevokeAccountToken)

			r.Get("/settings", accountSettingsCtrl.Show)
			r.Post("/settings/sections/{section}/cache", accountSettingsCtrl.Flush)
			r.Post("/settings/sections/{section}/reset", accountSettingsCtrl.Reset)
			r.Patch("/settings/{group}", accountSettingsCtrl.Update)

			r.Get("/activity", accountActivityCtrl.Index)

			r.Get("/features", accountFeaturesCtrl.Index)
			r.Patch("/features/{key}", accountFeaturesCtrl.Update)
		})
	})

	facades.Route().Prefix("/v1/platform").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/features", platformFeaturesCtrl.Index)
		router.Patch("/features/{key}", platformFeaturesCtrl.Update)
		router.Patch("/chains/{chainId}/rpc", platformChainsCtrl.UpdateRPC)
		router.Patch("/chains/{chainId}", platformChainsCtrl.Update)
		router.Get("/settings", platformSettingsCtrl.Index)
		router.Get("/settings/{group}", platformSettingsCtrl.Show)
		router.Put("/settings/{group}", platformSettingsCtrl.Update)
		router.Get("/activity", accountActivityCtrl.Platform)
		router.Get("/users", platformUsersCtrl.Index)
		router.Post("/users/{id}/suspend", platformUsersCtrl.Suspend)
		router.Post("/users/{id}/reactivate", platformUsersCtrl.Reactivate)
		router.Post("/users/{id}/sessions/revoke", platformUsersCtrl.RevokeSessions)
		router.Delete("/users/{id}/mfa", platformUsersCtrl.ResetMFA)
	})

	facades.Route().Prefix("/v1/chains").Middleware(middleware.SessionAuth(), accountHeader, totpEnrollment, noCache).Group(func(router route.Router) {
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

	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth(), accountHeader, totpEnrollment, noCache).Group(func(router route.Router) {
		router.Get("", walletCtrl.ListWallets)
		router.Middleware(middleware.RequireFundAction(middleware.FundCreateWallet)).Post("", walletCtrl.CreateWalletAdmin)
		router.Get("/{walletId}", walletCtrl.GetWallet)
		router.Prefix("/{walletId}").Middleware(middleware.WalletContext()).Group(func(r route.Router) {
			r.Post("/activate", walletCtrl.ActivateWallet)

			r.Get("/addresses", addressCtrl.ListWalletAddresses)
			r.Middleware(middleware.WalletCan(middleware.PermAddressesCreate)).Post("/addresses", addressCtrl.GenerateAddress)
			r.Patch("/addresses/{addressId}", addressCtrl.UpdateAddress)

			r.Get("/users", walletUsersCtrl.ListWalletUsers)
			r.Post("/users", walletUsersCtrl.AddWalletUser)
			r.Delete("/users/{userId}", walletUsersCtrl.RemoveWalletUser)

			r.Get("/whitelist", whitelistCtrl.ListWhitelistEntries)
			r.Post("/whitelist", whitelistCtrl.AddWhitelistEntry)
			r.Delete("/whitelist/{entryId}", whitelistCtrl.DeleteWhitelistEntry)

			r.Get("/webhooks", walletWebhooksCtrl.ListWalletWebhooks)
			r.Post("/webhooks", walletWebhooksCtrl.CreateWalletWebhook)
			r.Post("/webhooks/{webhookId}/test", walletWebhooksCtrl.TestWalletWebhook)
			r.Delete("/webhooks/{webhookId}", walletWebhooksCtrl.DeleteWalletWebhook)

			r.Get("/settings", walletSettingsCtrl.GetWalletSettings)
			r.Patch("/settings", walletSettingsCtrl.UpdateWalletSettings)
			r.Post("/freeze", walletSettingsCtrl.FreezeWallet)
			r.Post("/archive", walletSettingsCtrl.ArchiveWallet)

			r.Get("/balances", balancesCtrl.ListWalletBalances)

			r.Get("/transactions", walletTxCtrl.ListWalletTransactions)
			r.Get("/transactions/{txId}", walletTxCtrl.GetWalletTransaction)

			r.Get("/withdrawals", withdrawalCtrl.ListWalletWithdrawals)
			r.Middleware(middleware.RequireFundAction(middleware.FundWithdraw)).Post("/withdrawals", withdrawalCtrl.CreateWalletWithdrawal)
			r.Post("/withdrawals/estimate", withdrawalCtrl.EstimateWithdrawalFee)
			r.Get("/fee-estimate", feeEstimateCtrl.GetWalletFeeEstimate)
			r.Get("/withdrawals/{withdrawalId}", withdrawalCtrl.GetWalletWithdrawal)
			r.Post("/withdrawals/{withdrawalId}/cancel", withdrawalCtrl.CancelWalletWithdrawal)

			r.Middleware(middleware.RequireFundAction(middleware.FundSweep)).Post("/consolidate", sweepCtrl.ConsolidateWallet)
			r.Get("/gas-status", sweepCtrl.GetGasStatus)
			r.Post("/gas-check", sweepCtrl.ForceGasCheck)
			r.Post("/withdraw/preview", sweepCtrl.PreviewWithdraw)

			r.Prefix("/unspents").Middleware(middleware.UTXOOnly()).Group(func(ur route.Router) {
				ur.Get("", unspentsCtrl.ListUnspentOutputs)
			})
		})
	})

	// Dashboard withdrawal detail. Same session and account-header auth as
	// GET /v1/wallets/{walletId}/withdrawals; the id is not scoped by a wallet path.
	facades.Route().Prefix("/v1/withdrawals").Middleware(middleware.SessionAuth(), accountHeader, totpEnrollment, noCache).Group(func(router route.Router) {
		router.Get("/{withdrawalId}", withdrawalCtrl.GetDashboardWithdrawal)
	})
}

func newDashboardAuthController() *dashauth.AuthController {
	return dashauth.NewAuthController(
		container.MustMake[*usersvc.Service](),
		container.MustMake[*accountsvc.Service](),
		container.MustMake[*sessions.RefreshTokens](),
		container.MustMake[*sessions.PasswordResets](),
		container.MustMake[*authsvc.Service](),
		container.MustMake[*authsvc.TwoFactorLogin](),
		container.MustMake[*authsvc.SessionRevoker](),
	)
}

func newDashboardUsersController() *dashusers.UsersController {
	return dashusers.NewUsersController(
		container.MustMake[*usersvc.Service](),
		container.MustMake[*accountsvc.Service](),
		container.MustMake[*authsvc.Service](),
		container.MustMake[*sessions.RefreshTokens](),
		container.MustMake[*authsvc.SecondFactorVerifier](),
		container.MustMake[*authsvc.SessionRevoker](),
		container.MustMake[*featuressvc.Service](),
		container.MustMake[*settingssvc.Service](),
	)
}

// currentWalletService reads the wallet service on each call. Recovery tests
// replace that field after boot, so a singleton captured at boot would be stale.
func currentWalletService() *walletsvc.Service {
	return container.Get().WalletService
}

func newDashboardWalletsController() *dashwallets.WalletsController {
	return dashwallets.NewWalletsController(
		container.MustMake[*walletrecords.Wallets](),
		container.MustMake[*chainsvc.Service](),
		currentWalletService,
		container.MustMake[*walletrecords.Members](),
	)
}

func walletPolicyMemberships() *walletrecords.Memberships {
	return walletrecords.NewMemberships(
		container.MustMake[*walletrecords.Wallets](),
		container.MustMake[*walletrecords.Members](),
		container.MustMake[*accountsvc.Service](),
	)
}

func newDashboardWalletUsersController() *dashwallets.UsersController {
	return dashwallets.NewUsersController(
		container.MustMake[*walletrecords.Members](),
		container.MustMake[*accountsvc.Service](),
		walletPolicyMemberships(),
	)
}

func newDashboardWhitelistController() *dashwallets.WhitelistController {
	return dashwallets.NewWhitelistController(
		container.MustMake[*walletrecords.Whitelist](),
		walletPolicyMemberships(),
	)
}

func newDashboardWalletWebhooksController() *dashwallets.WebhooksController {
	return dashwallets.NewWebhooksController(
		container.MustMake[*walletrecords.Webhooks](),
		container.MustMake[*webhook.Service](),
		walletPolicyMemberships(),
	)
}

func newDashboardWalletSettingsController() *dashwallets.SettingsController {
	return dashwallets.NewSettingsController(
		container.MustMake[*walletrecords.Wallets](),
		walletPolicyMemberships(),
		container.MustMake[*chainsvc.Service](),
	)
}

func newDashboardBalancesController() *dashwallets.BalancesController {
	return dashwallets.NewBalancesController(
		container.MustMake[*walletrecords.Balances](),
		container.MustMake[*chainsvc.Service](),
	)
}

func newDashboardWalletTransactionsController() *dashwallets.TransactionsController {
	return dashwallets.NewTransactionsController(
		container.MustMake[*walletrecords.Transactions](),
	)
}

func newDashboardChainsController() *dashchains.ChainsController {
	return dashchains.NewChainsController(
		container.MustMake[*chainsvc.Service](),
	)
}

func newDashboardCurrenciesController() *dashcurrencies.CurrenciesController {
	return dashcurrencies.NewCurrenciesController(
		container.MustMake[*currencies.Service](),
		container.MustMake[*price.Service](),
	)
}

func newDashboardPreferencesController() *dashpreferences.PreferencesController {
	return dashpreferences.NewPreferencesController(
		container.MustMake[*usersvc.Service](),
		container.MustMake[*currencies.Service](),
	)
}

func newDashboardAddressesController() *dashaddresses.AddressesController {
	return dashaddresses.NewAddressesController(
		container.MustMake[*walletrecords.Addresses](),
		currentWalletService,
		container.MustMake[*deposit.Service](),
	)
}

func newDashboardWithdrawalsController() *dashwithdrawals.WithdrawalsController {
	return dashwithdrawals.NewWithdrawalsController(
		container.MustMake[*withdrawalrecords.Records](),
		container.MustMake[*chainsvc.Service](),
		container.MustMake[*usersvc.Service](),
		container.MustMake[*chainpkg.Registry](),
		container.MustMake[*withdraw.Service](),
		container.MustMake[*authsvc.Service](),
		container.MustMake[*featuressvc.Service](),
		container.MustMake[*withdrawalevents.Publisher](),
		container.MustMake[*container.SharedRedis]().Client,
		walletPolicyMemberships(),
		container.MustMake[*walletrecords.Wallets](),
		container.MustMake[*authsvc.SecondFactorVerifier](),
	)
}

func newDashboardSweepController() *dashsweep.SweepController {
	return dashsweep.NewSweepController(
		container.MustMake[*sweep.Box]().Service,
		container.MustMake[*container.SharedRedis]().Client,
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardUnspentsController() *dashwallets.UnspentsController {
	return dashwallets.NewUnspentsController(
		container.MustMake[*walletrecords.UTXOs](),
	)
}

func newDashboardAccountActivityController() *dashactivity.ActivityController {
	return dashactivity.NewActivityController(
		container.MustMake[*activitysvc.Service](),
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

func newPlatformUsersController() *platformusers.UsersController {
	return platformusers.NewUsersController(
		container.MustMake[*usersvc.Service](),
	)
}

func newPlatformSettingsController() *platformsettings.SettingsController {
	return platformsettings.NewSettingsController(
		container.MustMake[*settingssvc.Service](),
	)
}

func newPlatformChainsController() *platformchains.ChainsController {
	return platformchains.NewChainsController(
		container.MustMake[*chainsvc.Thresholds](),
		container.MustMake[*chainsvc.RPC](),
	)
}

func newPlatformFeaturesController() *platformfeatures.FeaturesController {
	return platformfeatures.NewFeaturesController(
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardAccountsController() *dashaccounts.AccountsController {
	return dashaccounts.NewAccountsController(
		container.MustMake[*accountsvc.Service](),
		container.MustMake[*authsvc.Service](),
		container.MustMake[*settingssvc.Service](),
	)
}

func newDashboardInvitesController() *dashaccounts.InvitesController {
	return dashaccounts.NewInvitesController(
		container.MustMake[*accountsvc.Service](),
		container.MustMake[*usersvc.Service](),
	)
}
