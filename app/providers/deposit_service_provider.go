package providers

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/goravel/framework/contracts/foundation"
	"github.com/redis/go-redis/v9"

	blockstreamtip "github.com/macrowallets/waas/app/adapters/blockheight/blockstream"
	etherscantip "github.com/macrowallets/waas/app/adapters/blockheight/etherscan"
	mempooltip "github.com/macrowallets/waas/app/adapters/blockheight/mempool"
	solanatip "github.com/macrowallets/waas/app/adapters/blockheight/solana"
	"github.com/macrowallets/waas/app/adapters/redis/addressset"
	redispending "github.com/macrowallets/waas/app/adapters/redis/pending"
	"github.com/macrowallets/waas/app/adapters/redis/scanner"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/blockheight"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/chainregistry"
	"github.com/macrowallets/waas/app/services/deposit"
	"github.com/macrowallets/waas/app/services/deposit/pending"
	"github.com/macrowallets/waas/app/services/depositevents"
	"github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/refresh"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
)

// DepositServiceProvider binds the deposit scanner with its checkpoint
// repository, the inbound webhook ingest that records provider-pushed deposits,
// and the publisher of deposit webhooks both of them use.
type DepositServiceProvider struct{}

func (p *DepositServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.DepositScanCheckpointRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewDepositScanCheckpointRepository(nil), nil
	})
	app.Singleton((*depositevents.Publisher)(nil), func(app foundation.Application) (any, error) {
		return newDepositEvents(app)
	})
	app.Singleton((*deposit.Service)(nil), func(app foundation.Application) (any, error) {
		return newDepositService(app)
	})
	app.Singleton((*ingest.Service)(nil), func(app foundation.Application) (any, error) {
		return newIngestService(app)
	})
}

func (p *DepositServiceProvider) Boot(foundation.Application) {}

func newDepositEvents(app foundation.Application) (*depositevents.Publisher, error) {
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	decimals, err := newAssetDecimals(app)
	if err != nil {
		return nil, err
	}
	return depositevents.NewPublisher(depositevents.PublisherDeps{
		Enqueuer: webhookService,
		Wallets:  wallets,
		Decimals: decimals,
	}), nil
}

// newDepositService scans blocks for deposits and tracks confirmations. The
// checkpoint lives in the database with a Redis copy, and each chain's scan
// holds a lock in the process cache. The setters run here, before anyone
// resolves the service: the withdrawal and deposit webhooks, the scan window
// from deposit_scan, the failure policy, the pending-block store and the
// balance refresher.
func newDepositService(app foundation.Application) (*deposit.Service, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	// The registry is loaded above, so the networks are the ones it installed.
	registryService, err := resolve[*chainregistry.ChainRegistryService](app)
	if err != nil {
		return nil, fmt.Errorf("vault: chain registry: %w", err)
	}
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	transactions, err := resolve[*repositories.TransactionRepository](app)
	if err != nil {
		return nil, err
	}
	checkpoints, err := resolve[*repositories.DepositScanCheckpointRepository](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, fmt.Errorf("vault: account settings: %w", err)
	}
	withdrawalEvents, err := resolve[*withdrawalevents.Publisher](app)
	if err != nil {
		return nil, err
	}
	withdrawalRows, err := resolve[*repositories.WithdrawalRepository](app)
	if err != nil {
		return nil, err
	}
	depositEvents, err := resolve[*depositevents.Publisher](app)
	if err != nil {
		return nil, err
	}
	walletRefresher, err := resolve[*refresh.WalletRefresher](app)
	if err != nil {
		return nil, err
	}
	redisClient, err := facades.Redis()
	if err != nil {
		return nil, fmt.Errorf("vault: redis: %w", err)
	}

	etherscanKey := func(ctx context.Context) string {
		envKey := facades.Config().GetString("vault.webhooks.etherscan_api_key")
		return accountSettings.EtherscanKeyForHeight(ctx, envKey)
	}
	service := deposit.NewService(deposit.Deps{
		Store:        scanner.New(redisClient),
		Checkpoints:  checkpoints,
		Cache:        facades.Cache(),
		Registry:     registry,
		Webhook:      webhookService,
		Addresses:    addresses,
		Transactions: transactions,
		BlockHeightProviders: blockheight.NewProviders(blockheight.ProvidersDeps{
			Key:            etherscanKey,
			NetworkByChain: registryService.Networks(),
			Etherscan:      etherscantip.New(blockheight.EtherscanDeps{KeyAtUse: etherscanKey}),
			Blockstream:    blockstreamtip.New(),
			Testnet4:       mempooltip.New(),
			Solana:         solanatip.New(),
		}),
	})
	service.SetWithdrawalConfirmations(withdrawalEvents)
	service.SetDepositEvents(depositEvents)
	service.SetStaleOutboundAfter(time.Duration(facades.Config().GetInt("vault.withdrawals.stale_after_hours")) * time.Hour)
	service.SetWithdrawalReconciliation(withdrawalRows, transactions)
	// Each ScanLatestBlocks call reads deposit_scan and runs
	// deposit.ScanOptionsFromSettings. A stored value overrides DEPOSIT_SCAN_*.
	// A missing row, an invalid value, or a failed read keeps the environment
	// window and the scan continues.
	service.SetScanOptionSource(func(ctx context.Context) (deposit.ScanOptions, error) {
		envBatch := facades.Config().GetInt("vault.deposit_scan.batch_blocks")
		envCatchUp := facades.Config().GetInt("vault.deposit_scan.catch_up_blocks")
		envConcurrency := facades.Config().GetInt("vault.deposit_scan.concurrency")
		stored, readErr := accountSettings.EffectiveDepositScan(ctx)
		return deposit.ScanOptionsForRun(
			envBatch, envCatchUp, envConcurrency,
			stored.BatchBlocks, stored.CatchUpBlocks, stored.Concurrency,
			readErr,
		)
	})
	// DEPOSIT_SCAN_*_<CHAIN> lay one chain's window and parallelism over the global
	// window above, read at each cycle.
	service.SetChainScanOverrides(chainScanOverrides(facades.Config()))
	failurePolicy, err := deposit.FailurePolicyFromSettings(
		facades.Config().GetInt("vault.deposit_scan.retry_attempts"),
		facades.Config().GetInt("vault.deposit_scan.retry_delay_ms"),
		facades.Config().GetInt("vault.deposit_scan.pending_retry_seconds"),
		facades.Config().GetInt("vault.deposit_scan.pending_retry_max_seconds"),
		facades.Config().GetInt("vault.deposit_scan.max_new_pending_per_cycle"),
	)
	if err != nil {
		return nil, fmt.Errorf("vault: deposit failure policy: %w", err)
	}
	if err := service.SetFailurePolicy(failurePolicy); err != nil {
		return nil, fmt.Errorf("vault: deposit failure policy: %w", err)
	}
	if pendingDeposits := buildPendingDepositStore(redisClient, facades.Config().GetString("vault.deposit_scan.pending_dir")); pendingDeposits != nil {
		service.SetPendingStore(pendingDeposits)
	}
	service.SetBalanceRefresher(walletRefresher)
	return service, nil
}

// configIntReader is the part of the framework configuration the scan overrides read.
type configIntReader interface {
	GetInt(path string, defaultValue ...int) int
}

// chainScanOverrides reads vault.deposit_scan.chains.<chain> (DEPOSIT_SCAN_BATCH_BLOCKS_,
// _CATCH_UP_BLOCKS_ and _CONCURRENCY_<CHAIN>); a chain without an entry reads zeros and
// keeps the global window.
func chainScanOverrides(cfg configIntReader) func(chainID string) deposit.ChainScanOverride {
	return func(chainID string) deposit.ChainScanOverride {
		key := "vault.deposit_scan.chains." + chainID + "."
		return deposit.ChainScanOverride{
			BatchBlocks:   cfg.GetInt(key + "batch_blocks"),
			CatchUpBlocks: cfg.GetInt(key + "catch_up_blocks"),
			Concurrency:   cfg.GetInt(key + "concurrency"),
		}
	}
}

// newIngestService records the deposits a chain-data provider pushes to the
// inbound webhook routes.
func newIngestService(app foundation.Application) (*ingest.Service, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	transactions, err := resolve[*repositories.TransactionRepository](app)
	if err != nil {
		return nil, err
	}
	depositEvents, err := resolve[*depositevents.Publisher](app)
	if err != nil {
		return nil, err
	}
	redisClient, err := facades.Redis()
	if err != nil {
		return nil, fmt.Errorf("vault: redis: %w", err)
	}
	service := ingest.NewService(ingest.Deps{
		Addresses:    addressset.New(redisClient),
		Registry:     registry,
		Webhook:      webhookService,
		AddressRepo:  addresses,
		Transactions: transactions,
	})
	service.SetDepositEvents(depositEvents)
	return service, nil
}

// buildPendingDepositStore keeps failed deposit blocks in Redis and in a local
// append-only file; either one is enough, and with neither the scanner stops before a
// failing block instead of skipping it.
func buildPendingDepositStore(rdb redis.UniversalClient, dir string) pending.Store {
	var redisStore pending.Store
	if rdb != nil {
		store, err := redispending.NewRedisStore(redispending.RedisStoreDeps{Redis: rdb, KeyPrefix: redispending.DefaultRedisKeyPrefix})
		if err != nil {
			slog.Error("vault: pending deposit redis store unavailable", "error", err)
		} else {
			redisStore = store
		}
	}
	if dir == "" {
		dir = defaultPendingDepositDir()
	}
	fileStore, err := pending.NewFileStore(pending.FileStoreDeps{Dir: dir})
	if err != nil {
		slog.Error("vault: pending deposit file store unavailable", "dir", dir, "error", err)
		fileStore = nil
	}
	store, err := pending.NewDurableStore(pending.DurableStoreDeps{Redis: redisStore, File: fileStore})
	if err != nil {
		slog.Error("vault: no pending deposit store; a block that keeps failing stops the scan", "error", err)
		return nil
	}
	slog.Info("vault: pending deposit store ready", "redis", redisStore != nil, "file_dir", dir, "file", fileStore != nil)
	return store
}

func defaultPendingDepositDir() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".local", "state", "macro-wallets", "deposit-pending")
	}
	return filepath.Join(os.TempDir(), "macro-wallets", "deposit-pending")
}
