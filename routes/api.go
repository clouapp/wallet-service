package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/http/controllers"
	extaddresses "github.com/macrowallets/waas/app/http/controllers/external/addresses"
	extchains "github.com/macrowallets/waas/app/http/controllers/external/chains"
	extsweep "github.com/macrowallets/waas/app/http/controllers/external/sweep"
	exttransactions "github.com/macrowallets/waas/app/http/controllers/external/transactions"
	extwallets "github.com/macrowallets/waas/app/http/controllers/external/wallets"
	extwebhooks "github.com/macrowallets/waas/app/http/controllers/external/webhooks"
	extwithdrawals "github.com/macrowallets/waas/app/http/controllers/external/withdrawals"
	"github.com/macrowallets/waas/app/http/middleware"
	accountsvc "github.com/macrowallets/waas/app/services/account"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/deposit"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/feeestimate"
	"github.com/macrowallets/waas/app/services/sweep"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// RegisterExternalAPI registers Bearer API-token routes under /api/v1.
//
// All per-wallet routes live inside a nested group that applies
// APIWalletContext() so the walletId in the URL is verified to belong to the
// authenticated account before any controller runs. This prevents IDOR: a
// valid API token cannot operate on another account's wallets.
func RegisterExternalAPI() {
	noCache := middleware.CacheControl(0)
	chainCtrl := newExternalChainsController()
	transactionCtrl := newExternalTransactionsController()
	webhookCtrl := newExternalWebhooksController()
	walletCtrl := newExternalWalletsController()
	addressCtrl := newExternalAddressesController()
	sweepCtrl := newExternalSweepController()
	withdrawalCtrl := newExternalWithdrawalsController()
	feeEstimateCtrl := newFeeEstimateController()

	facades.Route().Prefix("/api/v1").Middleware(middleware.Throttle(middleware.ThrottleAPI), middleware.APITokenAuth(
		container.MustMake[*accountsvc.Service](),
	), noCache).Group(func(router route.Router) {
		// APIScope follows the S3.4.6 catalog and the S3.4.2 verbs:
		// wallets.read/create, addresses.create, withdrawals.create,
		// sweep.execute, webhooks.read/write, transactions.read.
		// A route the plan does not name stays behind APITokenAuth only.
		// Wallet routes resolve the wallet (404) before the scope check (403).
		router.Get("/chains", chainCtrl.ListChains)

		router.Middleware(middleware.APIScope(middleware.PermWalletsCreate)).Post("/wallets", walletCtrl.CreateWallet)
		router.Middleware(middleware.APIScope(middleware.PermWalletsRead)).Get("/wallets", walletCtrl.ListWallets)

		router.Get("/addresses/{address}", addressCtrl.LookupAddress)
		router.Get("/users/{external_id}/addresses", addressCtrl.ListUserAddresses)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext()).Group(func(r route.Router) {
			r.Middleware(middleware.APIScope(middleware.PermWalletsRead)).Get("", walletCtrl.GetWallet)

			r.Middleware(middleware.APIScope(middleware.PermAddressesCreate)).Post("/addresses", addressCtrl.GenerateAddress)
			r.Get("/addresses", addressCtrl.ListWalletAddresses)
			r.Patch("/addresses/{addressId}", addressCtrl.UpdateAddress)

			r.Middleware(middleware.APIScope(middleware.PermSweepExecute)).Post("/consolidate", sweepCtrl.ConsolidateWallet)
			r.Get("/gas-status", sweepCtrl.GetGasStatus)
			r.Middleware(middleware.Throttle(middleware.ThrottleGasCheck)).Post("/gas-check", sweepCtrl.ForceGasCheck)
			r.Post("/withdraw/preview", sweepCtrl.PreviewWithdraw)
			r.Get("/fee-estimate", feeEstimateCtrl.GetWalletFeeEstimate)
			r.Middleware(middleware.APIScope(middleware.PermWithdrawalsCreate)).Post("/withdrawals", withdrawalCtrl.CreateWalletWithdrawal)
			r.Get("/withdrawals/{idempotencyKey}", withdrawalCtrl.GetWalletWithdrawalByIdempotencyKey)
		})

		router.Middleware(middleware.APIScope(middleware.PermTransactionsRead)).Group(func(r route.Router) {
			r.Get("/transactions", transactionCtrl.ListTransactions)
			r.Get("/transactions/{id}", transactionCtrl.GetTransaction)
			r.Get("/users/{external_id}/transactions", transactionCtrl.ListUserTransactions)
		})

		router.Middleware(middleware.APIScope(middleware.PermWebhooksWrite)).Post("/webhooks", webhookCtrl.CreateWebhook)
		router.Middleware(middleware.APIScope(middleware.PermWebhooksRead)).Get("/webhooks", webhookCtrl.ListWebhooks)
		router.Middleware(middleware.APIScope(middleware.PermWebhooksWrite)).Patch("/webhooks/{webhookId}", webhookCtrl.UpdateWebhook)
	})
}

func newFeeEstimateController() *controllers.FeeEstimateController {
	return controllers.NewFeeEstimateController(container.MustMake[*feeestimate.Service]())
}

func newExternalTransactionsController() *exttransactions.TransactionsController {
	return exttransactions.NewTransactionsController(container.MustMake[*withdraw.Service]())
}

func newExternalWebhooksController() *extwebhooks.WebhooksController {
	return extwebhooks.NewWebhooksController(container.MustMake[*webhook.Service]())
}

func newExternalChainsController() *extchains.ChainsController {
	return extchains.NewChainsController(
		container.MustMake[*chainsvc.Service](),
	)
}

func newExternalAddressesController() *extaddresses.AddressesController {
	return extaddresses.NewAddressesController(extaddresses.AddressesControllerDeps{
		Addresses:     container.MustMake[*walletrecords.Addresses](),
		WalletService: currentWalletService,
		Deposits:      container.MustMake[*deposit.Service](),
		Registry:      container.MustMake[*chainpkg.Registry](),
	})
}

func newExternalSweepController() *extsweep.SweepController {
	return extsweep.NewSweepController(extsweep.SweepControllerDeps{
		Sweeps: container.MustMake[*sweep.Box]().Service,
		Flags:  container.MustMake[*featuressvc.Service](),
	})
}

func newExternalWithdrawalsController() *extwithdrawals.WithdrawalsController {
	return extwithdrawals.NewWithdrawalsController(extwithdrawals.WithdrawalsControllerDeps{
		Withdrawals:       container.MustMake[*withdrawalrecords.Records](),
		Chains:            container.MustMake[*chainsvc.Service](),
		Users:             container.MustMake[*usersvc.Service](),
		Transactions:      container.MustMake[*walletrecords.Transactions](),
		Registry:          container.MustMake[*chainpkg.Registry](),
		WithdrawalService: container.MustMake[*withdraw.Service](),
		Passwords:         container.MustMake[*authsvc.Service](),
		Flags:             container.MustMake[*featuressvc.Service](),
		Events:            container.MustMake[*withdrawalevents.Publisher](),
		Redis:             container.MustMake[*container.SharedRedis]().Client,
		SecondFactor:      container.MustMake[*authsvc.SecondFactorVerifier](),
	})
}

func newExternalWalletsController() *extwallets.WalletsController {
	return extwallets.NewWalletsController(extwallets.WalletsControllerDeps{
		Wallets:       container.MustMake[*walletrecords.Wallets](),
		WalletService: currentWalletService,
	})
}
