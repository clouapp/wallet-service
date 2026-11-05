package kyc_test

import (
	"context"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/broadcast"
)

// A refresh that keeps the status but changes what the screen shows — a new
// rejection category, a step the provider now asks for — is news to an open
// screen. Only a status change used to be announced, so a corrigible
// rejection that gained a category never reached the wizard showing it.
func TestRefreshAnnouncesAChangeTheOverviewShowsOnTheSameStatus(t *testing.T) {
	cases := map[string]*ramp.KycState{
		"a new rejection category": {Status: models.KycStatusRejected,
			RejectionCategories: []string{models.KycRejectionSelfieMismatch}},
		"a new requirement": {Status: models.KycStatusRejected,
			Requirements: []string{"proof_of_address"}},
	}
	for name, state := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			row := kycRow(models.KycStatusRejected)
			h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
				Return(linkedIntegration("cus_1"), nil).Once()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(row, nil).Once()
			h.adapter.EXPECT().RefreshKyc(testifymock.Anything, testifymock.Anything).Return(state, nil).Once()
			h.kycRepo.EXPECT().UpdateIfStatus(row, testifymock.Anything).Return(true, nil).Once()
			h.broadcaster.EXPECT().PublishKyc(uint(7), testifymock.MatchedBy(func(e broadcast.KycEvent) bool {
				return e.KycStatus == models.KycStatusRejected
			})).Once()

			_, err := h.svc.Refresh(context.Background(), 7, models.ProviderBridge)

			require.NoError(t, err)
		})
	}
}
