package withdraw

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"github.com/macrowallets/waas/tests/mocks"
)

// burst releases n goroutines together once every one is waiting.
func burst(n int, work func()) {
	var ready, done sync.WaitGroup
	start := make(chan struct{})
	ready.Add(n)
	done.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			work()
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()
}

// oneWithdrawalRow is the withdrawal primary key the service already uses:
// the idempotency key is the row id. Reads in one burst all observe an empty
// table, then Create accepts a single insert.
type oneWithdrawalRow struct {
	mu      sync.Mutex
	callers int
	waiting int
	release chan struct{}
	row     *models.Withdrawal
	inserts int
	retries int
}

func newOneWithdrawalRow(callers int) *oneWithdrawalRow {
	return &oneWithdrawalRow{callers: callers, release: make(chan struct{})}
}

func (o *oneWithdrawalRow) Within(_ context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return errors.New("callback is required")
	}
	return fn(context.Background())
}

func (o *oneWithdrawalRow) FindByIDAndWallet(_ context.Context, _, _ uuid.UUID) (*models.Withdrawal, error) {
	o.mu.Lock()
	o.waiting++
	if o.waiting == o.callers {
		close(o.release)
	}
	o.mu.Unlock()
	<-o.release
	return nil, models.ErrRepositoryNotFound
}

func (o *oneWithdrawalRow) Create(_ context.Context, withdrawal *models.Withdrawal) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.row != nil {
		return errors.New(`duplicate key value violates unique constraint "withdrawals_pkey"`)
	}
	copied := *withdrawal
	o.row = &copied
	o.inserts++
	return nil
}

func (o *oneWithdrawalRow) RetryBroadcast(context.Context, uuid.UUID, string, string, string, string) error {
	o.mu.Lock()
	o.retries++
	o.mu.Unlock()
	return nil
}

func TestService_Create_ExactlyOneIdempotencyKeyWins(t *testing.T) {
	const callers = 16
	mock := mocks.NewMockChain("eth")
	mock.NativeAssetVal = "ETH"
	mock.EstimateFeeFn = func(context.Context, types.TransferRequest) (*types.FeeEstimate, error) {
		return &types.FeeEstimate{Fee: "1", FeeAsset: "ETH"}, nil
	}
	rows := newOneWithdrawalRow(callers)
	svc := newCreateService(t, mock, rows, acceptTotp{}, &memChains{decimals: 18})
	wallet := sealedCreateWallet(t, "eth", nil)
	key := uuid.New()
	input := CreateInput{
		Wallet:             wallet,
		Passphrase:         createTestPassphrase,
		Amount:             "1",
		DestinationAddress: "0xdest",
		IdempotencyKey:     key.String(),
	}

	outcomes := make(chan error, callers)
	burst(callers, func() {
		result, err := svc.Create(context.Background(), input)
		if err == nil {
			if result == nil || result.Replayed || result.Withdrawal == nil || result.Withdrawal.ID != key {
				err = errors.New("winner did not return the idempotent withdrawal")
			}
		}
		outcomes <- err
	})

	winners := 0
	for i := 0; i < callers; i++ {
		err := <-outcomes
		if err == nil {
			winners++
			continue
		}
		var rowErr *CreateRowError
		require.ErrorAs(t, err, &rowErr)
	}
	require.Equal(t, 1, winners)
	require.Equal(t, 1, rows.inserts)
	require.Equal(t, 0, rows.retries)
}
