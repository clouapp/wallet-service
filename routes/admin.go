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
	dashroles "github.com/macrowallets/waas/app/http/controllers/dashboard/roles"
	dashsettings "github.com/macrowallets/waas/app/http/controllers/dashboard/settings"
	dashsweep "github.com/macrowallets/waas/app/http/controllers/dashboard/sweep"
	dashusers "github.com/macrowallets/waas/app/http/controllers/dashboard/users"
	dashwallets "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets"
	dashwithdrawals "github.com/macrowallets/waas/app/http/controllers/dashboard/withdrawals"
	platformaccounts "github.com/macrowallets/waas/app/http/controllers/platform/accounts"
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
	"github.com/macrowallets/waas/app/services/credentialmail"
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
	accountRolesCtrl := newDashboardAccountRolesController()
	platformFeaturesCtrl := newPlatformFeaturesController()
	platformAccountsCtrl := newPlatformAccountsController()
	platformAccountListCtrl := newPlatformAccountListController()
	platformAccountUsersCtrl := newPlatformAccountUsersController()
	platformAccountOwnersCtrl := newPlatformAccountOwnersController()
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
			r.Middleware(middleware.Can(middleware.PermAccountWrite)).Patch("", accountsCtrl.UpdateAccount)
			r.Middleware(middleware.Can(middleware.PermAccountLifecycle)).Post("/archive", accountsCtrl.ArchiveAccount)
			r.Post("/freeze", accountsCtrl.FreezeAccount)

			r.Middleware(middleware.Can(middleware.PermUsersRead)).Get("/users", accountsCtrl.ListAccountUsers)
			r.Post("/users", accountsCtrl.AddAccountUser)
			r.Patch("/users/{userId}", accountsCtrl.UpdateAccountUser)
			r.Delete("/users/{userId}", accountsCtrl.RemoveAccountUser)
			r.Middleware(middleware.Can(middleware.PermUsersRead)).Get("/invites", inviteCtrl.List)
			r.Middleware(middleware.Can(middleware.PermUsersWrite)).Post("/invites", inviteCtrl.Create)
			r.Middleware(middleware.Can(middleware.PermUsersWrite)).Post("/invites/{id}/resend", inviteCtrl.Resend)
			r.Middleware(middleware.Can(middleware.PermUsersWrite)).Delete("/invites/{id}", inviteCtrl.Delete)

			// S3.4.2: GET /v1/accounts/{accountId}/roles roles.read.
			// Effective grants are the code catalog. There is no
			// account_role_permissions row and no write on this path.
			r.Middleware(middleware.Can(middleware.PermRolesRead)).Get("/roles", accountRolesCtrl.Index)
			// S3.4.2: GET /v1/accounts/{accountId}/permissions roles.read.
			// The catalog is the same code. There is no permissions table
			// and no write on this path.
			r.Middleware(middleware.Can(middleware.PermRolesRead)).Get("/permissions", accountRolesCtrl.Permissions)

			r.Get("/tokens", accountsCtrl.ListAccountTokens)
			r.Post("/tokens", accountsCtrl.CreateAccountToken)
			r.Delete("/tokens/{tokenId}", accountsCtrl.RevokeAccountToken)

			// S1.4.7: GET /v1/accounts/{accountId}/settings settings.read.
			r.Get("/settings", accountSettingsCtrl.Show)
			r.Post("/settings/sections/{section}/cache", accountSettingsCtrl.Flush)
			r.Post("/settings/sections/{section}/reset", accountSettingsCtrl.Reset)
			// S1.4.7: GET /v1/accounts/{accountId}/settings/{group} settings.read (platform-managed groups readable).
			r.Get("/settings/{group}", accountSettingsCtrl.ShowGroup)
			r.Patch("/settings/{group}", accountSettingsCtrl.Update)
			// S1.4.7: PUT /v1/accounts/{accountId}/settings/{group} settings.write. Same handler and body rules as PATCH.
			r.Put("/settings/{group}", accountSettingsCtrl.Update)

			r.Get("/activity", accountActivityCtrl.Index)
			// S3.4.2: GET /v1/accounts/{accountId}/activity/{id} activity.read.
			// The body is one element of the list. There is no write on this path.
			r.Get("/activity/{id}", accountActivityCtrl.Show)

			// S2.4: no account-side write. Reads stay on this route.
			// Platform PUT /v1/platform/features/account/{id} stores the flag.
			r.Get("/features", accountFeaturesCtrl.Index)
		})
	})

	facades.Route().Prefix("/v1/platform").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/features", platformFeaturesCtrl.Index)
		router.Patch("/features/{key}", platformFeaturesCtrl.Update)
		// S2.4: GET /v1/platform/features/{scope}/{id} features.view.
		// Scope account is the only target this catalog stores. global is refused.
		// user and chain are not scopes on this branch, so they are 404 as well.
		// A platform_admins row is the gate. There is no features.view permission row.
		router.Get("/features/{scope}/{id}", platformFeaturesCtrl.ShowScope)
		// S2.4: PUT /v1/platform/features/{scope}/{id}[/{feature}]
		// FeaturePolicy: features.update (any) | features.account.update (account scope).
		// Neither name is a permission row. A platform_admins row is the gate
		// and stands in for both. The pair is not a second gate.
		// Scope account is the only target this catalog stores. global is refused.
		// user and chain are 404 before the admin check.
		router.Put("/features/{scope}/{id}/{feature}", platformFeaturesCtrl.UpdateScopeFeature)
		router.Put("/features/{scope}/{id}", platformFeaturesCtrl.UpdateScope)
		// S1.4.7: chains.view and chains.update. A platform_admins row is the gate.
		router.Patch("/chains/{chainId}/rpc", platformChainsCtrl.UpdateRPC)
		router.Patch("/chains/{chainId}", platformChainsCtrl.Update)
		// Declared before {group} so the literal path mail/test is not a group name.
		// S1.4.6: POST /v1/platform/settings/mail/test settings.update + mail.update (declared before {group}).
		router.Post("/settings/mail/test", platformSettingsCtrl.TestMail)
		router.Get("/settings", platformSettingsCtrl.Index)
		router.Get("/settings/{group}", platformSettingsCtrl.Show)
		// S3.4.1: GET /v1/platform/accounts accounts.view.
		// A platform_admins row is the gate. The plan does not name fields,
		// pagination, or sort, so this list matches GET /v1/platform/users.
		router.Get("/accounts", platformAccountListCtrl.Index)
		// S3.4.1: POST /v1/platform/accounts/{id}/freeze|unfreeze|archive accounts.lifecycle.
		// A platform_admins row is the gate. These posts are not behind AccountContext,
		// so a frozen or archived account can still be changed.
		router.Post("/accounts/{accountId}/freeze", platformAccountsCtrl.Freeze)
		router.Post("/accounts/{accountId}/unfreeze", platformAccountsCtrl.Unfreeze)
		router.Post("/accounts/{accountId}/archive", platformAccountsCtrl.Archive)
		// S3.4.1: GET /v1/platform/accounts/{id}/users.
		// A platform_admins row is the gate. The plan does not name fields,
		// pagination, or sort, so the page matches GET /v1/platform/users and
		// each row matches the account member list.
		router.Get("/accounts/{accountId}/users", platformAccountUsersCtrl.Index)
		// S3.4.1: POST /v1/platform/accounts/{id}/owners (attach owner — recovery) accounts.owners.
		// A platform_admins row is the gate. The body is email, matching account member add.
		// The route is not behind AccountContext, so a frozen account can still be recovered.
		router.Post("/accounts/{accountId}/owners", platformAccountOwnersCtrl.Attach)
		// S1.4.6: GET /v1/platform/accounts/{accountId}/settings/{group} settings.view (platform-managed account groups).
		router.Get("/accounts/{accountId}/settings/{group}", platformSettingsCtrl.ShowAccount)
		// S1.4.6: PUT /v1/platform/accounts/{accountId}/settings/{group} settings.update + sweep.update for account_sweep_limits.
		router.Put("/accounts/{accountId}/settings/{group}", platformSettingsCtrl.UpdateAccount)
		router.Put("/settings/{group}", platformSettingsCtrl.Update)
		router.Post("/settings/sections/{section}/cache", platformSettingsCtrl.Flush)
		router.Post("/settings/sections/{section}/reset", platformSettingsCtrl.Reset)
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
			r.Middleware(middleware.Can(middleware.PermAddressesCreate)).Post("/addresses", addressCtrl.GenerateAddress)
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
	return dashauth.NewAuthController(dashauth.AuthControllerDeps{
		Users:          container.MustMake[*usersvc.Service](),
		Accounts:       container.MustMake[*accountsvc.Service](),
		RefreshTokens:  container.MustMake[*sessions.RefreshTokens](),
		PasswordResets: container.MustMake[*sessions.PasswordResets](),
		Passwords:      container.MustMake[*authsvc.Service](),
		TwoFactor:      container.MustMake[*authsvc.TwoFactorLogin](),
		Revoker:        container.MustMake[*authsvc.SessionRevoker](),
		CredentialMail: container.MustMake[*credentialmail.Service](),
	})
}

func newDashboardUsersController() *dashusers.UsersController {
	return dashusers.NewUsersController(dashusers.UsersControllerDeps{
		Users:        container.MustMake[*usersvc.Service](),
		Accounts:     container.MustMake[*accountsvc.Service](),
		Passwords:    container.MustMake[*authsvc.Service](),
		Refresh:      container.MustMake[*sessions.RefreshTokens](),
		SecondFactor: container.MustMake[*authsvc.SecondFactorVerifier](),
		Revoker:      container.MustMake[*authsvc.SessionRevoker](),
		Features:     container.MustMake[*featuressvc.Service](),
		Limits:       container.MustMake[*settingssvc.Service](),
	})
}

// currentWalletService reads the wallet service on each call. Recovery tests
// replace that field after boot, so a singleton captured at boot would be stale.
func currentWalletService() *walletsvc.Service {
	return container.Get().WalletService
}

func newDashboardWalletsController() *dashwallets.WalletsController {
	return dashwallets.NewWalletsController(dashwallets.WalletsControllerDeps{
		Wallets:       container.MustMake[*walletrecords.Wallets](),
		Members:       container.MustMake[*walletrecords.Members](),
		Chains:        container.MustMake[*chainsvc.Service](),
		WalletService: currentWalletService,
	})
}

func walletPolicyMemberships() *walletrecords.Memberships {
	return walletrecords.NewMemberships(walletrecords.MembershipsDeps{
		Wallets:  container.MustMake[*walletrecords.Wallets](),
		Members:  container.MustMake[*walletrecords.Members](),
		Accounts: container.MustMake[*accountsvc.Service](),
	})
}

func newDashboardWalletUsersController() *dashwallets.UsersController {
	return dashwallets.NewUsersController(dashwallets.WalletUsersControllerDeps{
		Members:     container.MustMake[*walletrecords.Members](),
		Accounts:    container.MustMake[*accountsvc.Service](),
		Memberships: walletPolicyMemberships(),
	})
}

func newDashboardWhitelistController() *dashwallets.WhitelistController {
	return dashwallets.NewWhitelistController(dashwallets.WhitelistControllerDeps{
		Entries:     container.MustMake[*walletrecords.Whitelist](),
		Memberships: walletPolicyMemberships(),
	})
}

func newDashboardWalletWebhooksController() *dashwallets.WebhooksController {
	return dashwallets.NewWebhooksController(dashwallets.WebhooksControllerDeps{
		Configs:     container.MustMake[*walletrecords.Webhooks](),
		Delivery:    container.MustMake[*webhook.Service](),
		Memberships: walletPolicyMemberships(),
	})
}

func newDashboardWalletSettingsController() *dashwallets.SettingsController {
	return dashwallets.NewSettingsController(dashwallets.WalletSettingsControllerDeps{
		Wallets:     container.MustMake[*walletrecords.Wallets](),
		Memberships: walletPolicyMemberships(),
		Chains:      container.MustMake[*chainsvc.Service](),
	})
}

func newDashboardBalancesController() *dashwallets.BalancesController {
	return dashwallets.NewBalancesController(dashwallets.BalancesControllerDeps{
		Balances: container.MustMake[*walletrecords.Balances](),
		Tokens:   container.MustMake[*chainsvc.Service](),
	})
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
	return dashcurrencies.NewCurrenciesController(dashcurrencies.CurrenciesControllerDeps{
		Currencies: container.MustMake[*currencies.Service](),
		Prices:     container.MustMake[*price.Service](),
	})
}

func newDashboardPreferencesController() *dashpreferences.PreferencesController {
	return dashpreferences.NewPreferencesController(dashpreferences.PreferencesControllerDeps{
		Users:      container.MustMake[*usersvc.Service](),
		Currencies: container.MustMake[*currencies.Service](),
	})
}

func newDashboardAddressesController() *dashaddresses.AddressesController {
	return dashaddresses.NewAddressesController(dashaddresses.AddressesControllerDeps{
		Addresses:     container.MustMake[*walletrecords.Addresses](),
		WalletService: currentWalletService,
		Deposits:      container.MustMake[*deposit.Service](),
	})
}

func newDashboardWithdrawalsController() *dashwithdrawals.WithdrawalsController {
	return dashwithdrawals.NewWithdrawalsController(dashwithdrawals.WithdrawalsControllerDeps{
		Withdrawals:       container.MustMake[*withdrawalrecords.Records](),
		Chains:            container.MustMake[*chainsvc.Service](),
		Users:             container.MustMake[*usersvc.Service](),
		Registry:          container.MustMake[*chainpkg.Registry](),
		WithdrawalService: container.MustMake[*withdraw.Service](),
		Passwords:         container.MustMake[*authsvc.Service](),
		Flags:             container.MustMake[*featuressvc.Service](),
		Events:            container.MustMake[*withdrawalevents.Publisher](),
		Redis:             container.MustMake[*container.SharedRedis]().Client,
		Memberships:       walletPolicyMemberships(),
		Wallets:           container.MustMake[*walletrecords.Wallets](),
		SecondFactor:      container.MustMake[*authsvc.SecondFactorVerifier](),
	})
}

func newDashboardSweepController() *dashsweep.SweepController {
	return dashsweep.NewSweepController(dashsweep.SweepControllerDeps{
		Sweeps: container.MustMake[*sweep.Box]().Service,
		Redis:  container.MustMake[*container.SharedRedis]().Client,
		Flags:  container.MustMake[*featuressvc.Service](),
	})
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

func newDashboardAccountRolesController() *dashroles.Controller {
	return dashroles.NewController()
}

func newDashboardAccountFeaturesController() *dashfeatures.FeaturesController {
	return dashfeatures.NewFeaturesController(
		container.MustMake[*featuressvc.Service](),
	)
}

func newPlatformAccountsController() *platformaccounts.LifecycleController {
	return platformaccounts.NewLifecycleController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformAccountUsersController() *platformaccounts.UsersController {
	return platformaccounts.NewUsersController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformAccountOwnersController() *platformaccounts.OwnersController {
	return platformaccounts.NewOwnersController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformAccountListController() *platformaccounts.ListController {
	return platformaccounts.NewListController(
		container.MustMake[*accountsvc.Service](),
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
	return platformchains.NewChainsController(platformchains.ChainsControllerDeps{
		Thresholds: container.MustMake[*chainsvc.Thresholds](),
		RPC:        container.MustMake[*chainsvc.RPC](),
	})
}

func newPlatformFeaturesController() *platformfeatures.FeaturesController {
	return platformfeatures.NewFeaturesController(platformfeatures.FeaturesControllerDeps{
		Features: container.MustMake[*featuressvc.Service](),
		Accounts: container.MustMake[*accountsvc.Service](),
	})
}

func newDashboardAccountsController() *dashaccounts.AccountsController {
	return dashaccounts.NewAccountsController(dashaccounts.AccountsControllerDeps{
		AccountService: container.MustMake[*accountsvc.Service](),
		Passwords:      container.MustMake[*authsvc.Service](),
		Limits:         container.MustMake[*settingssvc.Service](),
		Features:       container.MustMake[*featuressvc.Service](),
		CredentialMail: container.MustMake[*credentialmail.Service](),
	})
}

func newDashboardInvitesController() *dashaccounts.InvitesController {
	return dashaccounts.NewInvitesController(dashaccounts.InvitesControllerDeps{
		Accounts:       container.MustMake[*accountsvc.Service](),
		Users:          container.MustMake[*usersvc.Service](),
		CredentialMail: container.MustMake[*credentialmail.Service](),
	})
}
