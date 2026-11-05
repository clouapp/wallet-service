package kyc_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// One provider being unreachable must not cost the user the terms they can
// accept now, and terms signed on opening are not offered again.
func TestStartTosForEnabledAnswersEachSessionStillToSign(t *testing.T) {
	cases := map[string]struct {
		session *ramp.TosSession
		err     error
	}{
		"other provider unreachable":    {err: errors.New("ozav: 502")},
		"other provider signed on open": {session: &ramp.TosSession{URL: "https://ozav.example/tos", SignedAgreementID: "sa_9"}},
	}
	for name, ozav := range cases {
		t.Run(name, func(t *testing.T) {
			h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
			h.seedProviderStates()
			h.expectTosSession(models.ProviderBridge, &ramp.TosSession{URL: "https://bridge.example/tos"}, nil)
			h.expectTosSession(models.ProviderOzav, ozav.session, ozav.err)

			sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{})
			require.NoError(t, err)
			require.Equal(t, []kyc.TosSessionView{
				{Provider: models.ProviderBridge, URL: "https://bridge.example/tos", Mode: kyc.TosModeHosted},
			}, sessions)
		})
	}
}

// expectTosSession stubs one provider's startTos: a row probed, a session asked
// for, and the row written when the provider answered.
func (h *fanOutHarness) expectTosSession(provider string, session *ramp.TosSession, err error) {
	adapter := h.adapters[provider]
	adapter.EXPECT().RequiresTos().Return(true).Once()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), provider).Return(linkedIntegration("cus_1"), nil).Once()
	reads := 1
	if err == nil {
		reads = 2
		h.kycRepo.EXPECT().UpdateIfStatus(testifymock.MatchedBy(func(row *models.CustomerProviderKyc) bool {
			return row.Provider == provider
		}), models.KycStatusNotStarted).Return(true, nil).Once()
	}
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), provider).
		Return(kycRowForProvider(provider, models.KycStatusNotStarted), nil).Times(reads)
	adapter.EXPECT().CreateTosSession(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		Return(session, err).Once()
}

// Nobody to ask is no_provider_available, not an empty success the app would
// read as submitted.
func TestSubmitDocumentsToEnabledRefusesWhenNoProviderIsEnabled(t *testing.T) {
	h := newFanOutHarness(t)
	h.seedProviderStates()

	_, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// The skip filter fails open: an unread state costs a redundant upload, while
// skipping on it could withhold the documents a provider is waiting for.
func TestSubmitDocumentsToEnabledSkipsNobodyWhenTheStatesCannotBeRead(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, errors.New("pq: connection reset")).Once()
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())
	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
}
