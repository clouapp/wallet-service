package repositories

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/shopspring/decimal"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/numeric"
)

// WalletRepository persists wallets.
type WalletRepository struct {
	db.Base
}

// NewWalletRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewWalletRepository(query orm.Query) *WalletRepository {
	return &WalletRepository{Base: db.NewBase(query)}
}

// Create inserts a wallet.
func (r *WalletRepository) Create(ctx context.Context, wallet *models.Wallet) error {
	if wallet == nil {
		return fmt.Errorf("create wallet: wallet is nil")
	}
	if err := r.Query(ctx).Create(wallet); err != nil {
		return fmt.Errorf("create wallet: %w", err)
	}
	return nil
}

// FindByID returns the wallet with its deposit address, or ErrRepositoryNotFound.
func (r *WalletRepository) FindByID(ctx context.Context, id uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	if err := r.Query(ctx).With("DepositAddress").Where("id = ?", id).First(&wallet); err != nil {
		return nil, fmt.Errorf("find wallet: %w", err)
	}
	if wallet.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &wallet, nil
}

// FindByIDAndAccount returns the wallet when it belongs to accountID, or ErrRepositoryNotFound.
func (r *WalletRepository) FindByIDAndAccount(ctx context.Context, id, accountID uuid.UUID) (*models.Wallet, error) {
	var wallet models.Wallet
	if err := r.Query(ctx).With("DepositAddress").Where("id = ? AND account_id = ?", id, accountID).First(&wallet); err != nil {
		return nil, fmt.Errorf("find wallet for account: %w", err)
	}
	if wallet.ID == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	return &wallet, nil
}

// FindAll returns every wallet ordered by created_at.
func (r *WalletRepository) FindAll(ctx context.Context) ([]models.Wallet, error) {
	var wallets []models.Wallet
	if err := r.Query(ctx).With("DepositAddress").Order("created_at").Find(&wallets); err != nil {
		return nil, fmt.Errorf("list wallets: %w", err)
	}
	return wallets, nil
}

// PaginateAll pages through every wallet ordered by created_at.
func (r *WalletRepository) PaginateAll(ctx context.Context, limit, offset int) ([]models.Wallet, int64, error) {
	var wallets []models.Wallet
	total, err := r.Query(ctx).Model(&models.Wallet{}).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count wallets: %w", err)
	}
	if err := r.Query(ctx).With("DepositAddress").Order("created_at").Offset(offset).Limit(limit).Find(&wallets); err != nil {
		return nil, 0, fmt.Errorf("list wallets: %w", err)
	}
	return wallets, total, nil
}

// assignedWalletMembership keeps wallets the user still belongs to.
// A soft-deleted or non-active wallet membership does not count.
const assignedWalletMembership = "id IN (SELECT wallet_id FROM wallet_users WHERE user_id = ? AND deleted_at IS NULL AND status = ?)"

// PaginateByAccount pages through one account's wallets, optionally one chain.
func (r *WalletRepository) PaginateByAccount(ctx context.Context, accountID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	return r.paginateByAccount(ctx, accountID, chain, limit, offset, nil)
}

// PaginateByAccountAndMember pages the account wallets the user belongs to.
func (r *WalletRepository) PaginateByAccountAndMember(ctx context.Context, accountID, userID uuid.UUID, chain string, limit, offset int) ([]models.Wallet, int64, error) {
	if userID == uuid.Nil {
		return nil, 0, fmt.Errorf("list account wallets: user is required")
	}
	return r.paginateByAccount(ctx, accountID, chain, limit, offset, func(query orm.Query) orm.Query {
		return query.Where(assignedWalletMembership, userID, models.StatusActive)
	})
}

func (r *WalletRepository) paginateByAccount(ctx context.Context, accountID uuid.UUID, chain string, limit, offset int, restrict func(orm.Query) orm.Query) ([]models.Wallet, int64, error) {
	var wallets []models.Wallet
	countQuery := r.Query(ctx).Model(&models.Wallet{}).Where("account_id = ?", accountID)
	if chain != "" {
		countQuery = countQuery.Where("chain = ?", chain)
	}
	if restrict != nil {
		countQuery = restrict(countQuery)
	}
	total, err := countQuery.Count()
	if err != nil {
		return nil, 0, fmt.Errorf("count account wallets: %w", err)
	}

	listQuery := r.Query(ctx).With("DepositAddress").Where("account_id = ?", accountID)
	if chain != "" {
		listQuery = listQuery.Where("chain = ?", chain)
	}
	if restrict != nil {
		listQuery = restrict(listQuery)
	}
	if err := listQuery.Order("created_at").Offset(offset).Limit(limit).Find(&wallets); err != nil {
		return nil, 0, fmt.Errorf("list account wallets: %w", err)
	}
	return wallets, total, nil
}

// SetDepositAddressID sets wallets.deposit_address_id.
func (r *WalletRepository) SetDepositAddressID(ctx context.Context, id, addressID uuid.UUID) error {
	return r.updateColumn(ctx, id, "deposit_address_id", addressID, "set wallet deposit address")
}

// SetMPCChainCode sets wallets.mpc_chain_code.
func (r *WalletRepository) SetMPCChainCode(ctx context.Context, id uuid.UUID, chainCode string) error {
	return r.updateColumn(ctx, id, "mpc_chain_code", chainCode, "set wallet chain code")
}

// Activate sets status and clears activation_code together.
func (r *WalletRepository) Activate(ctx context.Context, id uuid.UUID, status string) error {
	if _, err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Update(map[string]any{
		"status":          status,
		"activation_code": nil,
	}); err != nil {
		return fmt.Errorf("activate wallet: %w", err)
	}
	return nil
}

// SetFeeRateMin sets wallets.fee_rate_min.
func (r *WalletRepository) SetFeeRateMin(ctx context.Context, id uuid.UUID, value int) error {
	return r.updateColumn(ctx, id, "fee_rate_min", value, "set wallet fee_rate_min")
}

// SetFeeRateMax sets wallets.fee_rate_max.
func (r *WalletRepository) SetFeeRateMax(ctx context.Context, id uuid.UUID, value int) error {
	return r.updateColumn(ctx, id, "fee_rate_max", value, "set wallet fee_rate_max")
}

// SetFeeMultiplier sets wallets.fee_multiplier. A negative multiplier is refused.
func (r *WalletRepository) SetFeeMultiplier(ctx context.Context, id uuid.UUID, value numeric.NullDecimal) error {
	if err := rejectNegativeWalletSetting("fee_multiplier", value); err != nil {
		return err
	}
	return r.updateColumn(ctx, id, "fee_multiplier", value, "set wallet fee_multiplier")
}

// UpdateSettings writes the validated settings columns. A negative amount is refused
// and nothing is stored.
func (r *WalletRepository) UpdateSettings(ctx context.Context, id uuid.UUID, columns map[string]any) error {
	if ctx == nil {
		return fmt.Errorf("update wallet settings: context is required")
	}
	if id == uuid.Nil {
		return fmt.Errorf("update wallet settings: wallet id is required")
	}
	if len(columns) == 0 {
		return fmt.Errorf("update wallet settings: no columns to write")
	}
	for column, value := range columns {
		if err := rejectNegativeWalletSetting(column, value); err != nil {
			return err
		}
	}
	if _, err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Update(columns); err != nil {
		return fmt.Errorf("update wallet settings: %w", err)
	}
	return nil
}

func rejectNegativeWalletSetting(column string, value any) error {
	switch typed := value.(type) {
	case numeric.NullDecimal:
		if typed.Valid && typed.Decimal.IsNegative() {
			return fmt.Errorf("wallet %s %s: %w", column, typed.Decimal.String(), numeric.ErrNegative)
		}
	case numeric.Decimal:
		if typed.IsNegative() {
			return fmt.Errorf("wallet %s %s: %w", column, typed.String(), numeric.ErrNegative)
		}
	case decimal.Decimal:
		if typed.IsNegative() {
			return fmt.Errorf("wallet %s %s: %w", column, typed.String(), numeric.ErrNegative)
		}
	}
	return nil
}

// SetRequiredApprovals sets wallets.required_approvals.
func (r *WalletRepository) SetRequiredApprovals(ctx context.Context, id uuid.UUID, value int) error {
	return r.updateColumn(ctx, id, "required_approvals", value, "set wallet required_approvals")
}

// SetFrozenUntil sets wallets.frozen_until.
func (r *WalletRepository) SetFrozenUntil(ctx context.Context, id uuid.UUID, until time.Time) error {
	return r.updateColumn(ctx, id, "frozen_until", until, "set wallet frozen_until")
}

// SetStatus sets wallets.status.
func (r *WalletRepository) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	return r.updateColumn(ctx, id, "status", status, "set wallet status")
}

// SetLabel sets wallets.label.
func (r *WalletRepository) SetLabel(ctx context.Context, id uuid.UUID, label string) error {
	return r.updateColumn(ctx, id, "label", label, "set wallet label")
}

// SetBalanceSummary writes the native balance columns and the read-model status.
func (r *WalletRepository) SetBalanceSummary(ctx context.Context, id uuid.UUID, asset, raw, display string, syncedAt time.Time, readModelStatus string) error {
	fields := map[string]any{
		"balance_asset":          asset,
		"balance_raw":            raw,
		"balance_display":        display,
		"balance_last_synced_at": syncedAt,
		"read_model_status":      readModelStatus,
	}
	if err := amount.RequireNonNegativeColumns(fields, models.WalletBalanceAmountColumns...); err != nil {
		return err
	}
	if _, err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Update(fields); err != nil {
		return fmt.Errorf("set wallet balance summary: %w", err)
	}
	return nil
}

// SetBalanceRaw sets wallets.balance_raw after rejecting a negative amount.
func (r *WalletRepository) SetBalanceRaw(ctx context.Context, id uuid.UUID, raw string) error {
	fields := map[string]any{"balance_raw": raw}
	if err := amount.RequireNonNegativeColumns(fields, models.WalletBalanceAmountColumns...); err != nil {
		return err
	}
	return r.updateColumn(ctx, id, "balance_raw", raw, "set wallet balance_raw")
}

// RecordGasCheck sets gas_last_checked_at and, when updateStatus is set, gas_status.
func (r *WalletRepository) RecordGasCheck(ctx context.Context, id uuid.UUID, checkedAt time.Time, status string, updateStatus bool) error {
	fields := map[string]any{"gas_last_checked_at": checkedAt}
	if updateStatus {
		fields["gas_status"] = status
	}
	if _, err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Update(fields); err != nil {
		return fmt.Errorf("record wallet gas check: %w", err)
	}
	return nil
}

// IncrementAddressIndex adds one to wallets.address_index and returns the new value.
func (r *WalletRepository) IncrementAddressIndex(ctx context.Context, id uuid.UUID) (int, error) {
	if _, err := r.Query(ctx).Exec("UPDATE wallets SET address_index = address_index + 1 WHERE id = ?", id); err != nil {
		return 0, fmt.Errorf("increment wallet address index: %w", err)
	}
	var newIndex int
	if err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Pluck("address_index", &newIndex); err != nil {
		return 0, fmt.Errorf("read wallet address index: %w", err)
	}
	return newIndex, nil
}

func (r *WalletRepository) updateColumn(ctx context.Context, id uuid.UUID, column string, value any, op string) error {
	if _, err := r.Query(ctx).Model(&models.Wallet{}).Where("id = ?", id).Update(column, value); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}
