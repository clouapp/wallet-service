package ramps_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	mockslog "github.com/goravel/framework/mocks/log"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const (
	recoveryTxID       uint = 41
	recoveryEstimateID uint = 42
	recoveryAuthID          = "6f1c2d4e-0000-4000-8000-000000000009"
	recoveryPayout          = "po_000000000009"
	recoverySealed          = "sealed-signed"
)

// recoveryProvider is BlindPay as the recovery sees it: the finalize and the
// bound read, answered by the test.
type recoveryProvider struct {
	*mocks.MockProviderAdapter
	h *recoveryHarness
}

func (p *recoveryProvider) FinalizeOfframp(_ context.Context, _ ramp.CustomerRef, req ramp.FinalizeRequest) (*ramp.RampOrder, error) {
	p.h.sends.Add(1)
	p.h.mu.Lock()
	p.h.sent = append(p.h.sent, req)
	answer := p.h.send
	p.h.mu.Unlock()
	return answer()
}

func (p *recoveryProvider) FindOfframpByQuote(context.Context, ramp.CustomerRef, ramp.FindByQuoteRequest) (*ramp.RampOrder, error) {
	p.h.finds.Add(1)
	p.h.mu.Lock()
	answer := p.h.find
	p.h.mu.Unlock()
	return answer()
}

func (p *recoveryProvider) GetBoundOrder(context.Context, ramp.CustomerRef, string, ramp.OrderBinding) (*ramp.RampOrder, error) {
	p.h.mu.Lock()
	answer := p.h.bound
	p.h.mu.Unlock()
	if answer == nil {
		return nil, errors.New("not used here")
	}
	return answer()
}

// recoveryHarness is one authorization the recovery is due to act on, held
// by a fake repository with the real one's compare-and-swap semantics, and a
// clock frozen at now.
type recoveryHarness struct {
	recovery *ramps.FinalizationRecovery
	funding  *mocks.MockOfframpFundingAuthorizationRepository
	now      time.Time

	mu       sync.Mutex
	auth     models.OfframpFundingAuthorization
	tx       models.Transaction
	quoteEnd time.Time
	sent     []ramp.FinalizeRequest
	send     func() (*ramp.RampOrder, error)
	find     func() (*ramp.RampOrder, error)
	bound    func() (*ramp.RampOrder, error)
	// trail is the order the attention mark and the escalation were written in;
	// markErr fails the mark, and marks counts the codes it was asked for.
	trail   []string
	markErr error
	marks   []string
	// writeErr fails the named transition, writing nothing.
	writeErr map[string]error
	// claimRace, when set, holds the first claim until the second arrives.
	claimRace *race.Race

	sends, finds, claims, finalizedWins, escalations atomic.Int32
	unknownCode                                      string
}

func newRecoveryHarness(t *testing.T) *recoveryHarness {
	t.Helper()
	return buildRecoveryHarness(t, nil)
}

// buildRecoveryHarness builds the harness; a non-nil logger is what
// facades.Log() answers, for a test that asserts what was logged.
func buildRecoveryHarness(t *testing.T, logger *mockslog.Log) *recoveryHarness {
	t.Helper()
	facades := mock.Factory()
	if logger != nil {
		facades.App().On("MakeLog").Return(logger)
		logger.On("WithContext", testifymock.Anything).Return(logger).Maybe()
		logger.On("With", testifymock.Anything).Return(logger).Maybe()
	} else {
		facades.Log()
	}
	crypt := facades.Crypt()
	crypt.EXPECT().DecryptString(recoverySealed).Return(signedFundingTx, nil).Maybe()

	now := time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)
	started := now.Add(-5 * time.Minute)
	binding, err := json.Marshal(ramp.OrderBinding{
		ProviderCustomerID: blindpayCusID, BankAccountID: blindpayBankAccountID, QuoteID: "qu_1",
		Network: "solana_devnet", Token: "USDB", SenderAmount: "100.00", SenderWallet: wallet,
	})
	require.NoError(t, err)
	h := &recoveryHarness{
		funding: mocks.NewMockOfframpFundingAuthorizationRepository(t), now: now,
		quoteEnd: now.Add(time.Minute), writeErr: map[string]error{},
		send: func() (*ramp.RampOrder, error) {
			return &ramp.RampOrder{ProviderOrderID: recoveryPayout, Status: models.TxStatusProcessing}, nil
		},
		find: func() (*ramp.RampOrder, error) { return nil, nil },
	}
	next := now.Add(-time.Second)
	h.auth = models.OfframpFundingAuthorization{
		Model: orm.Model{ID: 7}, ExternalID: recoveryAuthID, TransactionID: recoveryTxID,
		Provider: models.ProviderBlindPay, State: models.FundingAuthUnknown,
		ProviderFinalizeKey: ptr("bank-finalize:" + recoveryAuthID), FinalizeStartedAt: &started,
		SignedPayloadCiphertext: ptr(recoverySealed), PayoutBinding: ptr(string(binding)),
		RecoveryNextAttemptAt: &next,
	}
	estimate := uint(recoveryEstimateID)
	h.tx = models.Transaction{
		Model: orm.Model{ID: recoveryTxID}, ExternalID: "order-recovery", CustomerID: userID, UserID: userID,
		Provider: models.ProviderBlindPay, Type: models.TransactionTypeOfframp, Status: models.TxStatusProcessing,
		EstimateID: &estimate,
	}
	h.fakeFunding()

	txRepo := mocks.NewMockTransactionRepository(t)
	txRepo.EXPECT().FindByID(recoveryTxID).RunAndReturn(func(uint) (*models.Transaction, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		row := h.tx
		return &row, nil
	}).Maybe()
	txRepo.EXPECT().MarkAttention(recoveryTxID, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ uint, code string, _ time.Time) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.trail = append(h.trail, "mark")
			h.marks = append(h.marks, code)
			return h.markErr == nil, h.markErr
		}).Maybe()
	estimates := mocks.NewMockRampEstimateRepository(t)
	estimates.EXPECT().FindByCustomerAndID(userID, recoveryEstimateID).RunAndReturn(func(uint, uint) (*models.RampEstimate, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		end := h.quoteEnd
		return &models.RampEstimate{Model: orm.Model{ID: recoveryEstimateID}, ExpiresAt: &end}, nil
	}).Maybe()
	integ := mocks.NewMockCustomerProviderIntegrationRepository(t)
	integ.EXPECT().FindByCustomerAndProvider(testifymock.Anything, models.ProviderBlindPay).Return(&models.CustomerProviderIntegration{
		Provider: models.ProviderBlindPay, Status: models.IntegrationStatusActive,
		ExternalCustomerID: support.Ptr(blindpayCusID), Metadata: "{}",
	}, nil).Maybe()
	blindpay := mocks.NewMockProviderAdapter(t)
	blindpay.EXPECT().Code().Return(models.ProviderBlindPay).Maybe()
	router := ramp.NewRouter(ramp.RouterDeps{
		Factory: ramp.NewFactory(&recoveryProvider{MockProviderAdapter: blindpay, h: h}), Integrations: integ,
	})
	broadcaster := mocks.NewMockBroadcaster(t)
	broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()
	notifier := mocks.NewMockNotifier(t)
	notifier.EXPECT().TransactionUpdated(testifymock.Anything).Maybe()

	h.recovery = ramps.NewFinalizationRecovery(ramps.Deps{
		Router: router, Transactions: txRepo, Estimates: estimates, FundingAuthorizations: h.funding,
		Broadcaster: broadcaster, Members: support.IndividualMembers(t), Notifier: notifier,
	}).WithClock(func() time.Time { return h.now })
	return h
}

// due is the repository's dueForRecovery, on the stored row.
func (h *recoveryHarness) due(now time.Time) bool {
	a := h.auth
	leaseFree := a.FinalizeLeaseExpiresAt == nil || !a.FinalizeLeaseExpiresAt.After(now)
	switch a.State {
	case models.FundingAuthFinalizing:
		return leaseFree
	case models.FundingAuthUnknown:
		return leaseFree && (a.RecoveryNextAttemptAt == nil || !a.RecoveryNextAttemptAt.After(now))
	}
	return false
}

// settle is the transition a result writes: from finalizing|unknown only,
// failing as writeErr says.
func (h *recoveryHarness) settle(name string, from []string, apply func()) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.writeErr[name]; err != nil {
		return false, err
	}
	for _, state := range from {
		if h.auth.State == state {
			apply()
			return true, nil
		}
	}
	return false, nil
}

func (h *recoveryHarness) fakeFunding() {
	owned := []string{models.FundingAuthFinalizing, models.FundingAuthUnknown}
	h.funding.EXPECT().ListDueForRecovery(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(now time.Time, _ int) ([]models.OfframpFundingAuthorization, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			if !h.due(now) {
				return nil, nil
			}
			return []models.OfframpFundingAuthorization{h.auth}, nil
		}).Maybe()
	h.funding.EXPECT().ClaimRecovery(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, now, until time.Time) (bool, error) {
			if h.claimRace != nil {
				h.claimRace.Write()
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			if !h.due(now) {
				return false, nil
			}
			h.claims.Add(1)
			h.auth.RecoveryAttempts++
			h.auth.FinalizeLeaseExpiresAt = &until
			a.RecoveryAttempts, a.FinalizeLeaseExpiresAt = h.auth.RecoveryAttempts, &until
			return true, nil
		}).Maybe()
	h.funding.EXPECT().CompleteFinalized(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, payout, _ string) (bool, error) {
			won, err := h.settle("CompleteFinalized", owned, func() {
				h.auth.State, h.auth.FinalizeLeaseExpiresAt = models.FundingAuthFinalized, nil
				h.tx.Status, h.tx.GatewayOrderID = models.TxStatusProcessing, payout
			})
			if won {
				h.finalizedWins.Add(1)
				a.State = models.FundingAuthFinalized
			}
			return won, err
		}).Maybe()
	h.funding.EXPECT().CompleteFailed(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, code, to string) (bool, error) {
			won, err := h.settle("CompleteFailed", owned, func() {
				h.auth.State, h.auth.FailureCode, h.auth.FinalizeLeaseExpiresAt = models.FundingAuthFailed, &code, nil
				h.tx.Status = to
			})
			if won {
				a.State = models.FundingAuthFailed
			}
			return won, err
		}).Maybe()
	h.funding.EXPECT().MarkUnknown(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, code string, next time.Time) (bool, error) {
			won, err := h.settle("MarkUnknown", owned, func() {
				h.auth.State, h.auth.RecoveryNextAttemptAt, h.auth.FinalizeLeaseExpiresAt = models.FundingAuthUnknown, &next, nil
				h.tx.Status = models.TxStatusProcessing
				h.unknownCode = code
			})
			if won {
				a.State, a.RecoveryNextAttemptAt = models.FundingAuthUnknown, &next
			}
			return won, err
		}).Maybe()
	h.funding.EXPECT().Escalate(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, _ string) (bool, error) {
			won, err := h.settle("Escalate", owned, func() {
				h.auth.State, h.auth.FinalizeLeaseExpiresAt = models.FundingAuthNeedsOperator, nil
			})
			if won {
				h.mu.Lock()
				h.trail = append(h.trail, "escalate")
				h.mu.Unlock()
				h.escalations.Add(1)
				a.State = models.FundingAuthNeedsOperator
			}
			return won, err
		}).Maybe()
}

func (h *recoveryHarness) stored() models.OfframpFundingAuthorization {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.auth
}

func (h *recoveryHarness) order() models.Transaction {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.tx
}

func (h *recoveryHarness) recover(t *testing.T) ramps.RecoveryReport {
	t.Helper()
	report, err := h.recovery.Recover(context.Background(), 50)
	require.NoError(t, err)
	return report
}

// Two recoveries started at once claim the same authorization, and only the
// one that won the lease acts: one send, one payout bound.
func TestRecoverConcurrentRecoveriesActOnce(t *testing.T) {
	h := newRecoveryHarness(t)
	h.claimRace = race.New()

	errs := h.claimRace.Run(2, func() error {
		_, err := h.recovery.Recover(context.Background(), 50)
		return err
	})
	for _, err := range errs {
		require.NoError(t, err)
	}
	assert.EqualValues(t, 1, h.claims.Load(), "one lease")
	assert.EqualValues(t, 1, h.sends.Load(), "one send")
	assert.EqualValues(t, 1, h.finalizedWins.Load())
	assert.Equal(t, models.FundingAuthFinalized, h.stored().State)
	assert.Equal(t, recoveryPayout, h.order().GatewayOrderID)
}

// The resend carries the intent's bytes and key, bounded like the finalize.
func TestRecoverResendsTheSameBytesWithTheSameKey(t *testing.T) {
	h := newRecoveryHarness(t)
	report := h.recover(t)
	assert.Equal(t, 1, report.Finalized)
	require.Len(t, h.sent, 1)
	assert.Equal(t, signedFundingTx, h.sent[0].SignedTransaction)
	assert.Equal(t, "bank-finalize:"+recoveryAuthID, h.sent[0].IdempotencyKey)
	assert.Equal(t, "qu_1", h.sent[0].QuoteID)
	assert.Equal(t, wallet, h.sent[0].SenderWallet)
	lease := h.stored().FinalizeLeaseExpiresAt
	assert.Nil(t, lease, "a concluded row holds no lease")
}

// Anything that is not one of the three marks or a payout is ambiguous, an
// HTTP status included: unknown again, after min(2^n minutes, 30 minutes).
func TestRecoverReadsAnUnrecognizedAnswerAsUnknownWithBackoff(t *testing.T) {
	for _, c := range []struct {
		name     string
		answer   error
		attempts int
		wait     time.Duration
	}{
		{"a 400 no mark names", statusError{status: 400}, 0, 2 * time.Minute},
		{"a 500", statusError{status: 500}, 1, 4 * time.Minute},
		{"a timeout", context.DeadlineExceeded, 3, 16 * time.Minute},
		{"anything", errors.New("boom"), 6, 30 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newRecoveryHarness(t)
			h.auth.RecoveryAttempts = c.attempts
			h.send = func() (*ramp.RampOrder, error) { return nil, c.answer }

			report := h.recover(t)
			assert.Equal(t, 1, report.Rescheduled)
			got := h.stored()
			assert.Equal(t, models.FundingAuthUnknown, got.State)
			assert.Nil(t, got.FailureCode)
			require.NotNil(t, got.RecoveryNextAttemptAt)
			assert.Equal(t, h.now.Add(c.wait), *got.RecoveryNextAttemptAt)
			assert.Equal(t, models.TxStatusProcessing, h.order().Status)
			assert.Zero(t, h.finds.Load(), "an ambiguous send is sent again, not concluded by a lookup")
		})
	}
}

// A resend answered with a payout off the binding concludes nothing by itself:
// the replay of the create may name too little to bind (the sandbox's names no
// customer or bank account), so the listing, which names it all, decides.
func TestRecoverAResendOffItsBindingIsDecidedByTheLookup(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	report := h.recover(t)
	assert.Equal(t, 1, report.Finalized)
	assert.Zero(t, report.Escalated)
	assert.EqualValues(t, 1, h.finds.Load())
	assert.Equal(t, models.FundingAuthFinalized, h.stored().State)
	assert.Equal(t, recoveryPayout, h.order().GatewayOrderID)
}

// The listing showing the quote's payout off the binding too is a real
// mismatch: it goes to an operator, binding nothing.
func TestRecoverAResendAndALookupOffTheBindingEscalate(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	h.find = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }

	assert.Equal(t, 1, h.recover(t).Escalated)
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
	assert.Empty(t, h.order().GatewayOrderID)
	assert.Equal(t, []string{models.AttentionBindingMismatch}, h.marks)
}

// A resend that named a payout is evidence one was created: the listing not
// showing it never closes the finalize as failed, however long ago the quote
// lapsed. It stays unknown for the next attempt, then an operator.
func TestRecoverAResendOffItsBindingIsNeverFailedByAnEmptyLookup(t *testing.T) {
	for name, quoteEnd := range map[string]time.Duration{"quote live": time.Hour, "quote lapsed": -time.Hour} {
		t.Run(name, func(t *testing.T) {
			h := newRecoveryHarness(t)
			h.quoteEnd = h.now.Add(quoteEnd)
			h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }

			report := h.recover(t)
			assert.Equal(t, 1, report.Rescheduled)
			assert.Zero(t, report.Failed)
			assert.EqualValues(t, 1, h.finds.Load())
			got := h.stored()
			assert.Equal(t, models.FundingAuthUnknown, got.State)
			assert.Nil(t, got.FailureCode)
			assert.NotEqual(t, models.TxStatusFailed, h.order().Status)
		})
	}
}

// A refusal of the resend proves only that the resend created nothing: the
// first request may have created the payout. The listing decides.
func TestRecoverARefusedResendFindsThePayoutTheFirstRequestCreated(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, &ramp.FinalizeRefusedError{Code: "VALIDATION_FAILED"} }
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	report := h.recover(t)
	assert.Equal(t, 1, report.Finalized)
	assert.Zero(t, report.Failed)
	assert.EqualValues(t, 1, h.finds.Load())
	assert.Equal(t, recoveryPayout, h.order().GatewayOrderID)
	assert.NotEqual(t, models.TxStatusFailed, h.order().Status)
	assert.NotEqual(t, models.FundingAuthFailed, h.stored().State)
}

// A refusal with the quote still live and no payout listed concludes nothing.
func TestRecoverARefusedResendWithALiveQuoteStaysUnknown(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, &ramp.FinalizeRefusedError{Code: "VALIDATION_FAILED"} }

	assert.Equal(t, 1, h.recover(t).Rescheduled)
	got := h.stored()
	assert.Equal(t, models.FundingAuthUnknown, got.State)
	assert.Nil(t, got.FailureCode)
	assert.NotEqual(t, models.TxStatusFailed, h.order().Status)
}

// A refusal ends failed only once the quote lapsed past the grace and the
// listing answered without the payout.
func TestRecoverARefusedResendFailsOnlyOnceTheQuoteLapsedWithoutAPayout(t *testing.T) {
	h := newRecoveryHarness(t)
	h.quoteEnd = h.now.Add(-time.Hour)
	h.send = func() (*ramp.RampOrder, error) { return nil, &ramp.FinalizeRefusedError{Code: "VALIDATION_FAILED"} }

	assert.Equal(t, 1, h.recover(t).Failed)
	got := h.stored()
	assert.Equal(t, models.FundingAuthFailed, got.State)
	assert.Equal(t, models.FundingFailurePayoutNotCreated, *got.FailureCode)
	assert.Equal(t, models.TxStatusFailed, h.order().Status)
}

// A used quote may be ours: it is looked up, and the payout found is bound.
func TestRecoverLooksUpAUsedQuote(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrQuoteAlreadyUsed) }
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	assert.Equal(t, 1, h.recover(t).Finalized)
	assert.EqualValues(t, 1, h.finds.Load())
	assert.Equal(t, recoveryPayout, h.order().GatewayOrderID)
}

// A 200 that names no payout bound nothing: the payout is looked up.
func TestRecoverLooksUpAReplayWithoutAPayout(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{}, nil }
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	assert.Equal(t, 1, h.recover(t).Finalized)
	assert.EqualValues(t, 1, h.finds.Load())
}

// A lapsed quote on the resend is not "not created" by itself: the listing
// must answer first, and only a quote that lapsed with no payout listed fails.
func TestRecoverAQuoteExpiredAnswerAsksTheListingFirst(t *testing.T) {
	h := newRecoveryHarness(t)
	h.quoteEnd = h.now.Add(-time.Minute)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrEstimateExpired) }
	h.find = func() (*ramp.RampOrder, error) { return nil, errors.New("listing down") }

	assert.Equal(t, 1, h.recover(t).Rescheduled)
	assert.Equal(t, models.FundingAuthUnknown, h.stored().State)

	h.now = h.now.Add(time.Hour)
	h.find = func() (*ramp.RampOrder, error) { return nil, nil }
	assert.Equal(t, 1, h.recover(t).Failed)
	got := h.stored()
	assert.Equal(t, models.FundingFailurePayoutNotCreated, *got.FailureCode)
	assert.Equal(t, models.TxStatusFailed, h.order().Status)
}

// Not found while the quote is still live concludes nothing either.
func TestRecoverNotFoundWithALiveQuoteStaysUnknown(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrQuoteAlreadyUsed) }

	assert.Equal(t, 1, h.recover(t).Rescheduled)
	assert.Equal(t, models.FundingAuthUnknown, h.stored().State)
}

// A result the repository could not write is never taken for success: the row
// is rescheduled, and a pass whose writes all fail leaves it to its lease.
func TestRecoverNeverTakesAFailedWriteForSuccess(t *testing.T) {
	h := newRecoveryHarness(t)
	h.writeErr["CompleteFinalized"] = errors.New("order expired elsewhere")

	report := h.recover(t)
	assert.Zero(t, report.Finalized)
	assert.Equal(t, 1, report.Errors)
	assert.Equal(t, models.FundingAuthUnknown, h.stored().State)
	assert.Empty(t, h.order().GatewayOrderID)

	h.now = h.now.Add(time.Hour)
	h.writeErr["MarkUnknown"] = errors.New("database down")
	report = h.recover(t)
	assert.Zero(t, report.Finalized)
	assert.Equal(t, 1, report.Errors)
	got := h.stored()
	assert.Equal(t, models.FundingAuthUnknown, got.State)
	require.NotNil(t, got.FinalizeLeaseExpiresAt, "left leased: due again once the lease runs out")
	assert.Equal(t, h.now.Add(2*time.Minute), *got.FinalizeLeaseExpiresAt)
}

// The eighth attempt that concludes nothing escalates — a finalizing row is
// made unknown first, since only an unknown one escalates.
func TestRecoverEscalatesTheEighthInconclusiveAttempt(t *testing.T) {
	h := newRecoveryHarness(t)
	h.auth.State, h.auth.RecoveryNextAttemptAt, h.auth.RecoveryAttempts = models.FundingAuthFinalizing, nil, 7
	h.tx.Status = models.TxStatusAwaitingFunds
	h.send = func() (*ramp.RampOrder, error) { return nil, statusError{status: 503} }

	report := h.recover(t)
	assert.Equal(t, 1, report.Escalated)
	got := h.stored()
	assert.Equal(t, models.FundingAuthNeedsOperator, got.State)
	assert.Equal(t, 8, got.RecoveryAttempts)
	assert.Equal(t, models.TxStatusProcessing, h.order().Status, "the money may be moving")
	assert.EqualValues(t, 1, h.escalations.Load())
}

// A finalizing row whose order was moved where no finalize write can move it
// from cannot be made unknown: it is escalated directly, so it stops being
// re-claimed and surfaces in the operator queue, with the ramp.attention line.
func TestRecoverEscalatesAFinalizingRowItCannotMakeUnknown(t *testing.T) {
	logger := &mockslog.Log{}
	logger.On("Errorf", "ramp.attention: finalization could not be made unknown, escalating it directly: %v", testifymock.Anything).Once()
	logger.On("Errorf", "ramp.attention: finalization handed to an operator").Once()
	h := buildRecoveryHarness(t, logger)
	h.auth.State, h.auth.RecoveryNextAttemptAt, h.auth.RecoveryAttempts = models.FundingAuthFinalizing, nil, 7
	h.tx.Status = models.TxStatusExpired
	h.send = func() (*ramp.RampOrder, error) { return nil, statusError{status: 503} }
	h.writeErr["MarkUnknown"] = errors.New("transaction 41 is not in a status a finalize can move")

	report := h.recover(t)
	assert.Equal(t, 1, report.Escalated)
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
	assert.Equal(t, models.TxStatusExpired, h.order().Status, "the order is left as it is")
	assert.EqualValues(t, 1, h.escalations.Load())
	logger.AssertExpectations(t)

	h.now = h.now.Add(time.Hour)
	assert.Zero(t, h.recover(t).Claimed, "an operator's row is not claimed again")
}

// A row already past its attempts (its escalation failed before) escalates
// without sending again.
func TestRecoverEscalatesARowPastItsAttemptsWithoutSending(t *testing.T) {
	h := newRecoveryHarness(t)
	h.auth.RecoveryAttempts = 8

	assert.Equal(t, 1, h.recover(t).Escalated)
	assert.Zero(t, h.sends.Load())
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
}

// The 24 h window runs from finalize_started_at on the injected clock: past
// it nothing is sent, the quote is looked up.
func TestRecoverPastTheWindowLooksUpInsteadOfSending(t *testing.T) {
	h := newRecoveryHarness(t)
	h.now = h.now.Add(24 * time.Hour)
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	assert.Equal(t, 1, h.recover(t).Finalized)
	assert.Zero(t, h.sends.Load())
}

// Without the signed bytes there is nothing to send: the payout is looked up.
func TestRecoverWithoutTheSignedBytesLooksUp(t *testing.T) {
	h := newRecoveryHarness(t)
	h.auth.SignedPayloadCiphertext = nil
	h.find = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: recoveryPayout}, nil }

	assert.Equal(t, 1, h.recover(t).Finalized)
	assert.Zero(t, h.sends.Load())
}

// A lookup that answers another customer's payout escalates, binding nothing.
func TestRecoverEscalatesABindingMismatchFromTheLookup(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrQuoteAlreadyUsed) }
	h.find = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }

	assert.Equal(t, 1, h.recover(t).Escalated)
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
	assert.Empty(t, h.order().GatewayOrderID)
	assert.Equal(t, []string{"mark", "escalate"}, h.trail, "the order is marked before the row is escalated")
	assert.Equal(t, []string{models.AttentionBindingMismatch}, h.marks)
}

// A mark that cannot be written does not stop the escalation.
func TestRecoverEscalatesEvenWhenTheMarkCannotBeWritten(t *testing.T) {
	h := newRecoveryHarness(t)
	h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrQuoteAlreadyUsed) }
	h.find = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	h.markErr = errors.New("database down")

	report := h.recover(t)
	assert.Equal(t, 1, report.Escalated)
	assert.Zero(t, report.Errors)
	assert.Equal(t, models.FundingAuthNeedsOperator, h.stored().State)
}

// Another reason to escalate does not mark the order.
func TestRecoverDoesNotMarkAnExhaustedRow(t *testing.T) {
	h := newRecoveryHarness(t)
	h.auth.RecoveryAttempts = 8
	assert.Equal(t, 1, h.recover(t).Escalated)
	assert.Empty(t, h.marks)
}

// Resolve marks the order of a payout that is off the binding, before the row
// is escalated, and still answers ErrBindingMismatch.
func TestResolveMarksTheOrderOfAPayoutOffTheBinding(t *testing.T) {
	h := newRecoveryHarness(t)
	h.bound = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	row := h.stored()
	h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()

	err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{PayoutID: recoveryPayout})
	assert.ErrorIs(t, err, ramps.ErrBindingMismatch)
	assert.Equal(t, []string{"mark", "escalate"}, h.trail)
	assert.Equal(t, []string{models.AttentionBindingMismatch}, h.marks)
}

// A row already held by an operator is not escalated again, yet the order is
// marked.
func TestResolveMarksTheOrderOfARowAlreadyWithAnOperator(t *testing.T) {
	h := newRecoveryHarness(t)
	h.bound = func() (*ramp.RampOrder, error) { return &ramp.RampOrder{ProviderOrderID: "other"}, nil }
	row := h.stored()
	row.State = models.FundingAuthNeedsOperator
	h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()

	err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{PayoutID: recoveryPayout})
	assert.ErrorIs(t, err, ramps.ErrBindingMismatch)
	assert.Equal(t, []string{"mark"}, h.trail)
}

// A mark that fails does not turn the refusal into another error.
func TestResolveStillRefusesWhenTheMarkCannotBeWritten(t *testing.T) {
	h := newRecoveryHarness(t)
	h.bound = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	h.markErr = errors.New("database down")
	row := h.stored()
	h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()

	err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{PayoutID: recoveryPayout})
	assert.ErrorIs(t, err, ramps.ErrBindingMismatch)
}

// "Not created" with a listing that shows another customer's payout marks too.
func TestResolveNotCreatedMarksTheOrderOnAMismatch(t *testing.T) {
	h := newRecoveryHarness(t)
	h.quoteEnd = h.now.Add(-time.Hour)
	h.find = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrBindingMismatch) }
	row := h.stored()
	h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()

	err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{NotCreated: true})
	assert.ErrorIs(t, err, ramps.ErrBindingMismatch)
	assert.Equal(t, []string{"mark", "escalate"}, h.trail)
}

func TestRecoverFailsOnlyWhenTheSweepCannotList(t *testing.T) {
	mock.Factory().Log()
	funding := mocks.NewMockOfframpFundingAuthorizationRepository(t)
	funding.EXPECT().ListDueForRecovery(testifymock.Anything, 50).Return(nil, errors.New("database down")).Once()
	recovery := ramps.NewFinalizationRecovery(ramps.Deps{FundingAuthorizations: funding})

	_, err := recovery.Recover(context.Background(), 50)
	assert.ErrorContains(t, err, "database down")
}

// A verdict is exactly one of the two, on a row that is open to one: unknown
// or held for an operator, and not leased to a recovery right now.
func TestResolveRefusesWhatIsNotAnOpenVerdict(t *testing.T) {
	h := newRecoveryHarness(t)
	for _, in := range []dto.ResolveFinalizationDTO{{}, {PayoutID: recoveryPayout, NotCreated: true}, {PayoutID: "  "}} {
		assert.ErrorIs(t, h.recovery.Resolve(context.Background(), recoveryAuthID, in), ramps.ErrInvalidFinalizationResolution)
	}

	h.funding.EXPECT().FindByExternalID("missing").Return(nil, nil).Once()
	assert.ErrorIs(t, h.recovery.Resolve(context.Background(), "missing", dto.ResolveFinalizationDTO{NotCreated: true}),
		ramps.ErrFinalizationNotFound)

	lease := h.now.Add(time.Minute)
	for name, row := range map[string]models.OfframpFundingAuthorization{
		"finalizing": {ExternalID: recoveryAuthID, State: models.FundingAuthFinalizing},
		"finalized":  {ExternalID: recoveryAuthID, State: models.FundingAuthFinalized},
		"leased":     {ExternalID: recoveryAuthID, State: models.FundingAuthUnknown, FinalizeLeaseExpiresAt: &lease},
	} {
		h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()
		err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{PayoutID: recoveryPayout})
		assert.ErrorIs(t, err, ramps.ErrFinalizationNotResolvable, name)
	}
	assert.Zero(t, h.sends.Load()+h.finds.Load(), "nothing reached the provider")
}

// "Not created" waits a grace period past the quote's expiry (clock skew, an
// expiry the provider did not state): inside it a missing payout concludes
// nothing, past it the finalize fails.
func TestRecoverWaitsTheGracePastTheQuoteExpiry(t *testing.T) {
	for _, c := range []struct {
		name    string
		expired time.Duration
		lapsed  bool
	}{
		{"just expired", time.Second, false},
		{"inside the grace", 5 * time.Minute, false},
		{"exactly at the grace", 10 * time.Minute, false},
		{"past the grace", 10*time.Minute + time.Second, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			h := newRecoveryHarness(t)
			h.quoteEnd = h.now.Add(-c.expired)
			h.send = func() (*ramp.RampOrder, error) { return nil, fmt.Errorf("x: %w", ramp.ErrEstimateExpired) }

			report := h.recover(t)
			assert.EqualValues(t, 1, h.finds.Load(), "the listing is asked either way")
			if c.lapsed {
				assert.Equal(t, 1, report.Failed)
				assert.Equal(t, models.FundingFailurePayoutNotCreated, *h.stored().FailureCode)
				return
			}
			assert.Equal(t, 1, report.Rescheduled)
			assert.Equal(t, models.FundingAuthUnknown, h.stored().State)
			assert.Equal(t, models.TxStatusProcessing, h.order().Status)
		})
	}
}

// Resolve's "not created" holds to the same comparison.
func TestResolveNotCreatedWaitsTheGracePastTheQuoteExpiry(t *testing.T) {
	h := newRecoveryHarness(t)
	h.quoteEnd = h.now.Add(-5 * time.Minute)
	row := h.stored()
	h.funding.EXPECT().FindByExternalID(recoveryAuthID).Return(&row, nil).Once()

	err := h.recovery.Resolve(context.Background(), recoveryAuthID, dto.ResolveFinalizationDTO{NotCreated: true})
	assert.ErrorIs(t, err, ramps.ErrQuoteStillValid)
	assert.Zero(t, h.finds.Load())
	assert.Equal(t, models.FundingAuthUnknown, h.stored().State)
}
