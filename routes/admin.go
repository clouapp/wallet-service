package routes

import (
	"github.com/goravel/framework/contracts/route"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
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
	dashwalletbalances "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets/balances"
	dashwalletsettings "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets/settings"
	dashwallettransactions "github.com/macrowallets/waas/app/http/controllers/dashboard/wallets/transactions"
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
	"github.com/macrowallets/waas/app/services/walletops"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/walletsettings"
	"github.com/macrowallets/waas/app/services/walletview"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// RegisterAdminRoutes registers dashboard session-auth routes under /v1.
func RegisterAdminRoutes() {
	noCache := middleware.CacheControl(0)
	accounts := container.MustMake[*accountsvc.Service]()
	accountHeader := middleware.AccountHeader(accounts)
	whitelist := container.MustMake[*walletrecords.Whitelist]()
	walletWebhooks := container.MustMake[*walletrecords.Webhooks]()
	requireTOTP := middleware.RequireEnabledTOTP(
		container.MustMake[*usersvc.Service](),
		container.MustMake[*authsvc.SecondFactorVerifier](),
		whitelist,
		walletWebhooks,
	)
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
		router.Middleware(middleware.Throttle(middleware.ThrottleAuth)).Post("/register", authCtrl.Register)
		router.Middleware(middleware.Throttle(middleware.ThrottleLogin)).Post("/login", authCtrl.Login)
		router.Middleware(middleware.Throttle(middleware.ThrottleAuth)).Post("/2fa/verify", authCtrl.VerifyTwoFactor)
		router.Middleware(middleware.Throttle(middleware.ThrottleAuth)).Post("/refresh", authCtrl.RefreshToken)
		router.Middleware(middleware.Throttle(middleware.ThrottleRecover)).Post("/recover", authCtrl.ForgotPassword)
		router.Middleware(middleware.Throttle(middleware.ThrottleAuth)).Post("/recover/confirm", authCtrl.ResetPassword)
		router.Get("/invites/{token}", inviteCtrl.Preview)
		router.Middleware(middleware.Throttle(middleware.ThrottleAuth)).Post("/invites/accept", inviteCtrl.Accept)
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
			r.Middleware(middleware.Can(accounts, middleware.PermAccountWrite)).Patch("", accountsCtrl.UpdateAccount)
			r.Middleware(middleware.Can(accounts, middleware.PermAccountLifecycle)).Post("/archive", accountsCtrl.ArchiveAccount)
			r.Middleware(middleware.Can(accounts, middleware.PermAccountLifecycle)).Post("/freeze", accountsCtrl.FreezeAccount)

			r.Middleware(middleware.Can(accounts, middleware.PermUsersRead)).Get("/users", accountsCtrl.ListAccountUsers)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersWrite)).Post("/users", accountsCtrl.AddAccountUser)
			r.Middleware(middleware.AccountUpdateMember(accounts)).Patch("/users/{userId}", accountsCtrl.UpdateAccountUser)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersWrite)).Delete("/users/{userId}", accountsCtrl.RemoveAccountUser)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersRead)).Get("/invites", inviteCtrl.List)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersWrite)).Post("/invites", inviteCtrl.Create)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersWrite)).Post("/invites/{id}/resend", inviteCtrl.Resend)
			r.Middleware(middleware.Can(accounts, middleware.PermUsersWrite)).Delete("/invites/{id}", inviteCtrl.Delete)

			// S3.4.2: GET /v1/accounts/{accountId}/roles roles.read.
			// Effective grants are the code catalog. There is no
			// account_role_permissions row and no write on this path.
			r.Middleware(middleware.Can(accounts, middleware.PermRolesRead)).Get("/roles", accountRolesCtrl.Index)
			// S3.4.2: GET /v1/accounts/{accountId}/permissions roles.read.
			// The catalog is the same code. There is no permissions table
			// and no write on this path.
			r.Middleware(middleware.Can(accounts, middleware.PermRolesRead)).Get("/permissions", accountRolesCtrl.Permissions)

			r.Middleware(middleware.Can(accounts, middleware.PermTokensRead)).Get("/tokens", accountsCtrl.ListAccountTokens)
			r.Middleware(middleware.Can(accounts, middleware.PermTokensWrite), middleware.MintAPITokenPermissions()).Post("/tokens", accountsCtrl.CreateAccountToken)
			r.Middleware(middleware.Can(accounts, middleware.PermTokensWrite)).Delete("/tokens/{tokenId}", accountsCtrl.RevokeAccountToken)

			// S1.4.7: GET /v1/accounts/{accountId}/settings settings.read (policies.MayViewSettings).
			r.Middleware(middleware.MayViewSettings()).Get("/settings", accountSettingsCtrl.Show)
			// S1.4.7: POST /v1/accounts/{accountId}/settings/sections/{section}/cache settings.write (policies.MayUpdateSettings).
			// Owner and admin may flush an account-managed section. Auditor and user may not.
			r.Middleware(middleware.MayUpdateSettings()).Post("/settings/sections/{section}/cache", accountSettingsCtrl.Flush)
			// S1.4.7: POST /v1/accounts/{accountId}/settings/sections/{section}/reset settings.write (policies.MayUpdateSettings).
			// Owner and admin may reset an account-managed section. Auditor and user may not.
			r.Middleware(middleware.MayUpdateSettings()).Post("/settings/sections/{section}/reset", accountSettingsCtrl.Reset)
			// S1.4.7: GET /v1/accounts/{accountId}/settings/{group} settings.read (policies.MayViewSettings).
			// Platform-managed groups stay readable for owner, admin, and auditor.
			r.Middleware(middleware.MayViewSettings()).Get("/settings/{group}", accountSettingsCtrl.ShowGroup)
			// S1.4.7: PATCH and PUT /v1/accounts/{accountId}/settings/{group} settings.write (policies.MayUpdateSettings).
			// Owner and admin may write an account-managed group. Auditor and user may not.
			r.Middleware(middleware.MayUpdateSettings()).Patch("/settings/{group}", accountSettingsCtrl.Update)
			r.Middleware(middleware.MayUpdateSettings()).Put("/settings/{group}", accountSettingsCtrl.Update)

			// S3.4.2: GET /v1/accounts/{accountId}/activity activity.read (policies.MayReadActivity).
			// Owner, admin, and auditor may list. User may not.
			r.Middleware(middleware.MayReadActivity()).Get("/activity", accountActivityCtrl.Index)
			// S3.4.2: GET /v1/accounts/{accountId}/activity/{id} activity.read (policies.MayReadActivity).
			// Owner, admin, and auditor may read one event. User may not.
			// The body is one element of the list. There is no write on this path.
			r.Middleware(middleware.MayReadActivity()).Get("/activity/{id}", accountActivityCtrl.Show)

			// S2.4: GET /v1/accounts/{accountId}/features settings.read (policies.MayViewSettings).
			// Owner, admin, and auditor may list. User may not.
			// There is no account-side write.
			// Platform PUT /v1/platform/features/account/{id} stores the flag.
			r.Middleware(middleware.MayViewAccountFeatures()).Get("/features", accountFeaturesCtrl.Index)
		})
	})

	facades.Route().Prefix("/v1/platform").Middleware(
		middleware.SessionAuth(),
		middleware.PlatformAdmin(container.MustMake[*usersvc.Service](), platformRefusal),
		noCache,
	).Group(func(router route.Router) {
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
		// user and chain are 404 for an admin. PlatformAdmin refuses a member first.
		router.Put("/features/{scope}/{id}/{feature}", platformFeaturesCtrl.UpdateScopeFeature)
		router.Put("/features/{scope}/{id}", platformFeaturesCtrl.UpdateScope)
		// S1.4.7: chains.view and chains.update. A platform_admins row is the gate.
		router.Patch("/chains/{chainId}/rpc", platformChainsCtrl.UpdateRPC)
		router.Patch("/chains/{chainId}", platformChainsCtrl.Update)
		// Declared before {group} so the literal path mail/test is not a group name.
		// S1.4.6: POST /v1/platform/settings/mail/test settings.update + mail.update (declared before {group}).
		router.Post("/settings/mail/test", platformSettingsCtrl.Test)
		router.Get("/settings", platformSettingsCtrl.Index)
		router.Get("/settings/{group}", platformSettingsCtrl.Show)
		// S3.4.1: GET /v1/platform/accounts accounts.view.
		// A platform_admins row is the gate. The plan does not name fields,
		// pagination, or sort, so this list matches GET /v1/platform/users.
		router.Get("/accounts", platformAccountsCtrl.Index)
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
		router.Get("", chainCtrl.Index)
		router.Get("/{chainId}", chainCtrl.Show)
		router.Get("/{chainId}/tokens", chainCtrl.Tokens)
		router.Get("/{chainId}/resources", chainCtrl.Resources)
	})

	facades.Route().Prefix("/v1/currencies").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", currencyCtrl.Index)
		router.Get("/{code}", currencyCtrl.Show)
	})

	facades.Route().Prefix("/v1/me").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("/preferences", preferencesCtrl.GetPreferences)
		router.Put("/preferences", preferencesCtrl.UpdatePreferences)
	})

	facades.Route().Prefix("/v1/convert").Middleware(middleware.SessionAuth(), noCache).Group(func(router route.Router) {
		router.Get("", currencyCtrl.Convert)
	})

	facades.Route().Prefix("/v1/wallets").Middleware(middleware.SessionAuth(), accountHeader, totpEnrollment, noCache).Group(func(router route.Router) {
		router.Get("", walletCtrl.Index)
		router.Middleware(middleware.RequireFundAction(middleware.FundCreateWallet)).Post("", walletCtrl.Store)
		router.Get("/{walletId}", walletCtrl.Show)
		router.Prefix("/{walletId}").Middleware(middleware.WalletContext(middleware.WalletContextDeps{
			Wallets:  container.MustMake[*walletrecords.Wallets](),
			Accounts: accounts,
			Members:  container.MustMake[*walletrecords.Members](),
		})).Group(func(r route.Router) {
			r.Post("/activate", walletCtrl.Activate)

			r.Get("/addresses", addressCtrl.Index)
			r.Middleware(middleware.Can(accounts, middleware.PermAddressesCreate)).Post("/addresses", addressCtrl.Store)
			r.Patch("/addresses/{addressId}", addressCtrl.Update)

			r.Get("/users", walletUsersCtrl.ListWalletUsers)
			r.Middleware(middleware.WalletAddUser(walletPolicyMemberships())).Post("/users", walletUsersCtrl.AddWalletUser)
			r.Middleware(middleware.WalletRemoveUser(walletPolicyMemberships())).Delete("/users/{userId}", walletUsersCtrl.RemoveWalletUser)

			r.Get("/whitelist", whitelistCtrl.ListWhitelistEntries)
			r.Middleware(middleware.WalletWhitelist(walletPolicyMemberships(), whitelist), requireTOTP).Post("/whitelist", whitelistCtrl.AddWhitelistEntry)
			r.Middleware(middleware.WalletWhitelist(walletPolicyMemberships(), whitelist), requireTOTP).Delete("/whitelist/{entryId}", whitelistCtrl.DeleteWhitelistEntry)

			r.Get("/webhooks", walletWebhooksCtrl.ListWalletWebhooks)
			r.Middleware(middleware.WalletManageWebhooks(walletPolicyMemberships(), walletWebhooks), requireTOTP).Post("/webhooks", walletWebhooksCtrl.CreateWalletWebhook)
			r.Middleware(middleware.WalletManageWebhooks(walletPolicyMemberships(), walletWebhooks)).Post("/webhooks/{webhookId}/test", walletWebhooksCtrl.TestWalletWebhook)
			r.Middleware(middleware.WalletManageWebhooks(walletPolicyMemberships(), walletWebhooks), requireTOTP).Delete("/webhooks/{webhookId}", walletWebhooksCtrl.DeleteWalletWebhook)

			r.Get("/settings", walletSettingsCtrl.Show)
			r.Patch("/settings", walletSettingsCtrl.Update)
			r.Middleware(middleware.WalletFreeze(walletPolicyMemberships())).Post("/freeze", walletSettingsCtrl.Freeze)
			r.Middleware(middleware.WalletArchive(walletPolicyMemberships())).Post("/archive", walletSettingsCtrl.Archive)

			r.Get("/balances", balancesCtrl.Index)

			r.Get("/transactions", walletTxCtrl.Index)
			r.Get("/transactions/{txId}", walletTxCtrl.Show)

			r.Get("/withdrawals", withdrawalCtrl.Index)
			r.Middleware(middleware.RequireFundAction(middleware.FundWithdraw)).Post("/withdrawals", withdrawalCtrl.Store)
			r.Post("/withdrawals/estimate", withdrawalCtrl.Estimate)
			r.Get("/fee-estimate", feeEstimateCtrl.GetWalletFeeEstimate)
			r.Get("/withdrawals/{withdrawalId}", withdrawalCtrl.Show)
			r.Middleware(middleware.WalletCancelWithdrawal(walletPolicyMemberships(), container.MustMake[*withdrawalrecords.Records]())).Post("/withdrawals/{withdrawalId}/cancel", withdrawalCtrl.Cancel)

			r.Middleware(middleware.RequireFundAction(middleware.FundSweep)).Post("/consolidate", sweepCtrl.Consolidate)
			r.Get("/gas-status", sweepCtrl.GasStatus)
			r.Middleware(middleware.Throttle(middleware.ThrottleGasCheck)).Post("/gas-check", sweepCtrl.GasCheck)
			r.Post("/withdraw/preview", sweepCtrl.Preview)

			r.Prefix("/unspents").Middleware(middleware.UTXOOnly(container.MustMake[*walletrecords.Wallets]())).Group(func(ur route.Router) {
				ur.Get("", unspentsCtrl.ListUnspentOutputs)
			})
		})
	})

	// Dashboard withdrawal detail. Same session and account-header auth as
	// GET /v1/wallets/{walletId}/withdrawals; the id is not scoped by a wallet path.
	facades.Route().Prefix("/v1/withdrawals").Middleware(middleware.SessionAuth(), accountHeader, totpEnrollment, noCache).Group(func(router route.Router) {
		router.Get("/{withdrawalId}", withdrawalCtrl.ShowInAccount)
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

func newDashboardWalletsController() *dashwallets.WalletController {
	return dashwallets.NewWalletController(newWalletView(), newWalletOps())
}

// newWalletView composes the wallet reads both surfaces serve. The encryption
// key is read on each use, so it may be bound after the routes are built.
func newWalletView() *walletview.Service {
	return walletview.NewService(walletview.Deps{
		Wallets:      container.MustMake[*walletrecords.Wallets](),
		Members:      container.MustMake[*walletrecords.Members](),
		Balances:     container.MustMake[*walletrecords.Balances](),
		Transactions: container.MustMake[*walletrecords.Transactions](),
		Chains:       container.MustMake[*chainsvc.Service](),
		Cipher:       func() settingssvc.Cipher { return facades.Crypt() },
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

func newDashboardWalletSettingsController() *dashwalletsettings.SettingsController {
	return dashwalletsettings.NewSettingsController(
		walletsettings.NewService(walletsettings.Deps{
			Wallets:  container.MustMake[*walletrecords.Wallets](),
			Chains:   container.MustMake[*chainsvc.Service](),
			Networks: newWalletView(),
		}),
		walletPolicyMemberships(),
	)
}

func newDashboardBalancesController() *dashwalletbalances.BalanceController {
	return dashwalletbalances.NewBalanceController(newWalletView())
}

func newDashboardWalletTransactionsController() *dashwallettransactions.TransactionController {
	return dashwallettransactions.NewTransactionController(newWalletView())
}

func newDashboardChainsController() *dashchains.ChainController {
	return dashchains.NewChainController(
		container.MustMake[*chainsvc.Service](),
	)
}

func newDashboardCurrenciesController() *dashcurrencies.CurrencyController {
	return dashcurrencies.NewCurrencyController(
		container.MustMake[*currencies.Service](),
		container.MustMake[*price.Service](),
	)
}

func newDashboardPreferencesController() *dashpreferences.PreferencesController {
	return dashpreferences.NewPreferencesController(dashpreferences.PreferencesControllerDeps{
		Users:      container.MustMake[*usersvc.Service](),
		Currencies: container.MustMake[*currencies.Service](),
	})
}

func newDashboardAddressesController() *dashaddresses.AddressController {
	return dashaddresses.NewAddressController(newWalletOps())
}

// newWalletOps composes the wallet and address operations both surfaces serve.
func newWalletOps() *walletops.Service {
	return walletops.NewService(walletops.Deps{
		Wallets:   container.MustMake[*walletsvc.Service](),
		Addresses: container.MustMake[*walletrecords.Addresses](),
		Chains:    container.MustMake[*chainsvc.Service](),
		Cache:     container.MustMake[*deposit.Service](),
		Registry:  container.MustMake[*chainpkg.Registry](),
	})
}

func newDashboardWithdrawalsController() *dashwithdrawals.WithdrawalController {
	return dashwithdrawals.NewWithdrawalController(
		container.MustMake[*withdrawalrecords.Records](),
		container.MustMake[*withdraw.Service](),
		container.MustMake[*featuressvc.Service](),
	)
}

func newDashboardSweepController() *dashsweep.SweepController {
	return dashsweep.NewSweepController(dashsweep.SweepControllerDeps{
		Sweeps: container.MustMake[*sweep.Box]().Service,
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

func newPlatformAccountsController() *platformaccounts.AccountController {
	return platformaccounts.NewAccountController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformAccountUsersController() *platformaccounts.UserController {
	return platformaccounts.NewUserController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformAccountOwnersController() *platformaccounts.OwnerController {
	return platformaccounts.NewOwnerController(
		container.MustMake[*accountsvc.Service](),
	)
}

func newPlatformUsersController() *platformusers.UserController {
	return platformusers.NewUserController(
		container.MustMake[*usersvc.Service](),
	)
}

func newPlatformSettingsController() *platformsettings.SettingController {
	return platformsettings.NewSettingController(
		container.MustMake[*settingssvc.Service](),
	)
}

func newPlatformChainsController() *platformchains.ChainController {
	return platformchains.NewChainController(
		container.MustMake[*chainsvc.Thresholds](),
		container.MustMake[*chainsvc.RPC](),
	)
}

func newPlatformFeaturesController() *platformfeatures.FeatureController {
	return platformfeatures.NewFeatureController(
		container.MustMake[*featuressvc.Service](),
		container.MustMake[*accountsvc.Service](),
	)
}

func newDashboardAccountsController() *dashaccounts.AccountsController {
	return dashaccounts.NewAccountsController(dashaccounts.AccountsControllerDeps{
		AccountService: container.MustMake[*accountsvc.Service](),
		Passwords:      container.MustMake[*authsvc.Service](),
		Limits:         container.MustMake[*settingssvc.Service](),
		Features:       container.MustMake[*featuressvc.Service](),
	})
}

func newDashboardInvitesController() *dashaccounts.InvitesController {
	return dashaccounts.NewInvitesController(dashaccounts.InvitesControllerDeps{
		Accounts: container.MustMake[*accountsvc.Service](),
		Users:    container.MustMake[*usersvc.Service](),
	})
}
