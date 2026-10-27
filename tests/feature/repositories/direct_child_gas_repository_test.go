package repositories_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func gasPreparationRow(walletID, stepID uuid.UUID) *models.Transaction {
	key := stepID.String()
	return &models.Transaction{ID: stepID, WalletID: walletID, ExternalUserID: "system", Chain: "eth", TxType: models.TxTypeGasSeed,
		TxHash: "0x" + stepID.String(), FromAddress: "0xbase", ToAddress: "0xchild", Amount: "1000", Asset: "eth", Status: string(types.TxStatusConfirming),
		RequiredConfs: 1, LogIndex: models.TransactionLogIndexNone, Origin: models.TxOriginGasSeed, Direction: models.TxDirectionSelf, Source: models.TxSourceWithdrawalFlow,
		IdempotencyKey: &key, RawPayload: `{"direct_child_gas":{},"sender_nonce":0,"broadcast_outcome":"pending"}`}
}

func TestDirectChildGasRepository_CommitRollbackAndOuterTransactionRefusal(t *testing.T) {
	fixtures.TestDB(t)
	repo := repositories.NewTransactionRepository(nil)
	wallet := fixtures.InsertWallet(t, "eth")
	step := uuid.New()
	require.NoError(t, repo.WithinDirectChildGas(context.Background(), wallet.ID, step, func(ctx context.Context) error {
		busy, err := repo.DirectChildGasNonceOccupied(ctx, step, "eth", "0xbase", 0)
		require.NoError(t, err)
		require.False(t, busy)
		return repo.Create(ctx, gasPreparationRow(wallet.ID, step))
	}))
	row, err := repo.FindByID(context.Background(), step)
	require.NoError(t, err)
	require.Equal(t, step, row.ID)
	rollbackStep := uuid.New()
	rollback := errors.New("synthetic rollback")
	err = repo.WithinDirectChildGas(context.Background(), wallet.ID, rollbackStep, func(ctx context.Context) error {
		if createErr := repo.Create(ctx, gasPreparationRow(wallet.ID, rollbackStep)); createErr != nil {
			return createErr
		}
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	_, err = repo.FindByID(context.Background(), rollbackStep)
	require.ErrorIs(t, err, models.ErrRepositoryNotFound)
	require.NoError(t, repo.Within(context.Background(), func(ctx context.Context) error {
		called := false
		err = repo.WithinDirectChildGas(ctx, wallet.ID, uuid.New(), func(context.Context) error { called = true; return nil })
		require.ErrorContains(t, err, "outer transaction")
		require.False(t, called)
		return nil
	}))
	_, err = repo.DirectChildGasNonceOccupied(context.Background(), step, "eth", "0xbase", 0)
	require.ErrorContains(t, err, "requires its transaction")
}

func TestDirectChildGasRepository_ConcurrentClaimsInsertOneImmutableStep(t *testing.T) {
	fixtures.TestDB(t)
	repo := repositories.NewTransactionRepository(nil)
	wallet := fixtures.InsertWallet(t, "eth")
	step := uuid.New()
	start := make(chan struct{})
	results := make(chan error, 12)
	var group sync.WaitGroup
	var callbacks atomic.Int64
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			results <- repo.WithinDirectChildGas(context.Background(), wallet.ID, step, func(ctx context.Context) error {
				row, err := repo.FindByID(ctx, step)
				if err != nil && !errors.Is(err, models.ErrRepositoryNotFound) {
					return err
				}
				if row != nil {
					return nil
				}
				callbacks.Add(1)
				return repo.Create(ctx, gasPreparationRow(wallet.ID, step))
			})
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, callbacks.Load())
	row, err := repo.FindByID(context.Background(), step)
	require.NoError(t, err)
	require.Equal(t, "1000", row.Amount)
}

func TestDirectChildGasRepository_ZeroNonceRemainsOwnedAcrossOtherOperations(t *testing.T) {
	fixtures.TestDB(t)
	repo := repositories.NewTransactionRepository(nil)
	wallet := fixtures.InsertWallet(t, "eth")
	first, second := uuid.New(), uuid.New()
	require.NoError(t, repo.WithinDirectChildGas(context.Background(), wallet.ID, first, func(ctx context.Context) error { return repo.Create(ctx, gasPreparationRow(wallet.ID, first)) }))
	require.NoError(t, repo.WithinDirectChildGas(context.Background(), wallet.ID, second, func(ctx context.Context) error {
		busy, err := repo.DirectChildGasNonceOccupied(ctx, second, "eth", "0xBASE", 0)
		require.NoError(t, err)
		require.True(t, busy)
		busy, err = repo.DirectChildGasNonceOccupied(ctx, first, "eth", "0xbase", 0)
		require.NoError(t, err)
		require.False(t, busy)
		busy, err = repo.DirectChildGasNonceOccupied(ctx, second, "eth", "0xbase", 1)
		require.NoError(t, err)
		require.False(t, busy)
		return nil
	}))
}
