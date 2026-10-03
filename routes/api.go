package routes

import (
	"github.com/goravel/framework/contracts/route"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/container"
	extaddresses "github.com/macrowallets/waas/app/http/controllers/external/addresses"
	extchains "github.com/macrowallets/waas/app/http/controllers/external/chains"
	extsweep "github.com/macrowallets/waas/app/http/controllers/external/sweep"
	exttransactions "github.com/macrowallets/waas/app/http/controllers/external/transactions"
	extwallets "github.com/macrowallets/waas/app/http/controllers/external/wallets"
	extwebhooks "github.com/macrowallets/waas/app/http/controllers/external/webhooks"
	extwithdrawals "github.com/macrowallets/waas/app/http/controllers/external/withdrawals"
	"github.com/macrowallets/waas/app/http/middleware"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainsvc "github.com/macrowallets/waas/app/services/chains"
	featuressvc "github.com/macrowallets/waas/app/services/features"
	usersvc "github.com/macrowallets/waas/app/services/users"
	"github.com/macrowallets/waas/app/services/walletrecords"
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

	facades.Route().Prefix("/api/v1").Middleware(middleware.APITokenAuth(), noCache).Group(func(router route.Router) {
		router.Get("/chains", chainCtrl.ListChains)

		router.Post("/wallets", walletCtrl.CreateWallet)
		router.Get("/wallets", walletCtrl.ListWallets)

		router.Get("/addresses/{address}", addressCtrl.LookupAddress)
		router.Get("/users/{external_id}/addresses", addressCtrl.ListUserAddresses)

		router.Prefix("/wallets/{walletId}").Middleware(middleware.APIWalletContext()).Group(func(r route.Router) {
			r.Get("", walletCtrl.GetWallet)

			r.Post("/addresses", addressCtrl.GenerateAddress)
			r.Get("/addresses", addressCtrl.ListWalletAddresses)
			r.Patch("/addresses/{addressId}", addressCtrl.UpdateAddress)

			r.Post("/consolidate", sweepCtrl.ConsolidateWallet)
			r.Get("/gas-status", sweepCtrl.GetGasStatus)
			r.Post("/gas-check", sweepCtrl.ForceGasCheck)
			r.Post("/withdraw/preview", sweepCtrl.PreviewWithdraw)
			r.Post("/withdrawals", withdrawalCtrl.CreateWalletWithdrawal)
			r.Get("/withdrawals/{idempotencyKey}", withdrawalCtrl.GetWalletWithdrawalByIdempotencyKey)
		})

		router.Get("/transactions", transactionCtrl.ListTransactions)
		router.Get("/transactions/{id}", transactionCtrl.GetTransaction)
		router.Get("/users/{external_id}/transactions", transactionCtrl.ListUserTransactions)

		router.Post("/webhooks", webhookCtrl.CreateWebhook)
		router.Get("/webhooks", webhookCtrl.ListWebhooks)
		router.Patch("/webhooks/{webhookId}", webhookCtrl.UpdateWebhook)
	})
}

func newExternalTransactionsController() *exttransactions.TransactionsController {
	return exttransactions.NewTransactionsController(container.Get().WithdrawalService)
}

func newExternalWebhooksController() *extwebhooks.WebhooksController {
	return extwebhooks.NewWebhooksController(container.Get().WebhookService)
}

func newExternalChainsController() *extchains.ChainsController {
	return extchains.NewChainsController(
		container.MustMake[*chainsvc.Service](),
	)
}

func newExternalAddressesController() *extaddresses.AddressesController {
	return extaddresses.NewAddressesController(
		container.MustMake[*walletrecords.Addresses](),
		currentWalletService,
		container.Get().DepositService,
		container.Get().Registry,
	)
}

func newExternalSweepController() *extsweep.SweepController {
	return extsweep.NewSweepController(
		container.Get().SweepService,
		container.Get().Redis,
		container.MustMake[*featuressvc.Service](),
	)
}

func newExternalWithdrawalsController() *extwithdrawals.WithdrawalsController {
	deps := container.Get()
	return extwithdrawals.NewWithdrawalsController(
		container.MustMake[*withdrawalrecords.Records](),
		container.MustMake[*chainsvc.Service](),
		container.MustMake[*usersvc.Service](),
		container.MustMake[*walletrecords.Transactions](),
		deps.Registry,
		deps.WithdrawalService,
		container.MustMake[*authsvc.Service](),
		container.MustMake[*featuressvc.Service](),
		deps.WithdrawalEvents,
		deps.Redis,
	)
}

func newExternalWalletsController() *extwallets.WalletsController {
	return extwallets.NewWalletsController(
		container.MustMake[*walletrecords.Wallets](),
		currentWalletService,
	)
}
