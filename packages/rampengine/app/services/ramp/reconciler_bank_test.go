package ramp_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// staleBankPayout is a bank payout the sweep scans: processing, with the payout
// id the finalize stored.
func staleBankPayout(status string) models.Transaction {
	tx := *bankPayout(status)
	tx.CustomerID, tx.UserID = reconcileUser, reconcileUser
	return tx
}

// A bank payout is read against the binding stored at finalize, never through
// the generic GetOrder, and what it reports is applied like any refresh.
func TestSweepReadsABankPayoutBoundAndCompletesIt(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusCompleted}, nil
	}

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	var saved *models.Transaction
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once().
		Run(func(args testifymock.Arguments) { saved = args.Get(0).(*models.Transaction) })
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, []string{"po_1"}, h.boundReads)
	require.Equal(t, models.TxStatusCompleted, saved.Status)
	require.NotNil(t, saved.CompletedAt)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A Pix payout a provider prepared is read bound, like a bank payout: the
// reader follows how the order is funded, not its rail.
func TestSweepReadsAPreparedPixPayoutBound(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	pix, prepared := models.RailPixPayout, models.FundingModePrepared
	order.Rail, order.FundingMode = &pix, &prepared
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusCompleted}, nil
	}

	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, []string{"po_1"}, h.boundReads)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// The binding handed to the provider is the stored one, field for field.
func TestSweepHoldsABankPayoutToTheStoredBinding(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	stored := payoutBinding()
	h.bindings.byTx[order.ID] = stored
	var got ramp.OrderBinding
	h.bound = func(_ string, b ramp.OrderBinding) (*ramp.RampOrder, error) {
		got = b
		return &ramp.RampOrder{Status: models.TxStatusProcessing}, nil
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()

	_, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, *stored, got)
}

// A payout that is not the one the binding names writes nothing and tells no
// one, however it answers.
func TestSweepWritesNothingOnABindingMismatch(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return nil, ramp.ErrBindingMismatch
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.txRepo.EXPECT().TouchUpdatedAt(order.ID).Return(nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 1, report.Attention, "the mismatch is counted")
	require.Equal(t, 0, report.Failed)
	require.Equal(t, []string{"po_1"}, h.boundReads)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.announced)
}

// A bank payout with no stored binding is not read at all: there is nothing to
// hold the provider's answer to.
func TestSweepSkipsABankPayoutWithNoStoredBinding(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.txRepo.EXPECT().TouchUpdatedAt(order.ID).Return(nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	require.Equal(t, 1, report.Attention)
	require.Empty(t, h.boundReads)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}

// A binding that cannot be read is a failed refresh, retried next sweep.
func TestSweepCountsAnUnreadableBindingAsFailed(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.err = errors.New("db down")
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	require.Empty(t, h.boundReads)
}

// A provider without the bound reader is the usual error, not a fall-back to
// the generic read.
func TestSweepNeverFallsBackToTheGenericReadForABankPayout(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankUnbound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Failed)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}

// A stale read never walks a payout under refund review back to processing.
func TestSweepKeepsARefundAttentionPayoutAgainstAStaleBoundRead(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusRefundAttention)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusProcessing}, nil
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 0, report.Updated)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}

// The admin refresh button takes the same bound path as the sweep.
func TestRefreshOneReadsABankPayoutBound(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusCompleted}, nil
	}
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, []string{"po_1"}, h.boundReads)
	h.adapter.AssertNotCalled(t, "GetOrder", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A bound mismatch through the admin button also writes nothing.
func TestRefreshOneWritesNothingOnABindingMismatch(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) { return nil, ramp.ErrBindingMismatch }

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.False(t, changed)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
}

// Following a payout in flight is not gated on eligibility: a customer whose
// bank_payout endorsement was revoked still has the payout read bound and
// applied, and a mismatch still writes nothing.
func TestSweepFollowsABankPayoutAfterTheEndorsementIsRevoked(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	require.NoError(t, h.kycRow.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementBankPayout, Status: models.EndorsementStatusRevoked},
	}))
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusCompleted}, nil
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, []string{"po_1"}, h.boundReads)
}

func TestRefreshOneFollowsABankPayoutAfterTheEndorsementIsRevoked(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	require.NoError(t, h.kycRow.SetEndorsements(nil))
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) { return nil, ramp.ErrBindingMismatch }

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, []string{"po_1"}, h.boundReads, "the payout was read though the customer is no longer eligible")
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	h.txRepo.AssertNotCalled(t, "TouchUpdatedAt", testifymock.Anything)
}

// A row that stays wrong is touched and the rows behind it are still swept: it
// cannot hold the head of the queue.
func TestSweepReachesTheOrdersBehindAMismatchingPayout(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	bad := staleBankPayout(models.TxStatusProcessing)
	bad.ID, bad.GatewayOrderID = 1, "po_bad"
	good := staleBankPayout(models.TxStatusProcessing)
	good.ID, good.GatewayOrderID = 2, "po_good"
	h.bindings.byTx[1], h.bindings.byTx[2] = payoutBinding(), payoutBinding()
	h.bound = func(id string, _ ramp.OrderBinding) (*ramp.RampOrder, error) {
		if id == "po_bad" {
			return nil, ramp.ErrBindingMismatch
		}
		return &ramp.RampOrder{ProviderOrderID: id, Status: models.TxStatusCompleted}, nil
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 2).
		Return([]models.Transaction{bad, good}, nil).Once()
	h.txRepo.EXPECT().TouchUpdatedAt(uint(1)).Return(nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 2)
	require.NoError(t, err)
	require.Equal(t, 1, report.Attention)
	require.Equal(t, 1, report.Updated)
	require.Equal(t, []string{"po_bad", "po_good"}, h.boundReads)
}

// A diverging read raises the mark once, however many sweeps see it, and the
// first clean read drops it. The status is never applied while it diverges.
func TestSweepMarksABindingMismatchOnceAndClearsItOnACleanRead(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	clean := false
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		if clean {
			return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusCompleted}, nil
		}
		return nil, ramp.ErrBindingMismatch
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Times(3)
	h.txRepo.EXPECT().TouchUpdatedAt(order.ID).Return(nil).Twice()

	for i := 0; i < 2; i++ {
		report, err := h.reconciler.Sweep(context.Background(), 0, 20)
		require.NoError(t, err)
		require.Equal(t, 1, report.Attention)
		require.Equal(t, 0, report.Updated)
	}
	require.Equal(t, models.AttentionBindingMismatch, h.attention[order.ID])
	require.Equal(t, 2, h.markCalls)
	require.Equal(t, 1, h.markWrote, "the second diverging read writes nothing")
	require.Zero(t, h.clearCalls)
	h.txRepo.AssertNotCalled(t, "SaveProviderSnapshotIfStatus", testifymock.Anything, testifymock.Anything)
	require.Empty(t, h.announced)

	clean = true
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()
	report, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Equal(t, 1, report.Updated)
	require.Empty(t, h.attention, "the clean read cleared the mark")
	require.Equal(t, 1, h.clearCalls)
}

// The admin refresh button raises and clears the same mark.
func TestRefreshOneMarksAndClearsABindingMismatch(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.bindings.byTx[order.ID] = payoutBinding()
	clean := false
	h.bound = func(string, ramp.OrderBinding) (*ramp.RampOrder, error) {
		if clean {
			return &ramp.RampOrder{ProviderOrderID: "po_1", Status: models.TxStatusProcessing}, nil
		}
		return nil, ramp.ErrBindingMismatch
	}

	changed, err := h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, models.AttentionBindingMismatch, h.attention[order.ID])

	clean = true
	_, err = h.reconciler.RefreshOne(context.Background(), &order)
	require.NoError(t, err)
	require.Empty(t, h.attention)
}

// A binding the provider read does not match is flagged; a payout with no
// stored binding is not read and not marked.
func TestSweepDoesNotMarkAPayoutWithNoStoredBinding(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	order := staleBankPayout(models.TxStatusProcessing)
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 20).Return([]models.Transaction{order}, nil).Once()
	h.txRepo.EXPECT().TouchUpdatedAt(order.ID).Return(nil).Once()

	_, err := h.reconciler.Sweep(context.Background(), 0, 20)
	require.NoError(t, err)
	require.Zero(t, h.markCalls)
	require.Zero(t, h.clearCalls)
}

// A repository error while marking or clearing does not abort the batch: the
// orders behind the diverging one are still read and applied.
func TestSweepGoesOnWhenTheMarkCannotBeWritten(t *testing.T) {
	h := newReconcilerHarnessFor(t, false, bankBound)
	bad := staleBankPayout(models.TxStatusProcessing)
	bad.ID, bad.GatewayOrderID = 1, "po_bad"
	good := staleBankPayout(models.TxStatusProcessing)
	good.ID, good.GatewayOrderID = 2, "po_good"
	h.bindings.byTx[1], h.bindings.byTx[2] = payoutBinding(), payoutBinding()
	h.markErr, h.clearErr = errors.New("database down"), errors.New("database down")
	h.bound = func(id string, _ ramp.OrderBinding) (*ramp.RampOrder, error) {
		if id == "po_bad" {
			return nil, ramp.ErrBindingMismatch
		}
		return &ramp.RampOrder{ProviderOrderID: id, Status: models.TxStatusCompleted}, nil
	}
	h.txRepo.EXPECT().FindStaleRampOrders(testifymock.Anything, 2).Return([]models.Transaction{bad, good}, nil).Once()
	h.txRepo.EXPECT().TouchUpdatedAt(uint(1)).Return(nil).Once()
	h.txRepo.EXPECT().SaveProviderSnapshotIfStatus(testifymock.Anything, models.TxStatusProcessing).Return(true, nil).Once()
	h.broadcaster.EXPECT().Publish(reconcileUser, testifymock.Anything).Once()

	report, err := h.reconciler.Sweep(context.Background(), 0, 2)
	require.NoError(t, err)
	require.Equal(t, 1, report.Attention)
	require.Equal(t, 1, report.Updated)
	require.Zero(t, report.Failed)
	require.Equal(t, 1, h.markCalls)
	require.Equal(t, 1, h.clearCalls)
}
