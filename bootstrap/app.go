package bootstrap

import (
	"time"

	"github.com/goravel/framework/contracts/console"
	contractsevent "github.com/goravel/framework/contracts/event"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/adapters/redis/pricecache"
	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/dtos"
	appfacades "github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/http/middleware"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/listeners"
	"github.com/macrowallets/waas/app/providers"
	"github.com/macrowallets/waas/app/repositories"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/config"
	"github.com/macrowallets/waas/database/seeders"
)

// Boot wires Goravel and returns the application instance.
func Boot() contractsfoundation.Application {
	return foundation.Setup().
		WithMigrations(Migrations).
		WithProviders(Providers).
		WithSeeders(seeders.All).
		WithJobs(func() []queue.Job {
			balances := container.MustMake[*refresh.BalanceService]()
			return []queue.Job{
				jobs.NewRefreshWalletBalances(balances),
				jobs.NewRefreshWalletTransactions(balances),
				jobs.NewRefreshWalletTokens(balances),
				&jobs.RefreshWalletUTXOs{},
				jobs.NewReconcileWalletState(balances),
				&jobs.SendCredentialMailJob{},
			}
		}).
		WithCommands(func() []console.Command {
			balances := container.MustMake[*refresh.BalanceService]()
			dispatcher := listeners.NewRefreshDispatcher()
			deposits := container.MustMake[*deposit.Service]()
			registry := container.MustMake[*chainpkg.Registry]()
			prices := container.MustMake[*price.Service]()
			return []console.Command{
				commands.NewRefreshWallet(balances, dispatcher),
				commands.NewRefreshAddress(balances, dispatcher),
				commands.NewRefreshCurrency(registry, balances, dispatcher),
				commands.NewRefreshTx(balances, dispatcher),
				commands.NewScanDeposits(deposits),
				commands.NewReconcileWallet(balances, dispatcher),
				commands.NewPriceWebSocket(prices, container.MustMake[*price.CoinAPICredential]().Key, pricecache.New(container.MustMake[*container.SharedRedis]().Client)),
				commands.NewPriceCheckUpdate(prices),
				&commands.ChainsSetRPC{},
				commands.NewChainsAlignNetwork(deposits),
				commands.NewChainsAddMissing(seedMissingAddedChains),
				&commands.WithdrawPreflight{},
				commands.NewPruneActivity(),
				commands.NewEVMCall(commands.EVMCallDeps{
					Wallets: container.MustMake[*repositories.WalletRepository](),
					Signer:  evmCallSigner(),
				}),
				commands.NewWalletsExportKeys(
					container.MustMake[*repositories.WalletRepository](),
					container.MustMake[*repositories.AddressRepository](),
					container.MustMake[*repositories.ChainRepository](),
				),
			}
		}).
		WithEvents(func() map[contractsevent.Event][]contractsevent.Listener {
			return map[contractsevent.Event][]contractsevent.Listener{
				&dtos.WalletCreated{}:          {&listeners.EnqueueWalletRefresh{}},
				&dtos.WalletActivated{}:        {&listeners.EnqueueWalletRefresh{}},
				&dtos.DepositDetected{}:        {&listeners.EnqueueTransactionRefresh{}},
				&dtos.WithdrawalBroadcasted{}:  {&listeners.EnqueueWalletRefresh{}},
				&dtos.WalletRefreshRequested{}: {&listeners.EnqueueWalletRefresh{}},
			}
		}).
		WithRules(Rules).
		WithConfig(config.Boot).
		WithMiddleware(func(h contractsconfiguration.Middleware) {
			h.Use(middleware.GlobalChain(requestTimeout())...).
				Recover(middleware.RecoverPanic)
		}).
		WithRouting(providers.RegisterRoutes).
		Create()
}

// requestTimeout is http.request_timeout, the same key the gin driver used.
// Zero or a missing config leaves the chain without a deadline.
func requestTimeout() time.Duration {
	const defaultRequestTimeoutSeconds = 30
	seconds := defaultRequestTimeoutSeconds
	if cfg := appfacades.Config(); cfg != nil {
		seconds = cfg.GetInt("http.request_timeout", defaultRequestTimeoutSeconds)
	}
	if seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
