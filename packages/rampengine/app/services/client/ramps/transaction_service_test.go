package ramps_test

import (
	"context"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

func TestLatestReturnsNotFoundWhenEmpty(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(nil, nil).Once()

	_, err := h.transactions.Latest(context.Background(), userID)
	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

func TestLatestRefreshesNonTerminal(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOfframp, models.TxStatusProcessing, "trf_1")
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()
	h.adapter.EXPECT().GetOrder(testifymock.Anything, testifymock.Anything, "trf_1").
		Return(&ramp.RampOrder{Status: models.TxStatusCompleted}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(tx, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
}

func TestLatestDoesNotRefreshTerminal(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	tx := storedOrder(models.TransactionTypeOnramp, models.TxStatusCompleted, "trf_1")
	h.txRepo.EXPECT().FindLatestByCustomer(userID).Return(tx, nil).Once()

	view, err := h.transactions.Latest(context.Background(), userID)
	require.NoError(t, err)
	require.Equal(t, models.TxStatusCompleted, view.Status)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestListDoesNotCallProvider(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	rows := make([]models.Transaction, 8)
	for i := range rows {
		rows[i] = *storedOrder(models.TransactionTypeOnramp, models.TxStatusAwaitingFunds, "trf_1")
	}
	h.txRepo.EXPECT().Paginate(repositories.TransactionListFilters{
		CustomerID: userID, RampOnly: true, Page: 1, PerPage: 20,
	}).Return(rows, int64(8), nil).Once()

	views, total, err := h.transactions.List(context.Background(), userID, "", "", nil, nil, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(8), total)
	require.Len(t, views, 8)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

func TestListUnifiedPaginateUsesRampOnlyWhenTypeEmpty(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.txRepo.EXPECT().Paginate(repositories.TransactionListFilters{
		CustomerID: userID, Type: "", Status: "", RampOnly: true, Page: 1, PerPage: 20,
	}).Return(nil, int64(0), nil).Once()

	_, _, err := h.transactions.List(context.Background(), userID, "", "", nil, nil, 1, 20)
	require.NoError(t, err)
}
