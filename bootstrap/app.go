package bootstrap

import (
	"github.com/goravel/framework/contracts/console"
	contractsevent "github.com/goravel/framework/contracts/event"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/container"
	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/listeners"
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
			}
		}).
		WithCommands(func() []console.Command {
			balances := container.MustMake[*refresh.BalanceService]()
			deposits := container.MustMake[*deposit.Service]()
			registry := container.MustMake[*chainpkg.Registry]()
			prices := container.MustMake[*price.Service]()
			return []console.Command{
				commands.NewRefreshWallet(balances),
				commands.NewRefreshAddress(balances),
				commands.NewRefreshCurrency(registry, balances),
				commands.NewRefreshTx(balances),
				commands.NewScanDeposits(deposits),
				commands.NewReconcileWallet(balances),
				commands.NewPriceWebSocket(prices, container.MustMake[*price.CoinAPICredential]().Key, container.MustMake[*container.SharedRedis]().Client),
				commands.NewPriceCheckUpdate(prices),
				&commands.ChainsSetRPC{},
				commands.NewChainsAlignNetwork(deposits),
				&commands.WithdrawPreflight{},
			}
		}).
		WithEvents(func() map[contractsevent.Event][]contractsevent.Listener {
			return map[contractsevent.Event][]contractsevent.Listener{
				&events.WalletCreated{}:          {&listeners.EnqueueWalletRefresh{}},
				&events.WalletActivated{}:        {&listeners.EnqueueWalletRefresh{}},
				&events.DepositDetected{}:        {&listeners.EnqueueTransactionRefresh{}},
				&events.WithdrawalBroadcasted{}:  {&listeners.EnqueueWalletRefresh{}},
				&events.WalletRefreshRequested{}: {&listeners.EnqueueWalletRefresh{}},
			}
		}).
		WithRules(Rules).
		WithConfig(config.Boot).
		Create()
}
