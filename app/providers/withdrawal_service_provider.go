package providers

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"

	sweepsecrets "github.com/macrowallets/waas/app/adapters/secretsmanager"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	authsvc "github.com/macrowallets/waas/app/services/auth"
	chainpkg "github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/features"
	"github.com/macrowallets/waas/app/services/feeestimate"
	mpc "github.com/macrowallets/waas/app/services/mpc"
	"github.com/macrowallets/waas/app/services/price"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/app/services/withdrawalevents"
	"github.com/macrowallets/waas/app/services/withdrawalrecords"
)

// WithdrawalServiceProvider binds the transaction and withdrawal repositories
// by type, the withdrawal records over them, the sweep service that signs and
// broadcasts, the fee estimate built on it, the withdrawal service, and the
// publisher of withdrawal webhooks.
type WithdrawalServiceProvider struct{}

func (p *WithdrawalServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.TransactionRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewTransactionRepository(nil), nil
	})
	app.Singleton((*repositories.WithdrawalRepository)(nil), func(foundation.Application) (any, error) {
		return repositories.NewWithdrawalRepository(nil), nil
	})
	app.Singleton((*walletrecords.Transactions)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.TransactionRepository](app)
		if err != nil {
			return nil, err
		}
		return walletrecords.NewTransactions(store), nil
	})
	app.Singleton((*sweep.Box)(nil), func(app foundation.Application) (any, error) {
		service, err := newSweepService(app)
		if err != nil {
			return nil, err
		}
		return &sweep.Box{Service: service}, nil
	})
	app.Singleton((*feeestimate.Service)(nil), func(app foundation.Application) (any, error) {
		return newFeeEstimate(app)
	})
	app.Singleton((*withdraw.Service)(nil), func(app foundation.Application) (any, error) {
		return newWithdrawalService(app)
	})
	app.Singleton((*withdrawalevents.Publisher)(nil), func(app foundation.Application) (any, error) {
		return newWithdrawalEvents(app)
	})
	app.Singleton((*withdrawalrecords.Records)(nil), func(app foundation.Application) (any, error) {
		store, err := resolve[*repositories.WithdrawalRepository](app)
		if err != nil {
			return nil, err
		}
		activityLog, err := resolve[*repositories.AccountActivityRepository](app)
		if err != nil {
			return nil, err
		}
		wallets, err := resolve[*walletrecords.Wallets](app)
		if err != nil {
			return nil, err
		}
		transactions, err := resolve[*walletrecords.Transactions](app)
		if err != nil {
			return nil, err
		}
		return withdrawalrecords.NewRecords(withdrawalrecords.Deps{
			Store:        store,
			Activity:     activityLog,
			Wallets:      wallets,
			Transactions: transactions,
		}), nil
	})
}

func (p *WithdrawalServiceProvider) Boot(foundation.Application) {}

// newSweepService signs and broadcasts consolidations and withdrawals: MPC
// signing with share B from Secrets Manager, the account sweep limits from
// the settings store, and the sweep-enabled flag.
func newSweepService(app foundation.Application) (sweep.Service, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	mpcService, err := resolve[*mpc.TSSService](app)
	if err != nil {
		return nil, err
	}
	secrets, err := resolve[*sweepsecrets.SDKClient](app)
	if err != nil {
		return nil, err
	}
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
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
	chains, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return nil, err
	}
	accountSettings, err := resolve[*settings.Service](app)
	if err != nil {
		return nil, fmt.Errorf("vault: account settings: %w", err)
	}
	flags, err := resolve[*features.Service](app)
	if err != nil {
		return nil, fmt.Errorf("vault: feature flags: %w", err)
	}
	prices, err := resolve[*price.Service](app)
	if err != nil {
		return nil, err
	}
	return sweep.NewService(sweep.Deps{
		Registry:     registry,
		MPC:          mpcService,
		Secrets:      sweepsecrets.New(secrets),
		Cache:        facades.Cache(),
		Webhook:      webhookService,
		Wallets:      wallets,
		Addresses:    addresses,
		Transactions: transactions,
		SweepLimits:  accountSettings.EffectiveSweepLimits,
		Chains:       chains,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagSweepEnabled, features.CodeSweepPaused)
		},
		GasDefaults: nil,
		TokenPricer: prices,
	}), nil
}

// newWithdrawalService creates and executes withdrawals behind the
// withdrawals-enabled flag. The USD quote, the create path (the second
// factor and the withdrawal rows) and the submit path (the row outcome and the
// withdrawal webhooks) are wired here, before anyone resolves it.
func newWithdrawalService(app foundation.Application) (*withdraw.Service, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	mpcService, err := resolve[*mpc.TSSService](app)
	if err != nil {
		return nil, err
	}
	transactions, err := resolve[*repositories.TransactionRepository](app)
	if err != nil {
		return nil, err
	}
	wallets, err := resolve[*repositories.WalletRepository](app)
	if err != nil {
		return nil, err
	}
	addresses, err := resolve[*repositories.AddressRepository](app)
	if err != nil {
		return nil, err
	}
	box, err := resolve[*sweep.Box](app)
	if err != nil {
		return nil, err
	}
	flags, err := resolve[*features.Service](app)
	if err != nil {
		return nil, fmt.Errorf("vault: feature flags: %w", err)
	}
	prices, err := resolve[*price.Service](app)
	if err != nil {
		return nil, err
	}
	users, err := resolve[*repositories.UserRepository](app)
	if err != nil {
		return nil, err
	}
	verifier, err := resolve[*authsvc.SecondFactorVerifier](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal create: %w", err)
	}
	withdrawalRows, err := resolve[*withdrawalrecords.Records](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal create: %w", err)
	}
	chains, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return nil, err
	}
	events, err := resolve[*withdrawalevents.Publisher](app)
	if err != nil {
		return nil, fmt.Errorf("vault: withdrawal events: %w", err)
	}
	service := withdraw.NewService(withdraw.Deps{
		Registry:     registry,
		Webhook:      webhookService,
		MPC:          mpcService,
		Cache:        facades.Cache(),
		Transactions: transactions,
		Wallets:      wallets,
		Addresses:    addresses,
		Sweep:        box.Service,
		Flags: func(ctx context.Context, accountID uuid.UUID) error {
			return flags.Gate(ctx, accountID, features.FlagWithdrawalsEnabled, features.CodeWithdrawalsPaused)
		},
	})
	service.UseUSDQuote(prices)
	service.UseCreate(users, verifier, withdrawalRows, chains)
	service.UseSubmit(withdrawalRows, events)
	return service, nil
}

// newWithdrawalEvents publishes the withdrawal webhooks with the asset
// decimals the registry and the chain rows give.
func newWithdrawalEvents(app foundation.Application) (*withdrawalevents.Publisher, error) {
	webhookService, err := resolve[*webhook.Service](app)
	if err != nil {
		return nil, err
	}
	withdrawals, err := resolve[*repositories.WithdrawalRepository](app)
	if err != nil {
		return nil, err
	}
	transactions, err := resolve[*repositories.TransactionRepository](app)
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
	return withdrawalevents.NewPublisher(withdrawalevents.PublisherDeps{
		Enqueuer:     webhookService,
		Withdrawals:  withdrawals,
		Transactions: transactions,
		Wallets:      wallets,
		Decimals:     decimals,
	}), nil
}

// newAssetDecimals reads an asset's decimals from the registry tokens and
// the chain rows. It holds no state, so each publisher takes its own.
func newAssetDecimals(app foundation.Application) (withdrawalevents.RegistryDecimals, error) {
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return withdrawalevents.RegistryDecimals{}, err
	}
	chains, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return withdrawalevents.RegistryDecimals{}, err
	}
	return withdrawalevents.NewRegistryDecimals(withdrawalevents.RegistryDecimalsDeps{
		Registry: registry,
		Chains:   chains,
	}), nil
}

// newFeeEstimate quotes a withdrawal fee through the sweep service and caches
// it for fee_estimate.cache_ttl_seconds.
func newFeeEstimate(app foundation.Application) (*feeestimate.Service, error) {
	box, err := resolve[*sweep.Box](app)
	if err != nil {
		return nil, err
	}
	quoter, ok := box.Service.(sweep.FeeQuoter)
	if !ok || quoter == nil {
		return nil, fmt.Errorf("vault: sweep service cannot quote withdrawal fees")
	}
	registry, err := resolve[*chainpkg.Registry](app)
	if err != nil {
		return nil, err
	}
	chains, err := resolve[*repositories.ChainRepository](app)
	if err != nil {
		return nil, err
	}
	seconds := facades.Config().GetInt("fee_estimate.cache_ttl_seconds")
	if seconds < 0 {
		seconds = int(feeestimate.DefaultCacheTTL / time.Second)
	}
	return feeestimate.NewService(feeestimate.Deps{
		Quoter:   quoter,
		Registry: registry,
		Chains:   feeEstimateChains{repo: chains},
		Cache:    facades.Cache(),
		CacheTTL: time.Duration(seconds) * time.Second,
		Now:      time.Now,
	})
}

// feeEstimateChains adapts the context-aware chain repository to the estimator catalog.
type feeEstimateChains struct {
	repo *repositories.ChainRepository
}

func (c feeEstimateChains) FindByID(id string) (*models.Chain, error) {
	if c.repo == nil {
		return nil, fmt.Errorf("fee estimate: chain repository is required")
	}
	if id == "" {
		return nil, fmt.Errorf("fee estimate: chain id is required")
	}
	return c.repo.FindByID(context.Background(), id)
}
