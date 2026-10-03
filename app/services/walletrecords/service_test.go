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
)

func TestWalletsPaginateByAccountForwards(t *testing.T) {
	t.Parallel()

	accountID := uuid.New()
	want := []models.Wallet{{ID: uuid.New()}}
	store := &fakeWallets{rows: want, total: 3}
	got, total, err := walletrecords.NewWallets(store).PaginateByAccount(context.Background(), accountID, "eth", 20, 0)
	require.NoError(t, err)
	require.Equal(t, want, got)
	require.Equal(t, int64(3), total)
	require.Equal(t, accountID, store.accountID)
	require.Equal(t, "eth", store.chain)

	store.err = errors.New("store down")
	_, err = walletrecords.NewWallets(store).FindByID(context.Background(), uuid.New())
	require.ErrorIs(t, err, store.err)

	_, _, err = walletrecords.NewWallets(store).PaginateByAccount(nil, accountID, "", 1, 0)
	require.EqualError(t, err, "list wallets: context is required")
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
func (f *fakeWallets) FindByID(context.Context, uuid.UUID) (*models.Wallet, error) {
	return nil, f.err
}
func (f *fakeWallets) FindByIDAndAccount(context.Context, uuid.UUID, uuid.UUID) (*models.Wallet, error) {
	return nil, f.err
}
func (f *fakeWallets) SetFeeRateMin(context.Context, uuid.UUID, int) error { return f.err }
func (f *fakeWallets) SetFeeRateMax(context.Context, uuid.UUID, int) error { return f.err }
func (f *fakeWallets) SetFeeMultiplier(context.Context, uuid.UUID, float64) error {
	return f.err
}
func (f *fakeWallets) SetRequiredApprovals(context.Context, uuid.UUID, int) error { return f.err }
func (f *fakeWallets) SetFrozenUntil(context.Context, uuid.UUID, time.Time) error { return f.err }
func (f *fakeWallets) SetStatus(context.Context, uuid.UUID, string) error         { return f.err }
