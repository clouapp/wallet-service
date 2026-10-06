package bootstrap

import (
	"time"

	"github.com/goravel/framework/contracts/console"
	contractsevent "github.com/goravel/framework/contracts/event"
	contractsfoundation "github.com/goravel/framework/contracts/foundation"
	contractsconfiguration "github.com/goravel/framework/contracts/foundation/configuration"
	"github.com/goravel/framework/contracts/queue"
	goravelfacades "github.com/goravel/framework/facades"
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
	"github.com/macrowallets/waas/app/services/activity"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/chains"
	"github.com/macrowallets/waas/app/services/credentialmail"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/walletrecords"
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
			refresher := container.MustMake[*refresh.WalletRefresher]()
			return []queue.Job{
				jobs.NewRefreshWalletBalances(refresher),
				jobs.NewRefreshWalletTransactions(refresher),
				jobs.NewRefreshWalletTokens(refresher),
				jobs.NewRefreshWalletUTXOs(refresher),
				jobs.NewReconcileWalletState(refresher),
				jobs.NewSendCredentialMailJob(container.MustMake[*credentialmail.Service]()),
			}
		}).
		WithCommands(func() []console.Command {
			balances := container.MustMake[*refresh.BalanceService]()
			dispatcher := listeners.NewRefreshDispatcher()
			deposits := container.MustMake[*deposit.Service]()
			registry := container.MustMake[*chainpkg.Registry]()
			prices := container.MustMake[*price.Service]()
			wallets := container.MustMake[*walletrecords.Wallets]()
			addresses := container.MustMake[*walletrecords.Addresses]()
			transactions := container.MustMake[*walletrecords.Transactions]()
			return []console.Command{
				commands.NewRefreshWallet(commands.RefreshWalletDeps{
					Balances:       balances,
					Dispatcher:     dispatcher,
					Wallets:        wallets,
					RequestRefresh: dispatchWalletRefreshRequested,
				}),
				commands.NewRefreshAddress(commands.RefreshAddressDeps{
					Balances:   balances,
					Dispatcher: dispatcher,
					Wallets:    wallets,
					Addresses:  addresses,
				}),
				commands.NewRefreshCurrency(commands.RefreshCurrencyDeps{
					Registry:   registry,
					Balances:   balances,
					Dispatcher: dispatcher,
					Wallets:    wallets,
					Addresses:  addresses,
				}),
				commands.NewRefreshTx(commands.RefreshTxDeps{
					Balances:     balances,
					Dispatcher:   dispatcher,
					Wallets:      wallets,
					Transactions: transactions,
				}),
				commands.NewScanDeposits(deposits),
				commands.NewReconcileWallet(commands.ReconcileWalletDeps{
					Balances:   balances,
					Dispatcher: dispatcher,
					Wallets:    wallets,
				}),
				commands.NewPriceWebSocket(commands.PriceWebSocketDeps{
					Prices:     prices,
					CoinAPIKey: container.MustMake[*price.CoinAPICredential]().Key,
					Cache:      pricecache.New(container.MustMake[*container.SharedRedis]().Client),
				}),
				commands.NewPriceCheckUpdate(prices),
				commands.NewChainsSetRPC(chains.NewReplaceRPC(chains.ReplaceRPCDeps{
					Store: container.MustMake[*repositories.ChainRepository](),
					Seal:  func(plaintext string) (string, error) { return goravelfacades.Crypt().EncryptString(plaintext) },
				})),
				commands.NewChainsAlignNetwork(chainregistry.NewAligner(chainregistry.AlignerDeps{
					Store:   repositories.NewChainRegistryRepository(nil),
					Decrypt: decryptChainRPC,
					Probe:   chainregistry.ProbeRPCNetwork,
					Cache:   deposits,
					Profile: configuredChainProfile,
				})),
				commands.NewChainsAddMissing(chainregistry.NewMissingChains(seedMissingAddedChains)),
				&commands.WithdrawPreflight{},
				commands.NewPruneActivity(container.MustMake[*activity.Service]()),
				commands.NewEVMCall(commands.EVMCallDeps{
					Wallets: container.MustMake[*repositories.WalletRepository](),
					Signer:  evmCallSigner(),
				}),
				commands.NewWalletsExportKeys(commands.WalletsExportKeysDeps{
					Wallets:   container.MustMake[*repositories.WalletRepository](),
					Addresses: container.MustMake[*repositories.AddressRepository](),
					Chains:    container.MustMake[*repositories.ChainRepository](),
				}),
				&commands.TransactionsBackfillFees{},
			}
		}).
		WithEvents(func() map[contractsevent.Event][]contractsevent.Listener {
			// These events stay registered. Each one only enqueues one job, so the
			// service dispatches that job through the refresh Dispatcher port
			// and no listener remains.
			return map[contractsevent.Event][]contractsevent.Listener{
				&dtos.WalletCreated{}:          {},
				&dtos.WalletActivated{}:        {},
				&dtos.DepositDetected{}:        {},
				&dtos.WithdrawalBroadcasted{}:  {},
				&dtos.WalletRefreshRequested{}: {},
			}
		}).
		WithRules(Rules).
		WithConfig(bootConfig).
		WithMiddleware(func(h contractsconfiguration.Middleware) {
			h.Use(middleware.GlobalChain(requestTimeout())...).
				Recover(middleware.RecoverPanic)
		}).
		WithRouting(providers.RegisterRoutes).
		Create()
}

// bootConfig loads configuration and then installs the redacting log handler.
// WithConfig runs before service providers, which is the last moment a channel
// can be rewritten: the framework caches handlers on first use.
func bootConfig() {
	config.Boot()
	providers.InstallLogRedaction(appfacades.Config(), goravelfacades.App().Json())
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
