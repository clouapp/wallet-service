package routes

import (
	"github.com/goravel/framework/contracts/route"

	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/facades"
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
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/feeestimate"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
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
	scopeLookups := middleware.ScopeLookups{
		Transactions: container.MustMake[*walletrecords.Transactions](),
		Webhooks:     container.MustMake[*walletrecords.Webhooks](),
	}

	facades.Route().Prefix("/api/v1").Middleware(middleware.Throttle(middleware.ThrottleAPI), middleware.APITokenAuth(
		container.MustMake[*accountsvc.Service](),
	), noCache).Group(func(router route.Router) {
		// APIScope follows the S3.4.6 catalog and the S3.4.2 verbs:
		// wallets.read/create, addresses.create, withdrawals.create,
		// sweep.execute, webhooks.read/write, transactions.read.
		// A route the plan does not name stays behind APITokenAuth only.
		// Wallet routes resolve the wallet (404) before the scope check (403).
		router.Get("/chains", chainCtrl.Index)

		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermWalletsCreate)).Post("/wallets", walletCtrl.Store)
		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermWalletsRead)).Get("/wallets", walletCtrl.Index)

		router.Get("/addresses/{address}", addressCtrl.Show)
		router.Get("/users/{external_id}/addresses", addressCtrl.ByUser)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext(container.MustMake[*walletrecords.Wallets]())).Group(func(r route.Router) {
			r.Middleware(middleware.APIScope(scopeLookups, middleware.PermWalletsRead)).Get("", walletCtrl.Show)

			r.Middleware(middleware.APIScope(scopeLookups, middleware.PermAddressesCreate)).Post("/addresses", addressCtrl.Store)
			r.Get("/addresses", addressCtrl.Index)
			r.Patch("/addresses/{addressId}", addressCtrl.Update)

			r.Middleware(middleware.APIScope(scopeLookups, middleware.PermSweepExecute)).Post("/consolidate", sweepCtrl.Consolidate)
			r.Get("/gas-status", sweepCtrl.GasStatus)
			r.Middleware(middleware.Throttle(middleware.ThrottleGasCheck)).Post("/gas-check", sweepCtrl.GasCheck)
			r.Post("/withdraw/preview", sweepCtrl.Preview)
			r.Get("/fee-estimate", feeEstimateCtrl.GetWalletFeeEstimate)
			r.Middleware(middleware.APIScope(scopeLookups, middleware.PermWithdrawalsCreate)).Post("/withdrawals", withdrawalCtrl.Store)
			r.Get("/withdrawals/{idempotencyKey}", withdrawalCtrl.ShowByKey)
		})

		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermTransactionsRead)).Group(func(r route.Router) {
			r.Get("/transactions", transactionCtrl.Index)
			r.Get("/transactions/{id}", transactionCtrl.Show)
			r.Get("/users/{external_id}/transactions", transactionCtrl.IndexByUser)
		})

		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermWebhooksWrite)).Post("/webhooks", webhookCtrl.Store)
		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermWebhooksRead)).Get("/webhooks", webhookCtrl.Index)
		router.Middleware(middleware.APIScope(scopeLookups, middleware.PermWebhooksWrite)).Patch("/webhooks/{webhookId}", webhookCtrl.Update)
	})
}

func newFeeEstimateController() *controllers.FeeEstimateController {
	return controllers.NewFeeEstimateController(container.MustMake[*feeestimate.Service]())
}

func newExternalTransactionsController() *exttransactions.TransactionController {
	return exttransactions.NewTransactionController(container.MustMake[*withdraw.Service]())
}

func newExternalWebhooksController() *extwebhooks.WebhookController {
	return extwebhooks.NewWebhookController(container.MustMake[*webhook.Service]())
}

func newExternalChainsController() *extchains.ChainController {
	return extchains.NewChainController(
		container.MustMake[*chainsvc.Service](),
	)
}

func newExternalAddressesController() *extaddresses.AddressController {
	return extaddresses.NewAddressController(newWalletOps())
}

func newExternalSweepController() *extsweep.SweepController {
	return extsweep.NewSweepController(extsweep.SweepControllerDeps{
		Sweeps: container.MustMake[*sweep.Box]().Service,
		Flags:  container.MustMake[*featuressvc.Service](),
	})
}

func newExternalWithdrawalsController() *extwithdrawals.WithdrawalController {
	return extwithdrawals.NewWithdrawalController(
		container.MustMake[*withdrawalrecords.Records](),
		container.MustMake[*withdraw.Service](),
		container.MustMake[*featuressvc.Service](),
	)
}

func newExternalWalletsController() *extwallets.WalletController {
	return extwallets.NewWalletController(newWalletView(), newWalletOps())
}
