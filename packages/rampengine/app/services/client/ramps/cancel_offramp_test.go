package ramps_test

import (
	"context"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// cancelingAdapter is the Bridge slot with the optional cancel port, the same
// way deliveringAdapter carries the delivery port on the Ozav slot.
type cancelingAdapter struct {
	*mocks.MockProviderAdapter
	canceled []string
	err      error
}

func (c *cancelingAdapter) CancelOfframp(_ context.Context, _ ramp.CustomerRef, providerOrderID string) error {
	c.canceled = append(c.canceled, providerOrderID)
	return c.err
}

// withCanceler swaps the Bridge slot for one that can cancel and rebuilds the
// router and the off-ramp service on the new factory.
func (h *harness) withCanceler() *cancelingAdapter {
	canceling := &cancelingAdapter{MockProviderAdapter: h.adapter}
	h.factory = ramp.NewFactory(canceling, h.delivering)
	h.router = ramp.NewRouter(ramp.RouterDeps{Factory: h.factory, Config: h.cfg, Integrations: h.integ,
		Kyc: h.kycs, Persons: h.persons, AppEnv: h.appEnv})
	d := h.deps
	d.Factory, d.Router = h.factory, h.router
	h.offramp = ramps.NewOfframpService(d, h.destinations, h.mfa, h.appEnv, nil)
	return canceling
}

func unfundedOfframp(provider string) *models.Transaction {
	return &models.Transaction{
		CustomerID: userID,
		UserID:     userID, Provider: provider,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-unfunded-1",
		Status: models.TxStatusAwaitingFunds, AmountFiat: "19.36", AmountToken: "3.900000",
		TargetToken: models.TargetTokenUSDC, GatewayOrderID: "trf_1",
	}
}

// cancelWrites answers CancelUnfundedOfframp the way the repository does:
// when it wrote, the row it was handed now carries the failure.
func cancelWrites(wrote bool) func(*models.Transaction, string) (bool, error) {
	return func(tx *models.Transaction, reason string) (bool, error) {
		if wrote {
			tx.Status = models.TxStatusFailed
			tx.FailureReason = &reason
		}
		return wrote, nil
	}
}

func TestCancelOfframpWithdrawsAnOrderAwaitingFunds(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	canceling := h.withCanceler()
	tx := unfundedOfframp(models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").Return(tx, nil).Once()
	h.txRepo.EXPECT().CancelUnfundedOfframp(tx, testifymock.MatchedBy(func(reason string) bool { return reason != "" })).
		RunAndReturn(cancelWrites(true)).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	view, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.NoError(t, err)
	require.Equal(t, []string{"trf_1"}, canceling.canceled)
	require.Equal(t, models.TxStatusFailed, view.Status)
	require.Empty(t, h.announced, "the user ended it themselves; telling their devices it failed would be noise")
}

// Withdrawing an order nobody funded moves no money, so it is not gated on
// eligibility: a provider switched off, or a KYC that lapsed after the order,
// must not leave the order open upstream.
func TestCancelOfframpReachesTheProviderAfterItWasSwitchedOff(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.config(models.ProviderBridge, false, models.ProviderModeProduction)
	h.blockKyc(t, models.ProviderBridge, models.KycStatusRejected)
	canceling := h.withCanceler()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").
		Return(unfundedOfframp(models.ProviderBridge), nil).Once()
	h.txRepo.EXPECT().CancelUnfundedOfframp(testifymock.Anything, testifymock.Anything).RunAndReturn(cancelWrites(true)).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.NoError(t, err)
	require.Equal(t, []string{"trf_1"}, canceling.canceled)
}

// Past awaiting_funds the deposit is in, and the order is the provider's to
// settle or return.
func TestCancelOfframpRefusesAnOrderPastAwaitingFunds(t *testing.T) {
	for _, status := range []string{
		models.TxStatusPending, models.TxStatusFundsReceived, models.TxStatusProcessing,
		models.TxStatusCompleted, models.TxStatusFailed,
	} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
			canceling := h.withCanceler()
			tx := unfundedOfframp(models.ProviderBridge)
			tx.Status = status
			h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").Return(tx, nil).Once()

			_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
			require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
			require.Empty(t, canceling.canceled)
		})
	}
}

// Another user's order and an on-ramp read as absent, like Show.
func TestCancelOfframpHidesWhatIsNotTheUsersOfframp(t *testing.T) {
	onramp := unfundedOfframp(models.ProviderBridge)
	onramp.Type = models.TransactionTypeOnramp
	for name, row := range map[string]*models.Transaction{"missing": nil, "onramp": onramp} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
			canceling := h.withCanceler()
			h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").Return(row, nil).Once()

			_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
			require.ErrorIs(t, err, ramps.ErrOrderNotFound)
			require.Empty(t, canceling.canceled)
		})
	}
}

// A refused cancel leaves the order as the provider still holds it: open.
func TestCancelOfframpKeepsTheOrderWhenTheProviderRefuses(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	canceling := h.withCanceler()
	canceling.err = ramp.ErrProviderFailed
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").
		Return(unfundedOfframp(models.ProviderBridge), nil).Once()

	_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	h.txRepo.AssertNotCalled(t, "CancelUnfundedOfframp", testifymock.Anything, testifymock.Anything)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

func TestCancelOfframpRefusesAProviderThatCannotCancel(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").
		Return(unfundedOfframp(models.ProviderOzav), nil).Once()

	_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

// Bridge announces the cancel as transfer.updated, and that event can land
// first. The row it wrote is the answer; publishing again would repeat it.
func TestCancelOfframpAnswersTheRowAWebhookWroteFirst(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.withCanceler()
	settled := unfundedOfframp(models.ProviderBridge)
	settled.Status = models.TxStatusFailed
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").
		Return(unfundedOfframp(models.ProviderBridge), nil).Once()
	h.txRepo.EXPECT().CancelUnfundedOfframp(testifymock.Anything, testifymock.Anything).RunAndReturn(cancelWrites(false)).Once()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").Return(settled, nil).Once()

	view, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.NoError(t, err)
	require.Equal(t, models.TxStatusFailed, view.Status)
	h.broadcaster.AssertNotCalled(t, "Publish", testifymock.Anything, testifymock.Anything)
}

// A deposit claimed by hash is on its way to the managed wallet: the order is
// the deposit's now, and withdrawing it would strand that money.
func TestCancelOfframpRefusesAnOrderADepositClaimed(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	canceling := h.withCanceler()
	tx := unfundedOfframp(models.ProviderBridge)
	tx.DepositTxSignature = support.Ptr("0xhash")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").Return(tx, nil).Once()

	_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
	require.Empty(t, canceling.canceled)
	h.txRepo.AssertNotCalled(t, "CancelUnfundedOfframp", testifymock.Anything, testifymock.Anything)
}

// A cancel and a settlement of the same customer take the same lock: one
// cannot pay out an order the other is withdrawing.
func TestCancelOfframpHoldsTheCustomersSettleLock(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	locker := &recordingLocker{}
	h.deps.Locker = locker
	h.withCanceler()
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, "ord-unfunded-1").
		Return(unfundedOfframp(models.ProviderBridge), nil).Once()
	h.txRepo.EXPECT().CancelUnfundedOfframp(testifymock.Anything, testifymock.Anything).RunAndReturn(cancelWrites(true)).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	_, err := h.offramp.Cancel(context.Background(), userID, "ord-unfunded-1")
	require.NoError(t, err)
	require.Equal(t, []string{"offramp-settle:customer:7"}, locker.keys)
}
