package swap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/types"
)

// Store is the durable, account-scoped quote and order port.
type Store interface {
	CreateQuote(context.Context, *models.SwapQuote) error
	FindQuote(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*models.SwapQuote, error)
	CreateOrder(context.Context, *models.SwapOrder) error
	FindOrder(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*models.SwapOrder, error)
	FindOrderByKey(context.Context, uuid.UUID, uuid.UUID) (*models.SwapOrder, error)
	ListOrders(context.Context, uuid.UUID, uuid.UUID, int, int) ([]models.SwapOrder, int64, error)
	SaveFunding(context.Context, uuid.UUID, string) error
	RecordSource(context.Context, uuid.UUID, string, string) error
	RecordProgress(context.Context, *models.SwapOrder, string) error
}

// Wallets loads a destination wallet without trusting a body-supplied account.
type Wallets interface {
	FindByID(context.Context, uuid.UUID) (*models.Wallet, error)
}

// Catalog supplies current network metadata and only its active token list.
type Catalog interface {
	FindByID(context.Context, string) (*models.Chain, error)
	TokensForChain(string) []types.Token
	ValidateAddress(string, string) bool
}

// Deps wires swap orchestration without exposing repositories or HTTP clients.
type Deps struct {
	Router      *Router
	Factory     *Factory
	Store       Store
	Wallets     Wallets
	Catalog     Catalog
	Policy      Policy
	Credentials Credentials
	Execution   Execution
	Verifier    Verifier
	// Accounts lets the background reconciler load an order's account.
	Accounts Accounts
}

// Service coordinates quotes and their immutable, account-scoped orders.
type Service struct{ deps Deps }

// NewService creates the swap application service.
func NewService(deps Deps) *Service { return &Service{deps: deps} }

// QuoteInput is the requested exact-input conversion in human decimal units.
type QuoteInput struct {
	FromAsset string
	ToAsset   string
	Amount    string
}

// CreateQuote resolves assets from the custody catalog before contacting a provider.
func (s *Service) CreateQuote(ctx context.Context, account *models.Account, source *models.Wallet, destinationID uuid.UUID, in QuoteInput) (*models.SwapQuote, error) {
	if s == nil || s.deps.Router == nil || s.deps.Store == nil || s.deps.Wallets == nil || s.deps.Catalog == nil {
		return nil, ErrDisabled
	}
	if !ownsWallet(account, source) {
		return nil, ErrNotFound
	}
	destination, err := s.deps.Wallets.FindByID(ctx, destinationID)
	if err != nil {
		return nil, err
	}
	if !ownsWallet(account, destination) {
		return nil, ErrNotFound
	}
	from, err := s.asset(ctx, account, source, in.FromAsset)
	if err != nil {
		return nil, err
	}
	to, err := s.asset(ctx, account, destination, in.ToAsset)
	if err != nil {
		return nil, err
	}
	units, err := amount.DecimalToBaseUnits(in.Amount, from.Decimals)
	if err != nil || units.Sign() <= 0 {
		return nil, ErrInvalidRequest
	}
	quote, err := s.deps.Router.Quote(ctx, QuoteRequest{From: from, To: to, FromAddress: source.DepositAddress.Address, ToAddress: destination.DepositAddress.Address, AmountRaw: units.String()})
	if err != nil {
		return nil, err
	}
	row, err := quoteRow(account.ID, source.ID, destination.ID, quote)
	if err != nil {
		return nil, err
	}
	if err := s.deps.Store.CreateQuote(ctx, row); err != nil {
		return nil, err
	}
	return row, nil
}

func ownsWallet(account *models.Account, wallet *models.Wallet) bool {
	return account != nil && wallet != nil && wallet.AccountID != nil && *wallet.AccountID == account.ID
}

func (s *Service) asset(ctx context.Context, account *models.Account, wallet *models.Wallet, symbol string) (Asset, error) {
	if wallet.DepositAddress == nil || wallet.DepositAddress.Address == "" {
		return Asset{}, ErrUnsupported
	}
	chain, err := s.deps.Catalog.FindByID(ctx, wallet.Chain)
	if err != nil {
		return Asset{}, err
	}
	if chain.Status != "active" || chain.IsTestnet != (account.Environment == models.EnvironmentTest) {
		return Asset{}, ErrUnsupported
	}
	if !s.deps.Catalog.ValidateAddress(wallet.Chain, wallet.DepositAddress.Address) {
		return Asset{}, ErrUnsupported
	}
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	native := strings.ToUpper(chain.NativeSymbol)
	if symbol == "" {
		symbol = native
	}
	if !SupportedSymbol(symbol) {
		return Asset{}, ErrUnsupported
	}
	asset := Asset{Symbol: symbol, Chain: chain.ID, Decimals: chain.NativeDecimals, Native: symbol == native, Testnet: chain.IsTestnet}
	if chain.NetworkID != nil {
		asset.NetworkID = *chain.NetworkID
	}
	if asset.Native {
		return asset, nil
	}
	for _, token := range s.deps.Catalog.TokensForChain(chain.ID) {
		if strings.EqualFold(token.Symbol, symbol) {
			asset.Contract = token.Contract
			asset.Decimals = int(token.Decimals)
			return asset, nil
		}
	}
	return Asset{}, ErrUnsupported
}

func quoteRow(accountID, sourceID, destinationID uuid.UUID, quote *Quote) (*models.SwapQuote, error) {
	request, err := json.Marshal(quote.Request)
	if err != nil {
		return nil, err
	}
	fees, err := json.Marshal(quote.Fees)
	if err != nil {
		return nil, err
	}
	attempts, err := json.Marshal(quote.Attempts)
	if err != nil {
		return nil, err
	}
	return &models.SwapQuote{ID: quote.ID, AccountID: accountID, SourceWalletID: sourceID, DestinationWalletID: destinationID, Provider: quote.Provider, RouteID: quote.RouteID,
		RequestPayload: string(request), ExpectedRaw: quote.ExpectedRaw, MinimumRaw: quote.MinimumRaw, FeesPayload: string(fees), AttemptsPayload: string(attempts), RoutePayload: string(quote.Payload), ExpiresAt: quote.ExpiresAt, EstimatedSeconds: quote.EstimatedSeconds}, nil
}

// DecodeQuote restores a stored route without re-quoting or changing its provider.
func DecodeQuote(row *models.SwapQuote) (*Quote, error) {
	if row == nil {
		return nil, ErrNotFound
	}
	quote := &Quote{ID: row.ID, Provider: row.Provider, RouteID: row.RouteID, ExpectedRaw: row.ExpectedRaw, MinimumRaw: row.MinimumRaw, ExpiresAt: row.ExpiresAt, EstimatedSeconds: row.EstimatedSeconds, Payload: json.RawMessage(row.RoutePayload)}
	if err := json.Unmarshal([]byte(row.RequestPayload), &quote.Request); err != nil {
		return nil, fmt.Errorf("decode stored swap request: %w", err)
	}
	if err := json.Unmarshal([]byte(row.FeesPayload), &quote.Fees); err != nil {
		return nil, fmt.Errorf("decode stored swap fees: %w", err)
	}
	if err := json.Unmarshal([]byte(row.AttemptsPayload), &quote.Attempts); err != nil {
		return nil, fmt.Errorf("decode stored swap attempts: %w", err)
	}
	return quote, nil
}

// List returns only the authenticated account's orders from the scoped wallet.
func (s *Service) List(ctx context.Context, account *models.Account, wallet *models.Wallet, limit, offset int) ([]models.SwapOrder, int64, error) {
	if !ownsWallet(account, wallet) {
		return nil, 0, ErrNotFound
	}
	return s.deps.Store.ListOrders(ctx, account.ID, wallet.ID, limit, offset)
}

// Find returns an order only inside the authenticated account and source wallet.
func (s *Service) Find(ctx context.Context, account *models.Account, wallet *models.Wallet, id uuid.UUID) (*models.SwapOrder, error) {
	if !ownsWallet(account, wallet) {
		return nil, ErrNotFound
	}
	return s.deps.Store.FindOrder(ctx, account.ID, wallet.ID, id)
}

// Recover finds an ambiguous request using its original account-scoped key.
func (s *Service) Recover(ctx context.Context, account *models.Account, wallet *models.Wallet, key uuid.UUID) (*models.SwapOrder, error) {
	if !ownsWallet(account, wallet) {
		return nil, ErrNotFound
	}
	order, err := s.deps.Store.FindOrderByKey(ctx, account.ID, key)
	if err != nil {
		return nil, err
	}
	if order.SourceWalletID != wallet.ID {
		return nil, ErrNotFound
	}
	return s.Refresh(ctx, account, wallet, order.ID)
}
