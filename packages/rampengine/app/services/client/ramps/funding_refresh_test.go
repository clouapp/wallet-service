package ramps_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const refreshedFingerprint = "0000000000000000000000000000000000000000000000000000000000000001"

// fakeRefreshRepository gives the authorization repository the semantics of
// ReplaceAction (a compare-and-swap on state and revision) and FailAndExpire
// (the authorization fails and the order expires together), over the rows the
// create stored. contended runs before a swap is stored.
func (h *bankCreateHarness) fakeRefreshRepository(contended func()) {
	h.funding.EXPECT().ReplaceAction(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, previous int) (bool, error) {
			if contended != nil {
				contended()
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, stored := range h.auths {
				if stored.ID != a.ID {
					continue
				}
				if stored.State != models.FundingAuthPrepared || stored.ActionRevision != previous {
					return false, nil
				}
				stored.ProviderStateCiphertext, stored.ActionPayloadCiphertext = a.ProviderStateCiphertext, a.ActionPayloadCiphertext
				stored.UnsignedActionFingerprint, stored.ExpiresAt = a.UnsignedActionFingerprint, a.ExpiresAt
				stored.ActionRevision = previous + 1
				a.ActionRevision = previous + 1
				return true, nil
			}
			return false, nil
		}).Maybe()
	h.funding.EXPECT().FailAndExpire(testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(a *models.OfframpFundingAuthorization, code string) (bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			for _, stored := range h.auths {
				if stored.ID != a.ID || stored.State != models.FundingAuthPrepared {
					continue
				}
				stored.State, stored.FailureCode = models.FundingAuthFailed, &code
				for _, row := range h.rows {
					if row.ID == a.TransactionID {
						row.Status = models.TxStatusExpired
					}
				}
				return true, nil
			}
			return false, nil
		}).Maybe()
}

// placed creates the order and answers its external id, with the action it
// holds expiring in left.
func (h *bankCreateHarness) placed(t *testing.T, left time.Duration) string {
	t.Helper()
	view, _, err := h.create()
	require.NoError(t, err)
	h.mu.Lock()
	h.auths[0].ExpiresAt = time.Now().Add(left)
	h.mu.Unlock()
	h.prepares.Store(0)
	h.published.Store(0)
	return view.ID
}

func (h *bankCreateHarness) refresh(orderID string) (*ramps.OrderView, error) {
	return h.offramp.RefreshFundingAction(context.Background(), user, person, orderID)
}

// An action about to lapse is prepared again from the same quote: the order
// answers revision 2, a new fingerprint and a new expiry.
func TestRefreshFundingActionPreparesAgainWhenTheActionIsAboutToLapse(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, 5*time.Second)
	quoteExpiry := *h.estimate.ExpiresAt
	h.prepared = func() *ramp.PreparedOfframpFunding {
		p := preparedFunding()
		p.MessageFingerprint = refreshedFingerprint
		return p
	}

	view, err := h.refresh(id)

	require.NoError(t, err)
	require.Equal(t, int32(1), h.prepares.Load())
	h.prepare.AssertCalled(t, "PrepareOfframp", testifymock.Anything,
		ramp.CustomerRef{CustomerID: userID, ExternalID: blindpayCusID},
		ramp.OfframpPrepareRequest{
			ProviderQuoteID: "qu_1", OwnerAddress: wallet, Token: "usdc", Chain: "solana",
			SourceAmount: "100.00", QuoteExpiresAt: quoteExpiry,
		})
	require.NotNil(t, view.FundingAction)
	require.Equal(t, 2, view.FundingAction.ActionRevision)
	require.True(t, view.FundingAction.ExpiresAt.After(time.Now().Add(30*time.Second)))
	auth := h.storedAuths()[0]
	require.Equal(t, refreshedFingerprint, auth.UnsignedActionFingerprint)
	require.Equal(t, 2, auth.ActionRevision)
	require.Equal(t, models.FundingAuthPrepared, auth.State)
	require.NotContains(t, auth.ActionPayloadCiphertext, base64.StdEncoding.EncodeToString([]byte(unsignedTx)))
}

// The answer after a swap is the order as it stands then, not the row read
// before the prepare: an order expired meanwhile answers expired, with no
// action to sign.
func TestRefreshFundingActionAnswersTheOrderReloadedAfterTheSwap(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		h.rows[0].Status = models.TxStatusExpired
	})
	id := h.placed(t, 5*time.Second)

	view, err := h.refresh(id)

	require.NoError(t, err)
	require.Equal(t, int32(1), h.prepares.Load())
	require.Equal(t, 2, h.storedAuths()[0].ActionRevision, "the swap landed")
	require.Equal(t, models.TxStatusExpired, view.Status)
	require.Nil(t, view.FundingAction)
}

// An action with time left is the answer, and the provider is not asked.
func TestRefreshFundingActionKeepsAnActionThatStillLives(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, time.Minute)
	before := h.storedAuths()[0]

	view, err := h.refresh(id)

	require.NoError(t, err)
	require.Zero(t, h.prepares.Load())
	require.NotNil(t, view.FundingAction)
	require.Equal(t, 1, view.FundingAction.ActionRevision)
	require.Equal(t, before, h.storedAuths()[0])
}

// A lapsed quote cannot be funded: the authorization fails and the order
// expires together.
func TestRefreshFundingActionExpiresTheOrderWhenTheQuoteLapsed(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, 5*time.Second)
	h.mu.Lock()
	lapsed := time.Now().Add(-time.Second)
	h.estimate.ExpiresAt = &lapsed
	h.mu.Unlock()

	_, err := h.refresh(id)

	require.ErrorIs(t, err, ramp.ErrFundingActionExpired)
	require.Zero(t, h.prepares.Load())
	auth := h.storedAuths()[0]
	require.Equal(t, models.FundingAuthFailed, auth.State)
	require.Equal(t, "quote_expired", *auth.FailureCode)
	require.Equal(t, models.TxStatusExpired, h.storedRows()[0].Status)
	require.Equal(t, int32(1), h.published.Load(), "the expired order is published")
}

// A provider that cannot prepare leaves the authorization as it was: the
// repository is never asked to swap or fail it (its mock has no such
// expectation here), and the app asks again.
func TestRefreshFundingActionLeavesTheAuthorizationWhenThePrepareFails(t *testing.T) {
	h := newBankCreateHarness(t, true)
	id := h.placed(t, 5*time.Second)
	before := h.storedAuths()[0]
	h.prepareErr = ramp.ErrProviderFailed

	_, err := h.refresh(id)

	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.Equal(t, int32(1), h.prepares.Load())
	require.Equal(t, before, h.storedAuths()[0])
	require.Equal(t, models.FundingAuthPrepared, before.State)
	require.Zero(t, h.published.Load())
}

func TestRefreshFundingActionOfAnotherCustomersOrderIsNotFound(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, 5*time.Second)

	_, err := h.offramp.RefreshFundingAction(context.Background(), user, person, id+"-other")

	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
	require.Zero(t, h.prepares.Load())
}

// A Pix order has no funding action to refresh.
func TestRefreshFundingActionOfAPixOrderIsExpired(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, 5*time.Second)
	h.mu.Lock()
	pix := ramp.RailPixPayout.String()
	h.rows[0].Rail = &pix
	h.mu.Unlock()

	_, err := h.refresh(id)

	require.ErrorIs(t, err, ramp.ErrFundingActionExpired)
	require.Zero(t, h.prepares.Load())
}

// A Pix order a provider prepared renews its action as a bank order does.
func TestRefreshFundingActionOfAPreparedPixOrderRenewsTheAction(t *testing.T) {
	h := newBankCreateHarness(t, true)
	h.fakeRefreshRepository(nil)
	id := h.placed(t, 5*time.Second)
	h.mu.Lock()
	pix, prepared := ramp.RailPixPayout.String(), models.FundingModePrepared
	h.rows[0].Rail = &pix
	h.rows[0].FundingMode = &prepared
	h.mu.Unlock()

	view, err := h.refresh(id)

	require.NoError(t, err)
	require.Equal(t, int32(1), h.prepares.Load())
	require.NotNil(t, view.FundingAction)
}

// Two refreshes at once prepare once: the funding lock serializes them, and
// the second finds the first's fresh action. Without it both prepare and one
// swap loses the compare-and-swap.
func TestRefreshFundingActionConcurrentRefreshesPrepareOnce(t *testing.T) {
	for name, c := range map[string]struct {
		serialize bool
		want      int32
	}{
		"serialized":               {serialize: true, want: 1},
		"control: lock admits all": {serialize: false, want: 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newBankCreateHarness(t, true)
			pair := race.New()
			h.fakeRefreshRepository(pair.Write)
			id := h.placed(t, 5*time.Second)
			h.deps.Locker = pair.Locker(t, c.serialize)
			h.rebuild(t, h.deps)

			errs := pair.Run(2, func() error {
				_, err := h.refresh(id)
				return err
			})

			for _, err := range errs {
				require.NoError(t, err)
			}
			require.Equal(t, c.want, h.prepares.Load())
			require.Equal(t, 2, h.storedAuths()[0].ActionRevision, "one swap lands, whoever prepared")
		})
	}
}
