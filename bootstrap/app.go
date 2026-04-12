package bootstrap

import (
	"github.com/goravel/framework/contracts/console"
	contractsevent "github.com/goravel/framework/contracts/event"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	"github.com/goravel/framework/contracts/queue"
	"github.com/goravel/framework/foundation"

	"github.com/macrowallets/waas/app/console/commands"
	"github.com/macrowallets/waas/app/events"
	"github.com/macrowallets/waas/app/jobs"
	"github.com/macrowallets/waas/app/listeners"
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
			return []queue.Job{
				&jobs.RefreshWalletBalances{},
				&jobs.RefreshWalletTransactions{},
				&jobs.RefreshWalletTokens{},
				&jobs.RefreshWalletUTXOs{},
				&jobs.ReconcileWalletState{},
			}
		}).
		WithCommands(func() []console.Command {
			return []console.Command{
				&commands.RefreshWallet{},
				&commands.RefreshAddress{},
				&commands.RefreshCurrency{},
				&commands.RefreshTx{},
				&commands.ReconcileWallet{},
				&commands.PriceWebSocket{},
				&commands.PriceCheckUpdate{},
			}
		}).
		WithEvents(func() map[contractsevent.Event][]contractsevent.Listener {
			return map[contractsevent.Event][]contractsevent.Listener{
				&events.WalletCreated{}:         {&listeners.EnqueueWalletRefresh{}},
				&events.WalletActivated{}:       {&listeners.EnqueueWalletRefresh{}},
				&events.DepositDetected{}:        {&listeners.EnqueueTransactionRefresh{}},
				&events.WithdrawalBroadcasted{}:  {&listeners.EnqueueWalletRefresh{}},
				&events.WalletRefreshRequested{}: {&listeners.EnqueueWalletRefresh{}},
			}
		}).
		WithRules(Rules).
		WithConfig(config.Boot).
		Create()
}
