package walletrecords_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/pkg/numeric"
	"github.com/stretchr/testify/assert"
)

func TestWallets_Paginate_ByAccountForwards(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	want := []models.Wallet{{ID: uuid.New()}}
	store := &fakeWallets{rows: want, total: 3}
	got, total, err := walletrecords.NewWallets(store).PaginateByAccount(context.Background(), accountID, "eth", 20, 0)
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, int64(3), total)
	assert.Equal(t, accountID, store.accountID)
	assert.Equal(t, "eth", store.chain)

	store.err = errors.New("store down")
	_, err = walletrecords.NewWallets(store).FindByID(context.Background(), uuid.New())
	assert.ErrorIs(t, err, store.err)

	_, _, err = walletrecords.NewWallets(store).PaginateByAccount(nil, accountID, "", 1, 0)
	assert.EqualError(t, err, "list wallets: context is required")
}

type fakeWallets struct {
	rows      []models.Wallet
	total     int64
	accountID uuid.UUID
	chain     string
	err       error
}

func (f *fakeWallets) PaginateByAccount(_ context.Context, accountID uuid.UUID, chain string, _, _ int) ([]models.Wallet, int64, error) {
	f.accountID = accountID
	f.chain = chain
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, f.total, nil
}
func (f *fakeWallets) PaginateByAccountAndMember(_ context.Context, accountID, userID uuid.UUID, chain string, _, _ int) ([]models.Wallet, int64, error) {
	f.accountID = accountID
	f.chain = chain
	if userID == uuid.Nil {
		return nil, 0, errors.New("user is required")
	}
	if f.err != nil {
		return nil, 0, f.err
	}
	return f.rows, f.total, nil
}
func (f *fakeWallets) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, f.err
}
func (f *fakeWallets) FindByIDAndAccount(context.Context, uuid.UUID, uuid.UUID) (*models.Wallet, error) {
	return nil, f.err
}
func (f *fakeWallets) SetFeeRateMin(context.Context, uuid.UUID, int) error { return f.err }
func (f *fakeWallets) SetFeeRateMax(context.Context, uuid.UUID, int) error { return f.err }
func (f *fakeWallets) SetFeeMultiplier(context.Context, uuid.UUID, numeric.NullDecimal) error {
	return f.err
}
func (f *fakeWallets) UpdateSettings(context.Context, uuid.UUID, map[string]any) error { return f.err }
func (f *fakeWallets) SetRequiredApprovals(context.Context, uuid.UUID, int) error      { return f.err }
func (f *fakeWallets) SetFrozenUntil(context.Context, uuid.UUID, time.Time) error      { return f.err }
func (f *fakeWallets) SetStatus(context.Context, uuid.UUID, string) error              { return f.err }
func (f *fakeWallets) SetLabel(context.Context, uuid.UUID, string) error               { return f.err }

func TestTransactions_Find_ByChainAndTxHashForwards(t *testing.T) {
	t.Parallel()

	want := &models.Transaction{ID: uuid.New(), TxHash: "0xabc"}
	store := &fakeTransactions{row: want}
	got, err := walletrecords.NewTransactions(store).FindByChainAndTxHash(context.Background(), "eth", "0xabc")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "eth", store.chainID)
	assert.Equal(t, "0xabc", store.txHash)

	store.err = errors.New("store down")
	_, err = walletrecords.NewTransactions(store).FindByChainAndTxHash(context.Background(), "eth", "0xabc")
	assert.ErrorIs(t, err, store.err)

	_, err = walletrecords.NewTransactions(store).FindByChainAndTxHash(nil, "eth", "0xabc")
	assert.EqualError(t, err, "find transaction: context is required")
}

type fakeTransactions struct {
	row     *models.Transaction
	chainID string
	txHash  string
	err     error
}

func (f *fakeTransactions) FindByWallet(context.Context, uuid.UUID, string, string, int, int) ([]models.Transaction, int64, error) {
	return nil, 0, f.err
}
func (f *fakeTransactions) FindByIDAndWallet(context.Context, string, uuid.UUID) (*models.Transaction, error) {
	return nil, f.err
}
func (f *fakeTransactions) FindByID(context.Context, uuid.UUID) (*models.Transaction, error) {
	return nil, f.err
}
func (f *fakeTransactions) FindByChainAndTxHash(_ context.Context, chainID, txHash string) (*models.Transaction, error) {
	f.chainID = chainID
	f.txHash = txHash
	if f.err != nil {
		return nil, f.err
	}
	return f.row, nil
}

func TestAddresses_Find_ByChainAndAddressForwards(t *testing.T) {
	t.Parallel()

	want := &models.Address{ID: uuid.New(), Address: "0xabc"}
	store := &fakeAddresses{row: want}
	got, err := walletrecords.NewAddresses(store).FindByChainAndAddress(context.Background(), "eth", "0xabc")
	require.NoError(t, err)
	assert.Equal(t, want, got)
	assert.Equal(t, "eth", store.chainID)
	assert.Equal(t, "0xabc", store.address)

	store.err = errors.New("store down")
	_, err = walletrecords.NewAddresses(store).FindByChainAndAddress(context.Background(), "eth", "0xabc")
	assert.ErrorIs(t, err, store.err)

	_, err = walletrecords.NewAddresses(store).FindByChainAndAddress(nil, "eth", "0xabc")
	assert.EqualError(t, err, "find address: context is required")
}

type fakeAddresses struct {
	row     *models.Address
	chainID string
	address string
	err     error
}

func (f *fakeAddresses) PaginateByWalletID(context.Context, uuid.UUID, int, int) ([]models.Address, int64, error) {
	return nil, 0, f.err
}

func (f *fakeAddresses) FindByChainAndAddress(_ context.Context, chainID, address string) (*models.Address, error) {
	f.chainID = chainID
	f.address = address
	if f.err != nil {
		return nil, f.err
	}
	return f.row, nil
}
