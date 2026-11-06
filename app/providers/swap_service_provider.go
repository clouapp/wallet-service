package providers

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/foundation"
	providers "github.com/macrowallets/waas/app/adapters/swap"
	"github.com/macrowallets/waas/app/facades"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/app/services/swapcustody"
	"github.com/macrowallets/waas/app/services/sweep"
	"github.com/macrowallets/waas/app/services/withdraw"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/pkg/types"
)

// swapApprovalInclusionWait is how long an execute request waits for its ERC-20
// approval to be included before funding (below the 30 s request timeout; the
// quote's expiry and the request deadline bound it further).
const swapApprovalInclusionWait = 20 * time.Second

// SwapServiceProvider composes the adapter factory, administrative router and durable orders.
type SwapServiceProvider struct{}

// Register binds each swap dependency once per process.
func (*SwapServiceProvider) Register(app foundation.Application) {
	app.Singleton((*repositories.SwapRepository)(nil), func(foundation.Application) (any, error) { return repositories.NewSwapRepository(nil), nil })
	app.Singleton((*swap.Factory)(nil), func(foundation.Application) (any, error) {
		return swap.NewFactory(providers.NewSwapKit(providers.Deps{}), providers.NewLiFi(providers.Deps{}))
	})
	app.Singleton((*swap.Router)(nil), func(app foundation.Application) (any, error) {
		factory, err := resolve[*swap.Factory](app)
		if err != nil {
			return nil, err
		}
		config, err := resolve[*settings.Service](app)
		if err != nil {
			return nil, err
		}
		return swap.NewRouter(factory, swapPolicy{config}), nil
	})
	app.Singleton((*swap.Service)(nil), func(app foundation.Application) (any, error) {
		router, err := resolve[*swap.Router](app)
		if err != nil {
			return nil, err
		}
		factory, err := resolve[*swap.Factory](app)
		if err != nil {
			return nil, err
		}
		rows, err := resolve[*repositories.SwapRepository](app)
		if err != nil {
			return nil, err
		}
		wallets, err := resolve[*repositories.WalletRepository](app)
		if err != nil {
			return nil, err
		}
		chains, err := resolve[*repositories.ChainRepository](app)
		if err != nil {
			return nil, err
		}
		registry, err := resolve[*chain.Registry](app)
		if err != nil {
			return nil, err
		}
		config, err := resolve[*settings.Service](app)
		if err != nil {
			return nil, err
		}
		withdrawals, err := resolve[*withdraw.Service](app)
		if err != nil {
			return nil, err
		}
		transactions, err := resolve[*repositories.TransactionRepository](app)
		if err != nil {
			return nil, err
		}
		box, err := resolve[*sweep.Box](app)
		if err != nil {
			return nil, err
		}
		signer, ok := box.Service.(sweep.PreparedSigner)
		if !ok {
			return nil, fmt.Errorf("swap custody: prepared signer required")
		}
		accounts, err := resolve[*repositories.AccountRepository](app)
		if err != nil {
			return nil, err
		}
		execution := swapcustody.New(swapcustody.Deps{Authority: withdrawals, Registry: registry, Steps: rows, Transactions: transactions, Signer: signer, Cache: facades.Cache(),
			Tokens: registry, Settler: box.Service, InclusionWait: swapApprovalInclusionWait})
		return swap.NewService(swap.Deps{Router: router, Factory: factory, Store: NewSwapStore(rows), Wallets: swapWallets{wallets}, Catalog: swapCatalog{ChainRepository: chains, Registry: registry}, Policy: swapPolicy{config}, Credentials: swapCredentials{config}, Execution: execution, Verifier: swap.NewDepositVerifier(transactions, rows), Accounts: accounts}), nil
	})
}

// Boot has no eager external calls or database queries.
func (*SwapServiceProvider) Boot(foundation.Application) {}

type swapPolicy struct{ settings *settings.Service }

type swapCredentials struct{ settings *settings.Service }

func (s swapCredentials) Provider(ctx context.Context, provider string) (swap.ProviderConfig, error) {
	return s.settings.SwapProvider(ctx, provider)
}

func (s swapPolicy) Resolve(ctx context.Context, symbol, chain string) (swap.RoutingConfig, error) {
	return s.settings.SwapRouting(ctx, symbol, chain)
}

type swapStore struct{ *repositories.SwapRepository }

// NewSwapStore applies the service error contract to the durable repository.
func NewSwapStore(rows *repositories.SwapRepository) swap.Store { return swapStore{rows} }

func (s swapStore) FindQuote(ctx context.Context, accountID, walletID, id uuid.UUID) (*models.SwapQuote, error) {
	row, err := s.SwapRepository.FindQuote(ctx, accountID, walletID, id)
	return row, swapStoreError(err)
}
func (s swapStore) FindOrder(ctx context.Context, accountID, walletID, id uuid.UUID) (*models.SwapOrder, error) {
	row, err := s.SwapRepository.FindOrder(ctx, accountID, walletID, id)
	return row, swapStoreError(err)
}
func (s swapStore) FindOrderByKey(ctx context.Context, accountID, key uuid.UUID) (*models.SwapOrder, error) {
	row, err := s.SwapRepository.FindOrderByKey(ctx, accountID, key)
	return row, swapStoreError(err)
}
func (s swapStore) CreateOrder(ctx context.Context, row *models.SwapOrder) error {
	return swapStoreError(s.SwapRepository.CreateOrder(ctx, row))
}

// SaveFunding, RecordSource and RecordProgress are compare-and-swap writes: no
// row means another writer already moved the order, a conflict and not a 500.
func (s swapStore) SaveFunding(ctx context.Context, id uuid.UUID, payload string) error {
	return swapTransitionError(s.SwapRepository.SaveFunding(ctx, id, payload))
}
func (s swapStore) RecordSource(ctx context.Context, id uuid.UUID, hash, status string) error {
	return swapTransitionError(s.SwapRepository.RecordSource(ctx, id, hash, status))
}
func (s swapStore) RecordProgress(ctx context.Context, row *models.SwapOrder, previous string) error {
	return swapTransitionError(s.SwapRepository.RecordProgress(ctx, row, previous))
}
func swapTransitionError(err error) error {
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return swap.ErrConflict
	}
	return err
}
func swapStoreError(err error) error {
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return swap.ErrNotFound
	}
	if pgerr.IsUniqueViolation(err) {
		return swap.ErrConflict
	}
	return err
}

type swapWallets struct{ *repositories.WalletRepository }

func (s swapWallets) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	wallet, err := s.WalletRepository.FindByID(ctx, id)
	return wallet, swapStoreError(err)
}

type swapCatalog struct {
	*repositories.ChainRepository
	Registry *chain.Registry
}

func (s swapCatalog) TokensForChain(id string) []types.Token { return s.Registry.TokensForChain(id) }
func (s swapCatalog) ValidateAddress(id, address string) bool {
	adapter, err := s.Registry.Chain(id)
	return err == nil && adapter.ValidateAddress(address)
}
