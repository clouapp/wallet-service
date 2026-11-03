package ramps_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

type settlingAdapter struct {
	*mocks.MockProviderAdapter
	// requests is every payout asked for, in order; settled the last one.
	requests []ramp.OfframpSettleRequest
	settled  *ramp.OfframpSettleRequest
	order    *ramp.RampOrder
	err      error
	// unfunded makes the balance check answer that the wallet falls short.
	unfunded bool
}

func (s *settlingAdapter) OfframpFundingInstructions(
	context.Context, ramp.CustomerRef, ramp.OfframpFundingRequest,
) (*ramp.OfframpFundingInstructions, error) {
	return &ramp.OfframpFundingInstructions{ToAddress: "dep"}, nil
}

func (s *settlingAdapter) OfframpFundingSatisfied(context.Context, ramp.CustomerRef, ramp.OfframpFundingRequest) (bool, error) {
	return !s.unfunded, nil
}

func (s *settlingAdapter) SettleOfframp(_ context.Context, _ ramp.CustomerRef, req ramp.OfframpSettleRequest) (*ramp.RampOrder, error) {
	s.requests = append(s.requests, req)
	s.settled = &req
	return s.order, s.err
}

// recordingLocker runs fn at once and records the keys it was asked for.
type recordingLocker struct{ keys []string }

func (l *recordingLocker) WithLock(ctx context.Context, key string, fn func(context.Context) error) error {
	l.keys = append(l.keys, key)
	return fn(ctx)
}

// withSettler puts a settling adapter in the Ozav slot, answering order.
func (h *harness) withSettler() *settlingAdapter {
	settling := &settlingAdapter{MockProviderAdapter: h.second,
		order: &ramp.RampOrder{ProviderOrderID: "ozav_tr_settle_1", Status: models.TxStatusFundsReceived}}
	h.withOzavAdapter(settling)
	return settling
}

// claimedOfframp is an awaiting Ozav off-ramp a deposit claimed by hash: it
// settles on the claim, without the balance check.
func claimedOfframp() models.Transaction {
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	tx.DepositTxSignature = support.Ptr("0xhash")
	return tx
}

// expectOnlyFunding answers the balance check's listings with tx alone.
func (h *harness) expectOnlyFunding(tx models.Transaction) {
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).
		Return([]models.Transaction{tx}, nil).Once()
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return(nil, nil).Once()
}

func TestSettleOfframpPostsWhenFunded(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := h.withSettler()
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	// Once for the owner the lock is keyed by, once re-read under it.
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()
	// No deposit claimed it, so the balance is checked again under the lock.
	h.expectOnlyFunding(tx)
	h.txRepo.EXPECT().Update(&tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))
	require.NotNil(t, settling.settled)
	require.Equal(t, "ozav_q_1", settling.settled.ProviderQuoteID)
	require.Equal(t, "ozav_pd_1", settling.settled.ExternalAccountID)
	require.Equal(t, "ozav_tr_settle_1", tx.GatewayOrderID)
}

// The check that queued the settle ran outside the lock: by the time it runs,
// another order of the customer may have spent the balance. An order no
// deposit claimed is left awaiting rather than paid out of someone else's money.
func TestSettleOfframpLeavesAnUnclaimedOrderTheBalanceNoLongerCovers(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := h.withSettler()
	settling.unfunded = true
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()
	h.expectOnlyFunding(tx)

	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))
	require.Empty(t, settling.requests)
	require.Equal(t, models.TxStatusAwaitingFunds, tx.Status)
}

// A claimed deposit is this order's: it settles on the claim, whatever the
// balance aggregate says.
func TestSettleOfframpSettlesAClaimedOrderWithoutTheBalanceCheck(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := h.withSettler()
	settling.unfunded = true
	h.withSettleEstimate()
	tx := claimedOfframp()
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()
	h.txRepo.EXPECT().Update(&tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))
	require.Len(t, settling.requests, 1)
	h.txRepo.AssertNotCalled(t, "FindOpenOfframpsAwaitingSettlement", testifymock.Anything, testifymock.Anything)
}

// Settlement is serialized per customer, not per order: every order draws on
// the same managed wallet.
func TestSettleOfframpHoldsTheCustomersLock(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	locker := &recordingLocker{}
	h.deps.Locker = locker
	h.withSettler()
	tx := settled(claimedOfframp())
	h.txRepo.EXPECT().FindByID(tx.ID).Return(tx, nil).Twice()

	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))
	require.Equal(t, []string{"offramp-settle:customer:7"}, locker.keys)
}

func TestSettleOfframpRefusesAProviderWithoutSettlement(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.withOzavAdapter(h.second)
	tx := claimedOfframp()
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()

	require.ErrorContains(t, h.offramp.Settle(context.Background(), tx.ID), "does not implement off-ramp settlement")
}

// What the payout request needs is checked before the provider is asked: a
// request without its key or destination would move money unrepeatably, or to
// nowhere.
func TestSettleOfframpRefusesAnOrderItCannotBuildAPayoutFor(t *testing.T) {
	cases := map[string]struct {
		mutate func(*models.Transaction)
		want   string
	}{
		"nil idempotency key":   {func(tx *models.Transaction) { tx.IdempotencyKey = nil }, "no idempotency key"},
		"empty idempotency key": {func(tx *models.Transaction) { tx.IdempotencyKey = support.Ptr("") }, "no idempotency key"},
		"no destination ref":    {func(tx *models.Transaction) { tx.ProviderDestinationRef = support.Ptr("") }, "no payout destination ref"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
			settling := h.withSettler()
			tx := claimedOfframp()
			c.mutate(&tx)
			h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()

			require.ErrorContains(t, h.offramp.Settle(context.Background(), tx.ID), c.want)
			require.Empty(t, settling.requests)
		})
	}
}

// The estimate is the payout's terms; without it there is nothing to settle.
func TestSettleOfframpRefusesAnOrderWhoseEstimateCannotBeRead(t *testing.T) {
	cases := map[string]struct {
		mutate func(*models.Transaction)
		row    *models.RampEstimate
		want   error
		msg    string
	}{
		"no estimate":       {mutate: func(tx *models.Transaction) { tx.EstimateID = nil }, want: ramp.ErrEstimateRequired},
		"estimate missing":  {want: ramps.ErrEstimateNotFound},
		"undecodable quote": {row: &models.RampEstimate{RawResponse: "{not json"}, msg: "decode estimate snapshot"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
			settling := h.withSettler()
			h.estimateRepo.ExpectedCalls = nil
			h.estimateRepo.EXPECT().FindByCustomerAndID(userID, settleEstimateID).Return(c.row, nil).Maybe()
			tx := claimedOfframp()
			if c.mutate != nil {
				c.mutate(&tx)
			}
			h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()

			err := h.offramp.Settle(context.Background(), tx.ID)
			if c.want != nil {
				require.ErrorIs(t, err, c.want)
			} else {
				require.ErrorContains(t, err, c.msg)
			}
			require.Empty(t, settling.requests)
		})
	}
}

// The payout happened but its record did not: the error surfaces for the job
// to retry, nobody is told the order moved, and the retry presents the same
// idempotency key so the provider replays the payout instead of repeating it.
func TestSettleOfframpRetriesAFailedWriteUnderTheSameKey(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := h.withSettler()
	h.withSettleEstimate()
	tx := claimedOfframp()
	h.txRepo.EXPECT().FindByID(tx.ID).RunAndReturn(func(uint) (*models.Transaction, error) {
		// Each read is a fresh row, as the database would answer it.
		row := claimedOfframp()
		return &row, nil
	}).Times(4)
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(errors.New("db down")).Once()

	err := h.offramp.Settle(context.Background(), tx.ID)
	require.ErrorContains(t, err, "db down")
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)

	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()
	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))

	require.Len(t, settling.requests, 2)
	require.Equal(t, settling.requests[0].IdempotencyKey, settling.requests[1].IdempotencyKey)
}
