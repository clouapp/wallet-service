package ramps_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/goravel/framework/testing/mock"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// fundingProbe is an Ozav adapter whose balance answer the test sets, and which
// records every funding question it was asked: the amount it is asked to cover
// is what the allocation across a user's orders comes down to.
type fundingProbe struct {
	*mocks.MockProviderAdapter
	asked  []ramp.OfframpFundingRequest
	funded bool
	err    error
}

func (p *fundingProbe) OfframpFundingInstructions(
	context.Context, ramp.CustomerRef, ramp.OfframpFundingRequest,
) (*ramp.OfframpFundingInstructions, error) {
	return &ramp.OfframpFundingInstructions{ToAddress: "dep"}, nil
}

func (p *fundingProbe) OfframpFundingSatisfied(_ context.Context, _ ramp.CustomerRef, req ramp.OfframpFundingRequest) (bool, error) {
	p.asked = append(p.asked, req)
	return p.funded, p.err
}

func (p *fundingProbe) SettleOfframp(context.Context, ramp.CustomerRef, ramp.OfframpSettleRequest) (*ramp.RampOrder, error) {
	return nil, errors.New("fundingProbe: settle not expected")
}

const settleEstimateID = uint(99)

// withOzavAdapter rebuilds the router and the off-ramp service around adapter
// in the Ozav slot, the way TestSettleOfframpPostsWhenFunded does.
func (h *harness) withOzavAdapter(adapter ramp.ProviderAdapter) {
	h.factory = ramp.NewFactory(h.adapter, adapter)
	h.router = ramp.NewRouter(ramp.RouterDeps{Factory: h.factory, Config: h.cfg, Integrations: h.integ,
		Kyc: h.kycs, Persons: h.persons, AppEnv: h.appEnv})
	h.deps.Router = h.router
	h.deps.Factory = h.factory
	h.offramp = ramps.NewOfframpService(h.deps, h.destinations, h.mfa, h.appEnv, nil)
}

// withPayoutTolerance re-seeds the settings with the payout rail's deposit
// match tolerance answering tolerance/err. It is registered before the rail
// stubs because testify answers from the first matching expectation.
func (h *harness) withPayoutTolerance(tolerance string, err error) {
	h.settings.ExpectedCalls = nil
	h.settings.EXPECT().String(ramp.RailGroup(ramp.RailPixPayout), ramp.SettingOzavDepositMatchTolerance).
		Return(tolerance, err).Maybe()
	h.seedRail(h.lastRail, "", "", "", h.lastProviders...)
	h.stubOzavRailSettings(models.RailPixPayin)
	h.stubOzavRailSettings(models.RailPixPayout)
	for _, code := range h.lastProviders {
		group := ramp.ProviderGroup(code)
		h.settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(true, nil).Maybe()
		h.settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	}
}

// withSettleEstimate answers the estimate every settlement path loads.
func (h *harness) withSettleEstimate() {
	quoteID := "ozav_q_1"
	h.estimateRepo.ExpectedCalls = nil
	h.estimateRepo.EXPECT().FindByCustomerAndID(userID, settleEstimateID).Return(&models.RampEstimate{
		ProviderQuoteID: &quoteID,
		RawResponse: `{"source_amount":"10","source_currency":"USDC","chain":"solana","from_address":"` + wallet +
			`","pix_key":"39053344705","pix_key_type":"cpf","recipient_name":"Luiza","estimated_receive":"50"}`,
	}, nil).Maybe()
}

// awaitingOzavOfframp is an Ozav off-ramp waiting for its payout leg.
func awaitingOzavOfframp(id uint, token, chain, amount string) models.Transaction {
	estimate := settleEstimateID
	idem := "offramp:test-order"
	dest := "ozav_pd_1"
	return models.Transaction{
		Model: orm.Model{ID: id}, CustomerID: userID, UserID: userID, Provider: models.ProviderOzav,
		Type: models.TransactionTypeOfframp, ExternalID: "ord-" + amount,
		Status: models.TxStatusAwaitingFunds, AmountToken: amount, TargetToken: token,
		Chain: support.Ptr(chain), EstimateID: &estimate, IdempotencyKey: &idem,
		ProviderDestinationRef: &dest,
	}
}

// settled is tx as a concurrent settle left it: paid out, so Settle's re-read
// under the lock finds nothing to do.
func settled(tx models.Transaction) *models.Transaction {
	tx.GatewayOrderID = "ozav_tr_done"
	return &tx
}

// --- ApplyManagedWalletDeposit ------------------------------------------------

// The claim is keyed by the deposit's hash, and the match it hands the
// repository accepts only an order in the deposit's token and chain that the
// deposit covers, by no more than the rail's tolerance over it.
func TestApplyManagedWalletDepositClaimsTheOrderTheDepositMatches(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.withPayoutTolerance("0.5", nil)
	dep := ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10.3", Currency: "USDC", Network: "solana", TxHash: "0xhash",
	}
	target := awaitingOzavOfframp(3, "USDC", "solana", "10")
	var match func(*models.Transaction) bool
	h.txRepo.EXPECT().ClaimOfframpDeposit(userID, models.ProviderOzav, "0xhash", testifymock.Anything).
		RunAndReturn(func(_ uint, _, _ string, m func(*models.Transaction) bool) (*models.Transaction, error) {
			match = m
			return &target, nil
		}).Once()

	enqueue, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, dep)

	require.NoError(t, err)
	require.NotNil(t, enqueue)
	require.NotNil(t, match)
	cases := map[string]struct {
		tx   models.Transaction
		want bool
	}{
		"deposit over inside the tolerance": {awaitingOzavOfframp(1, "usdc", "SOLANA", "10"), true},
		"deposit over at the tolerance":     {awaitingOzavOfframp(1, "USDC", "solana", "9.8"), true},
		"deposit over beyond the tolerance": {awaitingOzavOfframp(1, "USDC", "solana", "9.7"), false},
		// A short deposit never pays out the order, tolerance or not.
		"deposit short inside the tolerance": {awaitingOzavOfframp(1, "USDC", "solana", "10.5"), false},
		"deposit short beyond the tolerance": {awaitingOzavOfframp(1, "USDC", "solana", "11"), false},
		"another token":                      {awaitingOzavOfframp(1, "BRS", "solana", "10"), false},
		"another chain":                      {awaitingOzavOfframp(1, "USDC", "ethereum", "10"), false},
		"an order naming no chain":           {chainless(awaitingOzavOfframp(1, "USDC", "", "10")), true},
	}
	for name, c := range cases {
		require.Equal(t, c.want, match(&c.tx), name)
	}

	// The enqueue settles the claimed order (no job configured, so Settle runs
	// inline); a concurrent settle already paid it, so the re-read stops it.
	h.txRepo.EXPECT().FindByID(target.ID).Return(settled(target), nil).Twice()
	enqueue()
}

// chainless is tx without a chain: an order that did not name one.
func chainless(tx models.Transaction) models.Transaction {
	tx.Chain = nil
	return tx
}

// A deposit that names no network is not a mismatch on the network: not every
// source says. The token and the amount still have to match.
func TestApplyManagedWalletDepositWithoutANetworkMatchesOnTokenAndAmount(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	var match func(*models.Transaction) bool
	h.txRepo.EXPECT().ClaimOfframpDeposit(userID, models.ProviderOzav, "0xhash", testifymock.Anything).
		RunAndReturn(func(_ uint, _, _ string, m func(*models.Transaction) bool) (*models.Transaction, error) {
			match = m
			return nil, nil
		}).Once()

	_, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10", Currency: "USDC", TxHash: "0xhash",
	})

	require.NoError(t, err)
	ethereum := awaitingOzavOfframp(1, "USDC", "ethereum", "10")
	require.True(t, match(&ethereum), "any chain")
	brs := awaitingOzavOfframp(1, "BRS", "ethereum", "10")
	require.False(t, match(&brs), "another token")
}

// A redelivered deposit, or one that matches nothing, claims nothing and
// enqueues nothing.
func TestApplyManagedWalletDepositReturnsNothingWhenNoOrderMatches(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.txRepo.EXPECT().ClaimOfframpDeposit(userID, models.ProviderOzav, "0xhash", testifymock.Anything).
		Return(nil, nil).Once()

	enqueue, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10", Currency: "USDC", Network: "solana", TxHash: "0xhash",
	})

	require.NoError(t, err)
	require.Nil(t, enqueue)
}

func TestApplyManagedWalletDepositReportsAFailedClaim(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.txRepo.EXPECT().ClaimOfframpDeposit(userID, models.ProviderOzav, "0xhash", testifymock.Anything).
		Return(nil, errors.New("db down")).Once()

	enqueue, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10", Currency: "USDC", TxHash: "0xhash",
	})

	require.ErrorContains(t, err, "db down")
	require.Nil(t, enqueue)
}

// An unreadable rail is an error rather than a silent exact match: claiming
// with the wrong tolerance would fund the wrong order.
func TestApplyManagedWalletDepositRefusesAnUnreadableTolerance(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.withPayoutTolerance("", errors.New("settings down"))

	enqueue, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10", Currency: "USDC", TxHash: "0xhash",
	})

	require.ErrorContains(t, err, "settings down")
	require.Nil(t, enqueue)
	h.txRepo.AssertNotCalled(t, "ClaimOfframpDeposit",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A deposit without a hash cannot be claimed, so it hands the user's open
// orders to the balance check instead — and only when the caller runs it.
func TestApplyManagedWalletDepositWithoutAHashRunsTheBalanceCheck(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()

	enqueue, err := h.offramp.ApplyManagedWalletDeposit(context.Background(), userID, ramp.ManagedWalletDeposit{
		WalletID: "w_1", Amount: "10", Currency: "USDC", Network: "solana",
	})

	require.NoError(t, err)
	require.NotNil(t, enqueue)
	require.Empty(t, probe.asked, "nothing is checked before the caller commits")
	h.txRepo.AssertNotCalled(t, "ClaimOfframpDeposit",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)

	open := []models.Transaction{
		awaitingOzavOfframp(1, "USDC", "solana", "10"),
		awaitingOzavOfframp(2, "USDC", "solana", "4"),
	}
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).Return(open, nil)
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return(nil, nil)

	enqueue()

	require.Len(t, probe.asked, 2, "every open order is checked, oldest first")
	require.Equal(t, "10.000000", probe.asked[0].Source.Amount)
	require.Equal(t, "14.000000", probe.asked[1].Source.Amount)
}

// --- EnqueueSettleIfFunded -----------------------------------------------------

// The wallet is one per customer: the balance has to cover this order plus the
// older open off-ramps and the undelivered on-ramps in the same token and
// chain. Newer orders and other assets are not this order's concern.
func TestEnqueueSettleIfFundedAsksForTheOlderOrdersAndCustodyToo(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).Return([]models.Transaction{
		awaitingOzavOfframp(1, "USDC", "solana", "5"),
		awaitingOzavOfframp(2, "BRS", "solana", "7"),
		awaitingOzavOfframp(4, "USDC", "ethereum", "8"),
		tx,
		awaitingOzavOfframp(5, "USDC", "solana", "100"),
	}, nil).Once()
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return([]models.Transaction{
		{CustomerID: userID, UserID: userID, Provider: models.ProviderOzav, Type: models.TransactionTypeOnramp,
			TargetToken: "usdc", Chain: support.Ptr("solana"), AmountToken: "2.5"},
		{CustomerID: userID, UserID: userID, Provider: models.ProviderOzav, Type: models.TransactionTypeOnramp,
			TargetToken: "BRS", Chain: support.Ptr("solana"), AmountToken: "50"},
	}, nil).Once()

	queued, err := h.offramp.EnqueueSettleIfFunded(context.Background(), &tx)

	require.NoError(t, err)
	require.False(t, queued)
	require.Len(t, probe.asked, 1)
	require.Equal(t, "17.500000", probe.asked[0].Source.Amount)
}

// A newer order that a deposit already claimed by hash owns that deposit: an
// older unclaimed order is not funded by it, so the balance has to cover both.
// A newer order nobody claimed is still not this order's concern.
func TestEnqueueSettleIfFundedCountsANewerOrdersClaimedDeposit(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	claimed := awaitingOzavOfframp(5, "USDC", "solana", "6")
	claimed.DepositTxSignature = support.Ptr("0xhash")
	claimedOtherAsset := awaitingOzavOfframp(6, "BRS", "solana", "40")
	claimedOtherAsset.DepositTxSignature = support.Ptr("0xother")
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).Return([]models.Transaction{
		tx, claimed, claimedOtherAsset, awaitingOzavOfframp(7, "USDC", "solana", "100"),
	}, nil).Once()
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return(nil, nil).Once()

	queued, err := h.offramp.EnqueueSettleIfFunded(context.Background(), &tx)

	require.NoError(t, err)
	require.False(t, queued)
	require.Len(t, probe.asked, 1)
	require.Equal(t, "16.000000", probe.asked[0].Source.Amount)
}

func TestEnqueueSettleIfFundedSettlesACoveredOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second, funded: true}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).
		Return([]models.Transaction{tx}, nil).Once()
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return(nil, nil).Once()
	// No job configured, so the enqueue is Settle inline; it finds the order
	// already paid out under the lock.
	h.txRepo.EXPECT().FindByID(tx.ID).Return(settled(tx), nil).Twice()

	queued, err := h.offramp.EnqueueSettleIfFunded(context.Background(), &tx)

	require.NoError(t, err)
	require.True(t, queued)
	require.Equal(t, "10.000000", probe.asked[0].Source.Amount)
}

func TestEnqueueSettleIfFundedReportsAFailedListing(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second, funded: true}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).
		Return(nil, errors.New("db down")).Once()

	queued, err := h.offramp.EnqueueSettleIfFunded(context.Background(), &tx)

	require.ErrorContains(t, err, "db down")
	require.False(t, queued)
	require.Empty(t, probe.asked, "a balance check against an unknown total is not made")
}

func TestEnqueueSettleIfFundedIgnoresAnOrderAlreadySettled(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	tx := settled(awaitingOzavOfframp(3, "USDC", "solana", "10"))

	queued, err := h.offramp.EnqueueSettleIfFunded(context.Background(), tx)

	require.NoError(t, err)
	require.False(t, queued)
}

// The read path checks the balance at most once per order per TTL: the app
// polls an awaiting order, and each poll would otherwise reach the provider.
func TestShowChecksAnAwaitingOfframpsFundingOncePerWindow(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	probe := &fundingProbe{MockProviderAdapter: h.second}
	h.withOzavAdapter(probe)
	h.withSettleEstimate()
	facades := mock.Factory()
	facades.Log()
	facades.Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()
	cache := facades.Cache()
	key := "offramp-settle-check:3"
	cache.EXPECT().Add(key, testifymock.Anything, 30*time.Second).Return(true).Once()
	cache.EXPECT().Add(key, testifymock.Anything, 30*time.Second).Return(false).Once()

	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindByCustomerAndExternalID(userID, tx.ExternalID).Return(&tx, nil).Twice()
	h.txRepo.EXPECT().FindOpenOfframpsAwaitingSettlement(userID, models.ProviderOzav).
		Return([]models.Transaction{tx}, nil).Once()
	h.txRepo.EXPECT().FindOnrampsInProviderCustody(userID, models.ProviderOzav).Return(nil, nil).Once()

	_, err := h.offramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)
	_, err = h.offramp.Show(context.Background(), userID, tx.ExternalID)
	require.NoError(t, err)

	require.Len(t, probe.asked, 1)
	cache.AssertExpectations(t)
}

// --- Settle -------------------------------------------------------------------

func TestSettleOfframpDropsAMissingOrder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	h.txRepo.EXPECT().FindByID(uint(3)).Return(nil, nil).Once()

	// Retrying cannot make the row appear, so the job is not failed for it.
	require.NoError(t, h.offramp.Settle(context.Background(), 3))
}

// The re-read under the user's lock is what keeps a second settle from paying
// an order the first one already paid out.
func TestSettleOfframpDoesNothingForAnOrderPaidWhileItWaited(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := &settlingAdapter{MockProviderAdapter: h.second}
	h.withOzavAdapter(settling)
	tx := awaitingOzavOfframp(3, "USDC", "solana", "10")
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Once()
	h.txRepo.EXPECT().FindByID(tx.ID).Return(settled(tx), nil).Once()

	require.NoError(t, h.offramp.Settle(context.Background(), tx.ID))
	require.Nil(t, settling.settled)
}

// A quote that expired before the payout fails the order with its own reason,
// tells the user, and is logged as an error: the crypto is still in the managed
// wallet and only an operator can return or re-price it.
func TestSettleOfframpFailsTheOrderWhenTheQuoteExpired(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	settling := &settlingAdapter{MockProviderAdapter: h.second, err: ramp.ErrEstimateExpired}
	h.withOzavAdapter(settling)
	h.withSettleEstimate()
	logger := &mockslog.Log{}
	mock.Factory().App().On("MakeLog").Return(logger)
	logger.EXPECT().WithContext(testifymock.Anything).Return(logger).Once()
	logger.EXPECT().With(testifymock.Anything).Return(logger).Once()
	logger.EXPECT().Error("ramps: offramp quote expired before payout — the user's crypto is still in the managed wallet").Once()

	tx := claimedOfframp()
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()
	h.txRepo.EXPECT().Update(&tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	err := h.offramp.Settle(context.Background(), tx.ID)

	require.ErrorIs(t, err, ramp.ErrQuoteExpiredSettlement)
	require.Equal(t, models.TxStatusFailed, tx.Status)
	require.Equal(t, "quote_expired_settlement", *tx.FailureReason)
	require.NotNil(t, settling.settled)
	logger.AssertExpectations(t)
}

// Ozav can quote an amount and then refuse the payout under its own floor
// (02/11/2026: a 20 USDC quote, then AMOUNT_BELOW_MINIMUM "185 USDC" on POST
// /transfers). Retrying cannot change the answer, so the order fails with its
// own reason instead of staying awaiting and re-posting on every poll.
func TestSettleOfframpFailsTheOrderTheProviderRefusesAsBelowItsMinimum(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderOzav)
	refusal := fmt.Errorf("ozav: POST /transfers: upstream rejected (AMOUNT_BELOW_MINIMUM): %w", ramp.ErrAmountBelowMinimum)
	settling := &settlingAdapter{MockProviderAdapter: h.second, err: refusal}
	h.withOzavAdapter(settling)
	h.withSettleEstimate()
	logger := &mockslog.Log{}
	mock.Factory().App().On("MakeLog").Return(logger)
	logger.EXPECT().WithContext(testifymock.Anything).Return(logger).Once()
	logger.EXPECT().With(testifymock.Anything).Return(logger).Once()
	logger.EXPECT().Error("ramps: provider refused the offramp payout as below its minimum — the user's crypto is still in the managed wallet").Once()

	tx := claimedOfframp()
	h.txRepo.EXPECT().FindByID(tx.ID).Return(&tx, nil).Twice()
	h.txRepo.EXPECT().Update(&tx).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(userID, testifymock.Anything).Once()

	err := h.offramp.Settle(context.Background(), tx.ID)

	require.ErrorIs(t, err, ramp.ErrAmountBelowMinimum)
	require.Equal(t, models.TxStatusFailed, tx.Status)
	require.Equal(t, "amount_below_minimum", *tx.FailureReason)
	logger.AssertExpectations(t)
}
