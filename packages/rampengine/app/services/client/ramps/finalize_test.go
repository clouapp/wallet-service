package ramps_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const (
	// signedFundingTx is base64("signed-solana-transaction").
	signedFundingTx = "c2lnbmVkLXNvbGFuYS10cmFuc2FjdGlvbg=="
	clientKey       = "client-key-0001"
	authExternalID  = "6f1c2d4e-0000-4000-8000-000000000001"
	payoutID        = "po_000000000001"
)

// finalizingAdapter is BlindPay with every port a finalize type-asserts for.
type finalizingAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockOfframpPrepareAdapter
	*mocks.MockOfframpFinalizeAdapter
	*mocks.MockSignedOfframpActionValidator
	*mocks.MockPayoutLegNamer
}

// legLessAdapter can finalize and validate but cannot name the payout leg.
type legLessAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockOfframpPrepareAdapter
	*mocks.MockOfframpFinalizeAdapter
	*mocks.MockSignedOfframpActionValidator
}

// statusError is an unrecognized answer that exposes an HTTP status, which
// the service must not read: only the adapter's marks decide.
type statusError struct{ status int }

func (e statusError) Error() string   { return fmt.Sprintf("upstream %d", e.status) }
func (e statusError) HTTPStatus() int { return e.status }

// trackingLocker counts the holders of the lock, so the provider fake can
// tell whether the POST runs inside it.
type trackingLocker struct{ held *atomic.Int32 }

func (l trackingLocker) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	l.held.Add(1)
	defer l.held.Add(-1)
	return fn(ctx)
}

// finalizeHarness is the bank create harness with an order placed and the
// finalize transitions faked with the repository's compare-and-swap
// semantics.
type finalizeHarness struct {
	*bankCreateHarness
	finalizer *mocks.MockOfframpFinalizeAdapter
	validator *mocks.MockSignedOfframpActionValidator
	legs      *mocks.MockPayoutLegNamer
	orderID   string
	held      atomic.Int32

	// sends counts FinalizeOfframp calls, begins BeginFinalize calls.
	sends, begins atomic.Int32
	// Under mu: what was sent and validated, what the provider answers, the
	// validator's error, the error each result write fails with, and the
	// code the last MarkUnknown carried.
	sent        []ramp.FinalizeRequest
	validated   []ramp.SignedOfframpActionValidationRequest
	answer      func() (*ramp.RampOrder, error)
	validateErr error
	writeErr    map[string]error
	unknownCode string
	// beginHook runs before an intent is stored (the race's barrier).
	beginHook func()
}

func newFinalizeHarness(t *testing.T) *finalizeHarness {
	t.Helper()
	return buildFinalizeHarness(t, func(f *finalizeHarness, base *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
		return &finalizingAdapter{base.blindpay, prepare, f.finalizer, f.validator, f.legs}
	})
}

func buildFinalizeHarness(
	t *testing.T, serve func(*finalizeHarness, *bankHarness, *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter,
) *finalizeHarness {
	t.Helper()
	f := &finalizeHarness{
		finalizer: mocks.NewMockOfframpFinalizeAdapter(t),
		validator: mocks.NewMockSignedOfframpActionValidator(t),
		legs:      mocks.NewMockPayoutLegNamer(t),
		writeErr:  map[string]error{},
		answer: func() (*ramp.RampOrder, error) {
			return &ramp.RampOrder{ProviderOrderID: payoutID, Status: models.TxStatusProcessing}, nil
		},
	}
	f.bankCreateHarness = buildBankCreateHarness(t, func(base *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
		return serve(f, base, prepare)
	})
	f.fakeRefreshRepository(nil)
	f.fakeFinalizeRepository()
	f.fakeFinalizePorts(t)
	f.deps.Locker = trackingLocker{held: &f.held}
	f.rebuild(t, f.deps)

	f.orderID = f.placed(t, time.Minute)
	f.mu.Lock()
	f.auths[0].ExternalID = authExternalID
	f.mu.Unlock()
	return f
}

func (f *finalizeHarness) fakeFinalizePorts(t *testing.T) {
	f.legs.EXPECT().PayoutLeg(testifymock.Anything, "solana", "usdc").Return("solana_devnet", "USDB", nil).Maybe()
	f.validator.EXPECT().ValidateSignedOfframpAction(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, req ramp.SignedOfframpActionValidationRequest) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			f.validated = append(f.validated, req)
			return f.validateErr
		}).Maybe()
	f.finalizer.EXPECT().FinalizeOfframp(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(ctx context.Context, ref ramp.CustomerRef, req ramp.FinalizeRequest) (*ramp.RampOrder, error) {
			f.sends.Add(1)
			f.mu.Lock()
			f.sent = append(f.sent, req)
			auth := *f.auths[0]
			plain := ""
			if auth.SignedPayloadCiphertext != nil {
				plain = f.sealed[*auth.SignedPayloadCiphertext]
			}
			answer := f.answer
			f.mu.Unlock()

			// The intent is committed before the money moves: the row is
			// finalizing, with the signed bytes stored encrypted.
			assert.Equal(t, models.FundingAuthFinalizing, auth.State, "intent committed before the POST")
			if assert.NotNil(t, auth.SignedPayloadCiphertext) {
				assert.NotContains(t, *auth.SignedPayloadCiphertext, signedFundingTx)
			}
			assert.Equal(t, signedFundingTx, plain, "the stored payload decrypts to the signed bytes")
			assert.Zero(t, f.held.Load(), "the funding lock is released before the POST")
			deadline, ok := ctx.Deadline()
			if assert.True(t, ok, "the POST is bounded") {
				assert.WithinDuration(t, time.Now().Add(45*time.Second), deadline, 2*time.Second)
			}
			assert.Equal(t, ramp.CustomerRef{CustomerID: userID, ExternalID: blindpayCusID}, ref)
			return answer()
		}).Maybe()
}

// fakeFinalizeRepository gives the finalize transitions the repository's
// semantics: BeginFinalize swaps prepared@revision to finalizing while the
// order is unfunded; each result swaps finalizing|unknown and moves the order
// in the same write. A write failure set in writeErr fails it, writing nothing.
func (f *finalizeHarness) fakeFinalizeRepository() {
	f.funding.EXPECT().BeginFinalize(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, revision int, in repositories.FinalizeIntent) (bool, error) {
			f.begins.Add(1)
			if f.beginHook != nil {
				f.beginHook()
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			stored, row := f.authRow(a.ID)
			if stored == nil || stored.State != models.FundingAuthPrepared || stored.ActionRevision != revision ||
				(row.Status != models.TxStatusPending && row.Status != models.TxStatusAwaitingFunds) {
				return false, nil
			}
			stored.State = models.FundingAuthFinalizing
			stored.FinalizeClientKeyHash, stored.FinalizeRequestHash = &in.ClientKeyHash, &in.RequestHash
			stored.ProviderFinalizeKey, stored.SignedPayloadCiphertext = &in.ProviderKey, &in.SignedPayloadCiphertext
			stored.PayoutBinding = &in.PayoutBinding
			stored.FinalizeStartedAt, stored.FinalizeLeaseExpiresAt = &in.StartedAt, &in.LeaseUntil
			stored.SignedPayloadRetainUntil = &in.RetainUntil
			a.State = models.FundingAuthFinalizing
			return true, nil
		}).Maybe()
	f.funding.EXPECT().CompleteFinalized(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, providerOrderID, _ string) (bool, error) {
			return f.complete(a, "CompleteFinalized", func(stored *models.OfframpFundingAuthorization, row *models.Transaction) {
				stored.State = models.FundingAuthFinalized
				row.Status, row.GatewayOrderID = models.TxStatusProcessing, providerOrderID
			})
		}).Maybe()
	f.funding.EXPECT().CompleteFailed(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, code, toStatus string) (bool, error) {
			return f.complete(a, "CompleteFailed", func(stored *models.OfframpFundingAuthorization, row *models.Transaction) {
				stored.State, stored.FailureCode = models.FundingAuthFailed, &code
				row.Status = toStatus
			})
		}).Maybe()
	f.funding.EXPECT().MarkUnknown(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, code string, next time.Time) (bool, error) {
			return f.complete(a, "MarkUnknown", func(stored *models.OfframpFundingAuthorization, row *models.Transaction) {
				stored.State, stored.RecoveryNextAttemptAt, stored.FinalizeLeaseExpiresAt = models.FundingAuthUnknown, &next, nil
				row.Status = models.TxStatusProcessing
				f.unknownCode = code
			})
		}).Maybe()
}

// complete is a result write: the swap from finalizing|unknown and the move
// of the order, or nothing.
func (f *finalizeHarness) complete(
	a *models.OfframpFundingAuthorization, method string, apply func(*models.OfframpFundingAuthorization, *models.Transaction),
) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.writeErr[method]; err != nil {
		return false, err
	}
	stored, row := f.authRow(a.ID)
	if stored == nil || (stored.State != models.FundingAuthFinalizing && stored.State != models.FundingAuthUnknown) {
		return false, nil
	}
	apply(stored, row)
	a.State = stored.State
	return true, nil
}

// authRow is the stored authorization and its order; the caller holds mu.
func (f *finalizeHarness) authRow(id uint) (*models.OfframpFundingAuthorization, *models.Transaction) {
	for _, stored := range f.auths {
		if stored.ID != id {
			continue
		}
		for _, row := range f.rows {
			if row.ID == stored.TransactionID {
				return stored, row
			}
		}
	}
	return nil, nil
}

func (f *finalizeHarness) input() dto.FinalizeOfframpDTO {
	return dto.FinalizeOfframpDTO{
		OrderID: f.orderID, SignedTransaction: signedFundingTx, ActionRevision: 1, IdempotencyKey: clientKey,
	}
}

func (f *finalizeHarness) finalize(in dto.FinalizeOfframpDTO) (*ramps.OrderView, error) {
	return f.offramp.Finalize(context.Background(), user, person, in)
}

func (f *finalizeHarness) auth() models.OfframpFundingAuthorization { return f.storedAuths()[0] }
func (f *finalizeHarness) row() models.Transaction                  { return f.storedRows()[0] }

func (f *finalizeHarness) setAnswer(order *ramp.RampOrder, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer = func() (*ramp.RampOrder, error) { return order, err }
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// requireNoFinalizeWrite holds that nothing was sent and the authorization
// and the order are exactly as before.
func (f *finalizeHarness) requireNoFinalizeWrite(t *testing.T, auth models.OfframpFundingAuthorization, row models.Transaction) {
	t.Helper()
	require.Zero(t, f.sends.Load(), "nothing is sent")
	require.Zero(t, f.begins.Load(), "no intent is written")
	require.Equal(t, auth, f.auth())
	require.Equal(t, row, f.row())
	f.funding.AssertNotCalled(t, "FailAndExpire", testifymock.Anything, testifymock.Anything)
}

// The happy path: the intent is written (hashed keys, the provider's key,
// the binding in the provider's spelling, the signed bytes encrypted), then
// one POST outside the lock, then the payout binds and the order processes.
func TestFinalizeSendsOnceAfterTheIntentAndBindsThePayout(t *testing.T) {
	f := newFinalizeHarness(t)
	before := time.Now()

	view, err := f.finalize(f.input())

	require.NoError(t, err)
	require.Equal(t, int32(1), f.sends.Load())
	binding := ramp.OrderBinding{
		ProviderCustomerID: blindpayCusID, BankAccountID: blindpayBankAccountID, QuoteID: "qu_1",
		Network: "solana_devnet", Token: "USDB", SenderAmount: "100.00", SenderWallet: wallet,
	}
	require.Equal(t, []ramp.FinalizeRequest{{
		OrderBinding: binding, SignedTransaction: signedFundingTx, IdempotencyKey: "bank-finalize:" + authExternalID,
	}}, f.sent)
	require.Equal(t, []ramp.SignedOfframpActionValidationRequest{{
		SignedTransaction: signedFundingTx, MessageFingerprint: messageFingerprint, CorrelationState: []byte(correlationState),
	}}, f.validated)

	auth := f.auth()
	require.Equal(t, models.FundingAuthFinalized, auth.State)
	require.Equal(t, sha256Hex(clientKey+":"+authExternalID), *auth.FinalizeClientKeyHash)
	require.Equal(t, sha256Hex(signedFundingTx+":1"), *auth.FinalizeRequestHash)
	require.Equal(t, "bank-finalize:"+authExternalID, *auth.ProviderFinalizeKey)
	var stored ramp.OrderBinding
	require.NoError(t, json.Unmarshal([]byte(*auth.PayoutBinding), &stored))
	require.Equal(t, binding, stored)
	require.NotContains(t, *auth.PayoutBinding, signedFundingTx)
	require.NotContains(t, *auth.SignedPayloadCiphertext, signedFundingTx)
	require.WithinDuration(t, before, *auth.FinalizeStartedAt, 2*time.Second)
	require.WithinDuration(t, before.Add(2*time.Minute), *auth.FinalizeLeaseExpiresAt, 2*time.Second)
	require.WithinDuration(t, before.Add(720*time.Hour), *auth.SignedPayloadRetainUntil, 2*time.Second)

	require.Equal(t, models.TxStatusProcessing, f.row().Status)
	require.Equal(t, payoutID, f.row().GatewayOrderID)
	require.Equal(t, f.orderID, view.ID)
	require.Equal(t, models.TxStatusProcessing, view.Status)
	require.Nil(t, view.FundingAction, "a finalized order has no action to sign")
	require.Equal(t, int32(1), f.published.Load())
}

// The send table (D7). Only the three marks and a 200 with a payout are
// read; everything else, whatever it carries, is unknown and the order
// processes, because the money may be moving.
func TestFinalizeRecordsTheSendOutcome(t *testing.T) {
	type want struct {
		state, txStatus, failure string
	}
	unknown := want{state: models.FundingAuthUnknown, txStatus: models.TxStatusProcessing}
	cases := map[string]struct {
		order *ramp.RampOrder
		err   error
		want  want
	}{
		"200 with a payout": {
			order: &ramp.RampOrder{ProviderOrderID: payoutID},
			want:  want{state: models.FundingAuthFinalized, txStatus: models.TxStatusProcessing},
		},
		"quote expired": {
			err:  fmt.Errorf("blindpay: finalize: %w", ramp.ErrEstimateExpired),
			want: want{state: models.FundingAuthFailed, txStatus: models.TxStatusExpired, failure: "quote_expired"},
		},
		"definitive refusal": {
			err:  fmt.Errorf("blindpay: %w", &ramp.FinalizeRefusedError{Code: "VALIDATION_FAILED"}),
			want: want{state: models.FundingAuthFailed, txStatus: models.TxStatusFailed, failure: "VALIDATION_FAILED"},
		},
		"quote already used": {err: fmt.Errorf("blindpay: finalize: %w", ramp.ErrQuoteAlreadyUsed), want: unknown},
		"unrecognized 400":   {err: statusError{status: 400}, want: unknown},
		"unrecognized 422":   {err: statusError{status: 422}, want: unknown},
		"5xx":                {err: statusError{status: 503}, want: unknown},
		"timeout":            {err: context.DeadlineExceeded, want: unknown},
		"no answer":          {err: ramp.ErrOutcomeUnknown, want: unknown},
		"provider failed":    {err: ramp.ErrProviderFailed, want: unknown},
		"200 not the bound payout": {
			err: fmt.Errorf("blindpay: finalize: %w", ramp.ErrBindingMismatch), want: unknown,
		},
		"unknown error":        {err: errors.New("connection reset by peer"), want: unknown},
		"200 with no order":    {want: unknown},
		"200 with no payout":   {order: &ramp.RampOrder{Status: "processing"}, want: unknown},
		"refusal with no code": {err: &ramp.FinalizeRefusedError{}, want: want{state: models.FundingAuthFailed, txStatus: models.TxStatusFailed, failure: "provider_refused"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			f.setAnswer(c.order, c.err)
			before := time.Now()

			view, err := f.finalize(f.input())

			require.NoError(t, err, "the outcome is the order, not an error")
			require.Equal(t, int32(1), f.sends.Load())
			auth := f.auth()
			require.Equal(t, c.want.state, auth.State)
			require.Equal(t, c.want.txStatus, f.row().Status)
			require.Equal(t, c.want.txStatus, view.Status)
			if c.want.failure != "" {
				require.Equal(t, c.want.failure, *auth.FailureCode)
			}
			if c.want.state == models.FundingAuthUnknown {
				require.WithinDuration(t, before.Add(time.Minute), *auth.RecoveryNextAttemptAt, 2*time.Second)
				require.Nil(t, auth.FinalizeLeaseExpiresAt)
			}
			require.Equal(t, int32(1), f.published.Load())
		})
	}
}

// A used quote is unknown under its own code, so the recovery looks the
// payout up by quote; any other ambiguity is finalize_ambiguous.
func TestFinalizeNamesTheUnknownOutcome(t *testing.T) {
	f := newFinalizeHarness(t)
	f.setAnswer(nil, ramp.ErrQuoteAlreadyUsed)
	_, err := f.finalize(f.input())
	require.NoError(t, err)
	require.Equal(t, "quote_already_used", f.unknownCode)

	g := newFinalizeHarness(t)
	g.setAnswer(nil, errors.New("boom"))
	_, err = g.finalize(g.input())
	require.NoError(t, err)
	require.Equal(t, "finalize_ambiguous", g.unknownCode)
}

// A result that cannot be written is an error to the app, and the
// authorization stays finalizing for the recovery to close: a failed write
// is never taken for success.
func TestFinalizeLeavesTheAuthorizationFinalizingWhenTheResultCannotBeWritten(t *testing.T) {
	cases := map[string]struct {
		method string
		order  *ramp.RampOrder
		err    error
	}{
		"finalized": {method: "CompleteFinalized", order: &ramp.RampOrder{ProviderOrderID: payoutID}},
		"failed":    {method: "CompleteFailed", err: &ramp.FinalizeRefusedError{Code: "VALIDATION_FAILED"}},
		"expired":   {method: "CompleteFailed", err: ramp.ErrEstimateExpired},
		"unknown":   {method: "MarkUnknown", err: errors.New("boom")},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			f.setAnswer(c.order, c.err)
			f.writeErr[c.method] = errors.New("order expired by another path")

			view, err := f.finalize(f.input())

			require.Error(t, err)
			require.Nil(t, view)
			for _, refusal := range []error{
				ramp.ErrEstimateExpired, ramp.ErrInvalidSignedActionPayload, ramp.ErrFundingActionExpired,
				ramps.ErrInvalidOrderState, ramp.ErrIdempotencyKeyReused,
			} {
				require.NotErrorIs(t, err, refusal, "a failed write is a 5xx, never a client refusal")
			}
			require.Equal(t, int32(1), f.sends.Load())
			require.Equal(t, models.FundingAuthFinalizing, f.auth().State)
			require.Equal(t, models.TxStatusAwaitingFunds, f.row().Status)
		})
	}
}

// A result write that loses its swap (the recovery closed the row first)
// answers the order as it stands.
func TestFinalizeAnswersTheReloadedOrderWhenTheResultSwapIsLost(t *testing.T) {
	f := newFinalizeHarness(t)
	f.mu.Lock()
	f.answer = func() (*ramp.RampOrder, error) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.auths[0].State = models.FundingAuthFinalized
		f.rows[0].Status, f.rows[0].GatewayOrderID = models.TxStatusProcessing, "po_000000000009"
		return &ramp.RampOrder{ProviderOrderID: payoutID}, nil
	}
	f.mu.Unlock()

	view, err := f.finalize(f.input())

	require.NoError(t, err)
	require.Equal(t, models.TxStatusProcessing, view.Status)
	require.Equal(t, "po_000000000009", f.row().GatewayOrderID, "the winner's write stands")
}

// The same key and body again answers the order as it is, without a second
// POST, whatever state the first left it in.
func TestFinalizeReplaysTheSameKeyAndBodyWithoutSending(t *testing.T) {
	for name, first := range map[string]error{"after finalized": nil, "after unknown": errors.New("boom")} {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			if first != nil {
				f.setAnswer(nil, first)
			}
			view, err := f.finalize(f.input())
			require.NoError(t, err)

			again, err := f.finalize(f.input())

			require.NoError(t, err)
			require.Equal(t, int32(1), f.sends.Load(), "the replay sends nothing")
			require.Equal(t, int32(1), f.begins.Load())
			require.Equal(t, view.ID, again.ID)
			require.Equal(t, view.Status, again.Status)
		})
	}
}

// The same key with another body is refused, whether the transaction or the
// revision changed; nothing is sent again.
func TestFinalizeRefusesTheSameKeyWithAnotherBody(t *testing.T) {
	f := newFinalizeHarness(t)
	_, err := f.finalize(f.input())
	require.NoError(t, err)

	otherTx := f.input()
	otherTx.SignedTransaction = "b3RoZXItdHJhbnNhY3Rpb24="
	_, err = f.finalize(otherTx)
	require.ErrorIs(t, err, ramps.ErrIdempotencyKeyReused)
	require.ErrorIs(t, err, ramp.ErrIdempotencyKeyReused)

	otherRevision := f.input()
	otherRevision.ActionRevision = 2
	_, err = f.finalize(otherRevision)
	require.ErrorIs(t, err, ramps.ErrIdempotencyKeyReused)

	require.Equal(t, int32(1), f.sends.Load())
	require.Equal(t, models.FundingAuthFinalized, f.auth().State)
}

// Another key once the authorization left prepared is an invalid state, not
// a second payout.
func TestFinalizeRefusesAnotherKeyOnceFinalizeStarted(t *testing.T) {
	f := newFinalizeHarness(t)
	_, err := f.finalize(f.input())
	require.NoError(t, err)

	other := f.input()
	other.IdempotencyKey = "client-key-0002"
	_, err = f.finalize(other)

	require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
	require.Equal(t, int32(1), f.sends.Load())
}

func TestFinalizeRequiresAnIdempotencyKey(t *testing.T) {
	for name, key := range map[string]string{"empty": "", "longer than 128": strings.Repeat("k", 129)} {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			auth, row := f.auth(), f.row()
			in := f.input()
			in.IdempotencyKey = key

			_, err := f.finalize(in)

			require.ErrorIs(t, err, ramps.ErrIdempotencyKeyRequired)
			require.ErrorIs(t, err, ramp.ErrInvalidIdempotencyKey)
			f.requireNoFinalizeWrite(t, auth, row)
		})
	}
}

// A key of exactly 128 characters is accepted.
func TestFinalizeAcceptsAKeyOf128Characters(t *testing.T) {
	f := newFinalizeHarness(t)
	in := f.input()
	in.IdempotencyKey = strings.Repeat("k", 128)

	_, err := f.finalize(in)

	require.NoError(t, err)
	require.Equal(t, int32(1), f.sends.Load())
}

func TestFinalizeOfAnotherCustomersOrderIsNotFound(t *testing.T) {
	f := newFinalizeHarness(t)
	auth, row := f.auth(), f.row()
	in := f.input()
	in.OrderID += "-other"

	_, err := f.finalize(in)

	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
	f.requireNoFinalizeWrite(t, auth, row)
}

// Only a bank payout is finalized.
func TestFinalizeOfAPixOrderIsAnInvalidState(t *testing.T) {
	f := newFinalizeHarness(t)
	f.mu.Lock()
	pix := ramp.RailPixPayout.String()
	f.rows[0].Rail = &pix
	f.mu.Unlock()
	auth, row := f.auth(), f.row()

	_, err := f.finalize(f.input())

	require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
	f.requireNoFinalizeWrite(t, auth, row)
}

// A Pix payout a provider prepared is finalized like a bank payout: the gate
// reads how the order is funded, not which rail it is on.
func TestFinalizeOfAPreparedPixOrderSendsOnce(t *testing.T) {
	f := newFinalizeHarness(t)
	f.mu.Lock()
	pix, prepared := ramp.RailPixPayout.String(), models.FundingModePrepared
	f.rows[0].Rail = &pix
	f.rows[0].FundingMode = &prepared
	f.mu.Unlock()

	view, err := f.finalize(f.input())

	require.NoError(t, err)
	require.Equal(t, int32(1), f.sends.Load())
	require.Equal(t, models.TxStatusProcessing, view.Status)
	require.Equal(t, models.FundingAuthFinalized, f.auth().State)
}

// An order no longer awaiting its funds, or an authorization no longer
// prepared, is an invalid state.
func TestFinalizeRefusesAnOrderThatCannotBeFunded(t *testing.T) {
	cases := map[string]func(f *finalizeHarness){
		"order expired": func(f *finalizeHarness) { f.rows[0].Status = models.TxStatusExpired },
		"authorization failed": func(f *finalizeHarness) {
			f.auths[0].State = models.FundingAuthFailed
			f.auths[0].FailureCode = ptr("quote_expired")
		},
		"authorization finalized": func(f *finalizeHarness) { f.auths[0].State = models.FundingAuthFinalized },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			f.mu.Lock()
			mutate(f)
			f.mu.Unlock()
			auth, row := f.auth(), f.row()

			_, err := f.finalize(f.input())

			require.ErrorIs(t, err, ramps.ErrInvalidOrderState)
			f.requireNoFinalizeWrite(t, auth, row)
		})
	}
}

// The kill switch closes the finalize, before anything is written: not even
// a lapsed quote is expired while the rail is off.
func TestFinalizeRefusesWhileTheRailIsSwitchedOff(t *testing.T) {
	for name, lapsed := range map[string]bool{"live quote": false, "lapsed quote": true} {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			f.mu.Lock()
			f.flagOn = false
			if lapsed {
				past := time.Now().Add(-time.Second)
				f.estimate.ExpiresAt = &past
			}
			f.mu.Unlock()
			auth, row := f.auth(), f.row()

			_, err := f.finalize(f.input())

			require.ErrorIs(t, err, ramp.ErrRailDisabled)
			f.requireNoFinalizeWrite(t, auth, row)
			require.Empty(t, f.validated)
		})
	}
}

// A signature of another revision of the action is a stale action.
func TestFinalizeRefusesAnotherRevisionOfTheAction(t *testing.T) {
	f := newFinalizeHarness(t)
	auth, row := f.auth(), f.row()
	in := f.input()
	in.ActionRevision = 2

	_, err := f.finalize(in)

	require.ErrorIs(t, err, ramp.ErrFundingActionExpired)
	f.requireNoFinalizeWrite(t, auth, row)
}

// A lapsed quote fails the authorization and expires the order, as the
// refresh does, and nothing is sent.
func TestFinalizeExpiresTheOrderWhenTheQuoteLapsed(t *testing.T) {
	f := newFinalizeHarness(t)
	f.mu.Lock()
	past := time.Now().Add(-time.Second)
	f.estimate.ExpiresAt = &past
	f.mu.Unlock()

	_, err := f.finalize(f.input())

	require.ErrorIs(t, err, ramp.ErrFundingActionExpired)
	require.Zero(t, f.sends.Load())
	require.Zero(t, f.begins.Load())
	require.Equal(t, models.FundingAuthFailed, f.auth().State)
	require.Equal(t, "quote_expired", *f.auth().FailureCode)
	require.Equal(t, models.TxStatusExpired, f.row().Status)
	require.Equal(t, int32(1), f.published.Load())
}

// An action past its own expiry is stale; the authorization stays prepared
// so the app can refresh it.
func TestFinalizeRefusesAnExpiredAction(t *testing.T) {
	f := newFinalizeHarness(t)
	f.mu.Lock()
	f.auths[0].ExpiresAt = time.Now().Add(-time.Second)
	f.mu.Unlock()
	auth, row := f.auth(), f.row()

	_, err := f.finalize(f.input())

	require.ErrorIs(t, err, ramp.ErrFundingActionExpired)
	f.requireNoFinalizeWrite(t, auth, row)
}

// A provider without every finalize port cannot finalize.
func TestFinalizeNeedsEveryFinalizePort(t *testing.T) {
	cases := map[string]func(*finalizeHarness, *bankHarness, *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter{
		"prepare only": func(_ *finalizeHarness, base *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
			return &preparingAdapter{MockProviderAdapter: base.blindpay, MockOfframpPrepareAdapter: prepare}
		},
		"no payout leg": func(f *finalizeHarness, base *bankHarness, prepare *mocks.MockOfframpPrepareAdapter) ramp.ProviderAdapter {
			return &legLessAdapter{base.blindpay, prepare, f.finalizer, f.validator}
		},
	}
	for name, serve := range cases {
		t.Run(name, func(t *testing.T) {
			f := buildFinalizeHarness(t, serve)
			auth, row := f.auth(), f.row()

			_, err := f.finalize(f.input())

			require.ErrorIs(t, err, ramp.ErrOfframpCreateUnavailable)
			f.requireNoFinalizeWrite(t, auth, row)
		})
	}
}

// A signature that does not match the prepared action is refused; the
// authorization stays prepared, so the wallet can sign again, and the
// provider is never called.
func TestFinalizeRefusesAnInvalidSignatureAndKeepsTheAuthorizationPrepared(t *testing.T) {
	f := newFinalizeHarness(t)
	f.validateErr = fmt.Errorf("blindpay: signed action: %w", ramp.ErrInvalidSignedActionPayload)
	auth, row := f.auth(), f.row()

	_, err := f.finalize(f.input())

	require.ErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
	f.requireNoFinalizeWrite(t, auth, row)
	require.Equal(t, models.FundingAuthPrepared, f.auth().State)
	f.finalizer.AssertNotCalled(t, "FinalizeOfframp", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// Any other validation failure is this side's, retryable: the error is not
// a refusal of the signature, and nothing moves.
func TestFinalizeAnswersAValidationFailureAsRetryable(t *testing.T) {
	f := newFinalizeHarness(t)
	f.validateErr = errors.New("blindpay: signed action: correlation state does not decode")
	auth, row := f.auth(), f.row()

	_, err := f.finalize(f.input())

	require.Error(t, err)
	require.NotErrorIs(t, err, ramp.ErrInvalidSignedActionPayload)
	f.requireNoFinalizeWrite(t, auth, row)
}

// A double tap (the same key and body, at once) sends one POST, and every
// answer is the same order. The funding lock serializes the decision; with
// a lock that admits everyone (the control), the intent's compare-and-swap
// still lets one through, and the others answer as a replay.
func TestFinalizeBurstSendsOnce(t *testing.T) {
	for name, serialize := range map[string]bool{"serialized": true, "control: lock admits all": false} {
		t.Run(name, func(t *testing.T) {
			f := newFinalizeHarness(t)
			pair := race.New()
			f.beginHook = pair.Write
			f.deps.Locker = pair.Locker(t, serialize)
			f.rebuild(t, f.deps)

			var mu sync.Mutex
			ids := map[string]bool{}
			errs := pair.Run(4, func() error {
				view, err := f.finalize(f.input())
				if err == nil {
					mu.Lock()
					ids[view.ID] = true
					mu.Unlock()
				}
				return err
			})

			for _, err := range errs {
				require.NoError(t, err)
			}
			require.Equal(t, int32(1), f.sends.Load(), "exactly one POST")
			require.Equal(t, map[string]bool{f.orderID: true}, ids, "every answer is the same order")
			require.Equal(t, models.FundingAuthFinalized, f.auth().State)
			if serialize {
				require.Equal(t, int32(1), f.begins.Load(), "the lock lets one caller write the intent")
			} else {
				require.GreaterOrEqual(t, f.begins.Load(), int32(2), "the control reaches the intent's swap")
			}
		})
	}
}
