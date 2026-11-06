package swap_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// settlingExecution is an Execution that also reads the funding outcome.
type settlingExecution struct {
	*mocks.MockSwapExecution
	state   swap.FundingState
	settled int
}

func (e *settlingExecution) SettleFunding(context.Context, *models.SwapOrder, *models.Wallet) (swap.FundingState, error) {
	e.settled++
	return e.state, nil
}

func withSettler(t *testing.T, f *executionFixture, state swap.FundingState) *settlingExecution {
	t.Helper()
	execution := &settlingExecution{MockSwapExecution: f.execution, state: state}
	f.service = swap.NewService(swap.Deps{Factory: mustFactory(t, f.provider), Store: f.store, Wallets: walletsFor(t, f), Catalog: mocks.NewMockSwapCatalog(t), Policy: mocks.NewMockSwapPolicy(t), Credentials: f.credentials, Execution: execution, Verifier: f.verifier, Accounts: accountsOf{f.account}})
	return execution
}

func mustFactory(t *testing.T, provider swap.Provider) *swap.Factory {
	t.Helper()
	factory, err := swap.NewFactory(provider)
	require.NoError(t, err)
	return factory
}

func walletsFor(t *testing.T, f *executionFixture) *mocks.MockSwapWallets {
	t.Helper()
	wallets := mocks.NewMockSwapWallets(t)
	wallets.On("FindByID", mock.Anything, f.source.ID).Return(f.source, nil).Maybe()
	wallets.On("FindByID", mock.Anything, f.destination.ID).Return(f.destination, nil).Maybe()
	return wallets
}

type accountsOf struct{ account *models.Account }

func (a accountsOf) FindByID(_ context.Context, id uuid.UUID) (*models.Account, error) {
	if a.account.ID != id {
		return nil, swap.ErrNotFound
	}
	return a.account, nil
}

func TestRefresh_FundingFailedOnChainEndsTheOrderWithoutTheProvider(t *testing.T) {
	for _, status := range []string{swap.StatusBroadcast, swap.StatusReconciling, swap.StatusProcessing} {
		t.Run(status, func(t *testing.T) {
			f := executionSetup(t)
			execution := withSettler(t, f, swap.FundingFailed)
			order := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Provider: swap.ProviderLiFi, Status: status, SourceTxHash: "reverted"}
			f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(order, nil).Once()
			f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
			f.store.On("RecordProgress", mock.Anything, order, status).Return(nil).Once()
			result, err := f.service.Refresh(context.Background(), f.account, f.source, order.ID)
			require.NoError(t, err)
			require.Equal(t, swap.StatusFailed, result.Status)
			require.Equal(t, "funding_failed_on_chain", result.FailureCode)
			require.Equal(t, 1, execution.settled)
			f.provider.AssertNotCalled(t, "Status", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestRefresh_ProviderFailureAfterFundingKeepsReconcilingAndSaysWhy(t *testing.T) {
	for reported, code := range map[string]string{swap.StatusReconciling: "provider_reported_failure", swap.StatusFailed: "provider_reported_failure", swap.StatusPartial: "provider_reported_partial"} {
		t.Run(reported, func(t *testing.T) {
			f := executionSetup(t)
			withSettler(t, f, swap.FundingIncluded)
			order := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Provider: swap.ProviderLiFi, Status: swap.StatusBroadcast, SourceTxHash: "sent"}
			f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(order, nil).Once()
			f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
			f.credentials.On("Provider", mock.Anything, swap.ProviderLiFi).Return(swap.ProviderConfig{}, nil).Once()
			f.provider.On("Status", mock.Anything, mock.Anything, "sent", mock.Anything).Return(&swap.Progress{Status: reported}, nil).Once()
			f.store.On("RecordProgress", mock.Anything, order, swap.StatusBroadcast).Return(nil).Once()
			result, err := f.service.Refresh(context.Background(), f.account, f.source, order.ID)
			require.NoError(t, err)
			require.Equal(t, swap.StatusReconciling, result.Status)
			require.Equal(t, code, result.FailureCode)
		})
	}
}

func TestRefresh_ALostCompareAndSwapReturnsTheStoredOrder(t *testing.T) {
	f := executionSetup(t)
	f.row.ExpiresAt = time.Now().Add(-time.Minute)
	order := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusPreparing}
	stored := &models.SwapOrder{ID: order.ID, Status: swap.StatusExpired, FailureCode: "quote_expired"}
	f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(order, nil).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.store.On("RecordProgress", mock.Anything, order, swap.StatusPreparing).Return(swap.ErrConflict).Once()
	f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, order.ID).Return(stored, nil).Once()
	result, err := f.service.Refresh(context.Background(), f.account, f.source, order.ID)
	require.NoError(t, err)
	require.Same(t, stored, result)
}

func TestExecute_AQuoteExpiredBeforeFundingEndsExpiredLikeRefresh(t *testing.T) {
	f := executionSetup(t)
	f.row.ExpiresAt = time.Now().Add(-time.Minute)
	existing := &models.SwapOrder{ID: uuid.New(), QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusPreparing, AuthorizedUserID: &f.in.UserID, AuthorizedUntil: time.Now().Add(time.Minute)}
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(existing, nil).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.store.On("RecordProgress", mock.Anything, existing, swap.StatusPreparing).Return(nil).Once()
	order, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.NoError(t, err)
	require.Equal(t, swap.StatusExpired, order.Status)
	require.Equal(t, "quote_expired", order.FailureCode)
}

// openStore lists open orders for the reconciler.
type openStore struct {
	*mocks.MockSwapStore
	open []models.SwapOrder
}

func (s openStore) ListOpenOrders(context.Context, int) ([]models.SwapOrder, error) {
	return s.open, nil
}

func TestReconcileSwaps_AdvancesOpenOrdersNobodyPolls(t *testing.T) {
	f := executionSetup(t)
	created := carbon.NewDateTime(carbon.FromStdTime(time.Now().Add(-time.Hour)))
	stuck := models.SwapOrder{ID: uuid.New(), AccountID: f.account.ID, QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Provider: swap.ProviderLiFi, Status: swap.StatusReconciling, SourceTxHash: "reverted"}
	stuck.CreatedAt = created
	orphan := models.SwapOrder{ID: uuid.New(), AccountID: uuid.New(), SourceWalletID: f.source.ID, Status: swap.StatusReconciling}
	execution := &settlingExecution{MockSwapExecution: f.execution, state: swap.FundingFailed}
	store := openStore{MockSwapStore: f.store, open: []models.SwapOrder{orphan, stuck}}
	f.service = swap.NewService(swap.Deps{Factory: mustFactory(t, f.provider), Store: store, Wallets: walletsFor(t, f), Catalog: mocks.NewMockSwapCatalog(t), Policy: mocks.NewMockSwapPolicy(t), Credentials: f.credentials, Execution: execution, Verifier: f.verifier, Accounts: accountsOf{f.account}})
	f.store.On("FindOrder", mock.Anything, f.account.ID, f.source.ID, stuck.ID).Return(&stuck, nil).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.store.On("RecordProgress", mock.Anything, mock.MatchedBy(func(o *models.SwapOrder) bool {
		return o.ID == stuck.ID && o.Status == swap.StatusFailed && o.FailureCode == "funding_failed_on_chain"
	}), swap.StatusReconciling).Return(nil).Once()
	require.NoError(t, f.service.ReconcileSwaps(context.Background()))
	require.Equal(t, 1, execution.settled, "an order whose account is unreadable is skipped, the rest advance")
}

func TestExecute_ACloseThatLosesItsCompareAndSwapReturnsTheStoredOrder(t *testing.T) {
	f := executionSetup(t)
	f.row.ExpiresAt = time.Now().Add(-time.Minute)
	existing := &models.SwapOrder{ID: uuid.New(), AccountID: f.account.ID, IdempotencyKey: f.in.IdempotencyKey, QuoteID: f.quote.ID, SourceWalletID: f.source.ID, Status: swap.StatusPreparing, AuthorizedUserID: &f.in.UserID, AuthorizedUntil: time.Now().Add(time.Minute)}
	funded := &models.SwapOrder{ID: existing.ID, Status: swap.StatusReconciling, SourceTxHash: "persisted-funding"}
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(existing, nil).Once()
	f.store.On("FindQuote", mock.Anything, f.account.ID, f.source.ID, f.quote.ID).Return(f.row, nil).Once()
	f.store.On("RecordProgress", mock.Anything, existing, swap.StatusPreparing).Return(swap.ErrConflict).Once()
	f.store.On("FindOrderByKey", mock.Anything, f.account.ID, f.in.IdempotencyKey).Return(funded, nil).Once()
	order, err := f.service.Execute(context.Background(), f.account, f.source, f.in)
	require.NoError(t, err)
	require.Same(t, funded, order, "a persisted funding is never reported as expired")
}
