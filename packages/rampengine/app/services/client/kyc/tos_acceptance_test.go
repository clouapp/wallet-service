package kyc_test

import (
	"context"
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// A user who never touched the provider opened no terms page; a failed read is a
// database fault, not tos_session_unknown sending the client round the terms step.
func TestRecordTosAcceptancesChecksAHostedPageWasOpened(t *testing.T) {
	down := errors.New("pq: connection reset")
	cases := map[string]struct {
		readErr error
		want    error
	}{
		"never touched the provider": {want: ramp.ErrTosSessionUnknown},
		"read fails":                 {readErr: down, want: down},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.adapter.EXPECT().RequiresTos().Return(true).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, c.readErr).Once()

			_, err := h.svc.RecordTosAcceptances(context.Background(), h.user, 7, []kyc.TosAcceptance{
				{Provider: models.ProviderBridge, SignedAgreementID: "sa_1"},
			})
			require.ErrorIs(t, err, c.want)
			if c.readErr != nil {
				require.NotErrorIs(t, err, ramp.ErrTosSessionUnknown)
			}
			h.kycRepo.AssertNotCalled(t, "Update", testifymock.Anything)
		})
	}
}
