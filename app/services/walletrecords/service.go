package walletrecords

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/numeric"
)

func requireStore(ctx context.Context, store any, op, what string) error {
	if ctx == nil {
		return fmt.Errorf("%s: context is required", op)
	}
	if store == nil {
		return fmt.Errorf("%s: %s repository is required", op, what)
	}
	return nil
}

// WalletStore is the wallet persistence dashboard and middleware handlers use.
type WalletStore interface {
	PaginateByAccount(ctx context.Context, accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error)
	PaginateByAccountAndMember(ctx context.Context, accountID, userID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error)
	FindByIDAndAccount(ctx context.Context, id, accountID uuid.UUID) (*models.Wallet, error)
	SetFeeRateMin(ctx context.Context, id uuid.UUID, value int) error
	SetFeeRateMax(ctx context.Context, id uuid.UUID, value int) error
	SetFeeMultiplier(ctx context.Context, id uuid.UUID, value numeric.NullDecimal) error
	UpdateSettings(ctx context.Context, id uuid.UUID, columns map[string]any) error
	SetRequiredApprovals(ctx context.Context, id uuid.UUID, value int) error
	SetFrozenUntil(ctx context.Context, id uuid.UUID, until time.Time) error
	SetStatus(ctx context.Context, id uuid.UUID, status string) error
	SetLabel(ctx context.Context, id uuid.UUID, label string) error
}

// Wallets reads and updates wallet rows.
type Wallets struct{ store WalletStore }

// NewWallets builds the wallet record service.
func NewWallets(store WalletStore) *Wallets { return &Wallets{store: store} }

func (s *Wallets) PaginateByAccount(ctx context.Context, accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	if err := requireStore(ctx, s.storeOrNil(), "list wallets", "wallets"); err != nil {
		return nil, 0, err
	}
	return s.store.PaginateByAccount(ctx, accountID, chain, limit, offset)
}

func (s *Wallets) PaginateByAccountAndMember(ctx context.Context, accountID, userID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	if userID == uuid.Nil {
		return nil, 0, fmt.Errorf("list wallets: user is required")
	}
	if err := requireStore(ctx, s.storeOrNil(), "list wallets", "wallets"); err != nil {
		return nil, 0, err
	}
	return s.store.PaginateByAccountAndMember(ctx, accountID, userID, chain, limit, offset)
}

func (s *Wallets) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	if err := requireStore(ctx, s.storeOrNil(), "find wallet", "wallets"); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, id)
}

func (s *Wallets) FindByIDAndAccount(ctx context.Context, id, accountID uuid.UUID) (*models.Wallet, error) {
	if err := requireStore(ctx, s.storeOrNil(), "find wallet", "wallets"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndAccount(ctx, id, accountID)
}

func (s *Wallets) SetFeeRateMin(ctx context.Context, id uuid.UUID, value int) error {
	if err := requireStore(ctx, s.storeOrNil(), "set fee rate min", "wallets"); err != nil {
		return err
	}
	return s.store.SetFeeRateMin(ctx, id, value)
}

func (s *Wallets) SetFeeRateMax(ctx context.Context, id uuid.UUID, value int) error {
	if err := requireStore(ctx, s.storeOrNil(), "set fee rate max", "wallets"); err != nil {
		return err
	}
	return s.store.SetFeeRateMax(ctx, id, value)
}

func (s *Wallets) SetFeeMultiplier(ctx context.Context, id uuid.UUID, value numeric.NullDecimal) error {
	if err := requireStore(ctx, s.storeOrNil(), "set fee multiplier", "wallets"); err != nil {
		return err
	}
	return s.store.SetFeeMultiplier(ctx, id, value)
}

func (s *Wallets) UpdateSettings(ctx context.Context, id uuid.UUID, columns map[string]any) error {
	if err := requireStore(ctx, s.storeOrNil(), "update wallet settings", "wallets"); err != nil {
		return err
	}
	if len(columns) == 0 {
		return fmt.Errorf("update wallet settings: no columns to write")
	}
	return s.store.UpdateSettings(ctx, id, columns)
}

func (s *Wallets) SetRequiredApprovals(ctx context.Context, id uuid.UUID, value int) error {
	if err := requireStore(ctx, s.storeOrNil(), "set required approvals", "wallets"); err != nil {
		return err
	}
	return s.store.SetRequiredApprovals(ctx, id, value)
}

func (s *Wallets) SetFrozenUntil(ctx context.Context, id uuid.UUID, until time.Time) error {
	if err := requireStore(ctx, s.storeOrNil(), "set frozen until", "wallets"); err != nil {
		return err
	}
	return s.store.SetFrozenUntil(ctx, id, until)
}

func (s *Wallets) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	if err := requireStore(ctx, s.storeOrNil(), "set wallet status", "wallets"); err != nil {
		return err
	}
	return s.store.SetStatus(ctx, id, status)
}

func (s *Wallets) SetLabel(ctx context.Context, id uuid.UUID, label string) error {
	if err := requireStore(ctx, s.storeOrNil(), "set wallet label", "wallets"); err != nil {
		return err
	}
	return s.store.SetLabel(ctx, id, label)
}

func (s *Wallets) storeOrNil() any {
	if s == nil {
		return nil
	}
	return s.store
}

// BalanceStore lists stored asset balances.
type BalanceStore interface {
	ListByWallet(ctx context.Context, walletID uuid.UUID) ([]models.WalletAssetBalance, error)
	ListByWallets(ctx context.Context, walletIDs []uuid.UUID) ([]models.WalletAssetBalance, error)
}

// Balances reads wallet asset balances.
type Balances struct{ store BalanceStore }

// NewBalances builds the balance record service.
func NewBalances(store BalanceStore) *Balances { return &Balances{store: store} }

func (s *Balances) ListByWallet(ctx context.Context, walletID uuid.UUID) ([]models.WalletAssetBalance, error) {
	if err := requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), "list balances", "balances"); err != nil {
		return nil, err
	}
	return s.store.ListByWallet(ctx, walletID)
}

func (s *Balances) ListByWallets(ctx context.Context, walletIDs []uuid.UUID) ([]models.WalletAssetBalance, error) {
	if err := requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), "list balances", "balances"); err != nil {
		return nil, err
	}
	return s.store.ListByWallets(ctx, walletIDs)
}

func storeOf[T any](value *T, store func() any) any {
	if value == nil {
		return nil
	}
	return store()
}

// UTXOStore lists spendable outputs.
type UTXOStore interface {
	ListSpendable(ctx context.Context, walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error)
}

// UTXOs reads spendable outputs.
type UTXOs struct{ store UTXOStore }

// NewUTXOs builds the UTXO record service.
func NewUTXOs(store UTXOStore) *UTXOs { return &UTXOs{store: store} }

func (s *UTXOs) ListSpendable(ctx context.Context, walletID uuid.UUID, chainID string) ([]models.WalletUTXO, error) {
	if err := requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), "list utxos", "utxos"); err != nil {
		return nil, err
	}
	return s.store.ListSpendable(ctx, walletID, chainID)
}

// MemberStore is the wallet membership persistence.
type MemberStore interface {
	FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WalletUser, error)
	FindByWalletAndUser(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error)
	FindByWalletAndUserIncludeDeleted(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error)
	Restore(ctx context.Context, id uuid.UUID) error
	SetRoles(ctx context.Context, id uuid.UUID, roles string) error
	Create(ctx context.Context, member *models.WalletUser) error
	SoftDelete(ctx context.Context, walletID, userID uuid.UUID) error
}

// Members reads and writes wallet memberships.
type Members struct{ store MemberStore }

// NewMembers builds the wallet member service.
func NewMembers(store MemberStore) *Members { return &Members{store: store} }

func (s *Members) ready(ctx context.Context, op string) error {
	return requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), op, "wallet users")
}

func (s *Members) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WalletUser, error) {
	if err := s.ready(ctx, "list wallet users"); err != nil {
		return nil, err
	}
	return s.store.FindByWalletID(ctx, walletID)
}

func (s *Members) FindByWalletAndUser(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error) {
	if err := s.ready(ctx, "find wallet user"); err != nil {
		return nil, err
	}
	return s.store.FindByWalletAndUser(ctx, walletID, userID)
}

func (s *Members) FindByWalletAndUserIncludeDeleted(ctx context.Context, walletID, userID uuid.UUID) (*models.WalletUser, error) {
	if err := s.ready(ctx, "find wallet user"); err != nil {
		return nil, err
	}
	return s.store.FindByWalletAndUserIncludeDeleted(ctx, walletID, userID)
}

func (s *Members) Restore(ctx context.Context, id uuid.UUID) error {
	if err := s.ready(ctx, "restore wallet user"); err != nil {
		return err
	}
	return s.store.Restore(ctx, id)
}

func (s *Members) SetRoles(ctx context.Context, id uuid.UUID, roles string) error {
	if err := s.ready(ctx, "set wallet user roles"); err != nil {
		return err
	}
	return s.store.SetRoles(ctx, id, roles)
}

func (s *Members) Create(ctx context.Context, member *models.WalletUser) error {
	if err := s.ready(ctx, "create wallet user"); err != nil {
		return err
	}
	return s.store.Create(ctx, member)
}

func (s *Members) SoftDelete(ctx context.Context, walletID, userID uuid.UUID) error {
	if err := s.ready(ctx, "remove wallet user"); err != nil {
		return err
	}
	return s.store.SoftDelete(ctx, walletID, userID)
}

// WhitelistStore is the address whitelist persistence.
type WhitelistStore interface {
	PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.WhitelistEntry, int64, error)
	Create(ctx context.Context, entry *models.WhitelistEntry) error
	FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WhitelistEntry, error)
	Delete(ctx context.Context, entry *models.WhitelistEntry) error
}

// Whitelist reads and writes whitelist entries.
type Whitelist struct{ store WhitelistStore }

// NewWhitelist builds the whitelist service.
func NewWhitelist(store WhitelistStore) *Whitelist { return &Whitelist{store: store} }

func (s *Whitelist) ready(ctx context.Context, op string) error {
	return requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), op, "whitelist")
}

func (s *Whitelist) PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.WhitelistEntry, int64, error) {
	if err := s.ready(ctx, "list whitelist"); err != nil {
		return nil, 0, err
	}
	return s.store.PaginateByWalletID(ctx, walletID, limit, offset)
}

func (s *Whitelist) Create(ctx context.Context, entry *models.WhitelistEntry) error {
	if err := s.ready(ctx, "add whitelist entry"); err != nil {
		return err
	}
	return s.store.Create(ctx, entry)
}

func (s *Whitelist) FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WhitelistEntry, error) {
	if err := s.ready(ctx, "find whitelist entry"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndWallet(ctx, id, walletID)
}

func (s *Whitelist) Delete(ctx context.Context, entry *models.WhitelistEntry) error {
	if err := s.ready(ctx, "delete whitelist entry"); err != nil {
		return err
	}
	return s.store.Delete(ctx, entry)
}

// WebhookStore is the per-wallet webhook config persistence.
type WebhookStore interface {
	FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WebhookConfig, error)
	Create(ctx context.Context, cfg *models.WebhookConfig) error
	FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WebhookConfig, error)
	Delete(ctx context.Context, cfg *models.WebhookConfig) error
}

// Webhooks reads and writes wallet webhook configs.
type Webhooks struct{ store WebhookStore }

// NewWebhooks builds the wallet webhook config service.
func NewWebhooks(store WebhookStore) *Webhooks { return &Webhooks{store: store} }

func (s *Webhooks) ready(ctx context.Context, op string) error {
	return requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), op, "webhook configs")
}

func (s *Webhooks) FindByWalletID(ctx context.Context, walletID uuid.UUID) ([]models.WebhookConfig, error) {
	if err := s.ready(ctx, "list wallet webhooks"); err != nil {
		return nil, err
	}
	return s.store.FindByWalletID(ctx, walletID)
}

func (s *Webhooks) Create(ctx context.Context, cfg *models.WebhookConfig) error {
	if err := s.ready(ctx, "create wallet webhook"); err != nil {
		return err
	}
	return s.store.Create(ctx, cfg)
}

func (s *Webhooks) FindByIDAndWallet(ctx context.Context, id, walletID uuid.UUID) (*models.WebhookConfig, error) {
	if err := s.ready(ctx, "find wallet webhook"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndWallet(ctx, id, walletID)
}

func (s *Webhooks) Delete(ctx context.Context, cfg *models.WebhookConfig) error {
	if err := s.ready(ctx, "delete wallet webhook"); err != nil {
		return err
	}
	return s.store.Delete(ctx, cfg)
}

// TransactionStore is the transaction persistence wallet handlers use.
type TransactionStore interface {
	FindByWallet(ctx context.Context, walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error)
	FindByIDAndWallet(ctx context.Context, txID string, walletID uuid.UUID) (*models.Transaction, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
	FindByChainAndTxHash(ctx context.Context, chainID, txHash string) (*models.Transaction, error)
}

// Transactions reads stored transactions.
type Transactions struct{ store TransactionStore }

// NewTransactions builds the transaction record service.
func NewTransactions(store TransactionStore) *Transactions { return &Transactions{store: store} }

func (s *Transactions) ready(ctx context.Context, op string) error {
	return requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), op, "transactions")
}

func (s *Transactions) FindByWallet(ctx context.Context, walletID uuid.UUID, txType, status string, limit, offset int) ([]models.Transaction, int64, error) {
	if err := s.ready(ctx, "list transactions"); err != nil {
		return nil, 0, err
	}
	return s.store.FindByWallet(ctx, walletID, txType, status, limit, offset)
}

func (s *Transactions) FindByIDAndWallet(ctx context.Context, txID string, walletID uuid.UUID) (*models.Transaction, error) {
	if err := s.ready(ctx, "find transaction"); err != nil {
		return nil, err
	}
	return s.store.FindByIDAndWallet(ctx, txID, walletID)
}

func (s *Transactions) FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error) {
	if err := s.ready(ctx, "find transaction"); err != nil {
		return nil, err
	}
	return s.store.FindByID(ctx, id)
}

func (s *Transactions) FindByChainAndTxHash(ctx context.Context, chainID, txHash string) (*models.Transaction, error) {
	if err := s.ready(ctx, "find transaction"); err != nil {
		return nil, err
	}
	return s.store.FindByChainAndTxHash(ctx, chainID, txHash)
}

// AddressStore reads stored addresses.
type AddressStore interface {
	PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error)
	FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error)
}

// Addresses pages wallet addresses.
type Addresses struct{ store AddressStore }

// NewAddresses builds the address record service.
func NewAddresses(store AddressStore) *Addresses { return &Addresses{store: store} }

func (s *Addresses) PaginateByWalletID(ctx context.Context, walletID uuid.UUID, limit, offset int) ([]models.Address, int64, error) {
	if err := requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), "list addresses", "addresses"); err != nil {
		return nil, 0, err
	}
	return s.store.PaginateByWalletID(ctx, walletID, limit, offset)
}

func (s *Addresses) FindByChainAndAddress(ctx context.Context, chainID, address string) (*models.Address, error) {
	if err := requireStore(ctx, storeOf(s, func() any {
		if s == nil {
			return nil
		}
		return s.store
	}), "find address", "addresses"); err != nil {
		return nil, err
	}
	return s.store.FindByChainAndAddress(ctx, chainID, address)
}
