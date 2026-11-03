package integration

import (
	"context"
	"fmt"
	"sync"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// The orphan: the provider holds a customer this account never got a link to.
// Adopting writes the link active with that id, and asks the provider nothing.
func TestAdoptLinksTheCustomerWhenThereIsNoLink(t *testing.T) {
	svc, repo := newService(t, mocks.NewMockProviderAdapter(t))
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()
	repo.EXPECT().Create(testifymock.MatchedBy(func(row *models.CustomerProviderIntegration) bool {
		return row.CustomerID == 7 && row.Provider == models.ProviderBridge &&
			row.ExternalCustomerID != nil && *row.ExternalCustomerID == "cus_1" &&
			row.Status == models.IntegrationStatusActive && row.Metadata == "{}"
	})).Return(nil).Once()

	link, created, err := svc.Adopt(context.Background(), 7, models.ProviderBridge, "cus_1")

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "cus_1", *link.ExternalCustomerID)
}

// A link that exists is answered untouched: rewriting it is the caller's
// relink, not an adoption.
func TestAdoptAnswersTheLinkThatExists(t *testing.T) {
	svc, repo := newService(t, mocks.NewMockProviderAdapter(t))
	existing := &models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusPending}
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(existing, nil).Once()

	link, created, err := svc.Adopt(context.Background(), 7, models.ProviderBridge, "cus_1")

	require.NoError(t, err)
	require.False(t, created)
	require.Same(t, existing, link)
	repo.AssertNotCalled(t, "Create", testifymock.Anything)
}

func TestAdoptRefusesAnUnknownProvider(t *testing.T) {
	svc, _ := newService(t, mocks.NewMockProviderAdapter(t))

	_, _, err := svc.Adopt(context.Background(), 7, "paypal", "cus_1")

	require.Error(t, err)
}

// An adoption and a Start for the same pair take the same lock, so the pair
// ends with one link. The negative control proves the test sees the race.
func TestAdoptAndStartWriteOneLink(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int
	}{
		"serialized by the start lock":        {serialize: true, want: 1},
		"negative control: a lock that isn't": {serialize: false, want: 2},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			adapter := mocks.NewMockProviderAdapter(t)
			adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
			adapter.EXPECT().StartIntegration(testifymock.Anything, ramp.CustomerRef{CustomerID: 7}).
				Return(&ramp.Integration{Status: models.IntegrationStatusPending}, nil)
			pair := race.New()
			repo := mocks.NewMockCustomerProviderIntegrationRepository(t)
			svc := NewService(ramp.NewFactory(adapter), repo, pair.Locker(t, c.serialize))

			var mu sync.Mutex
			var links []models.CustomerProviderIntegration
			repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
				RunAndReturn(func(uint, string) (*models.CustomerProviderIntegration, error) {
					mu.Lock()
					defer mu.Unlock()
					if len(links) == 0 {
						return nil, nil
					}
					first := links[0]
					return &first, nil
				})
			repo.EXPECT().Create(testifymock.Anything).RunAndReturn(func(row *models.CustomerProviderIntegration) error {
				pair.Write()
				mu.Lock()
				defer mu.Unlock()
				links = append(links, *row)
				return nil
			})

			calls := []func() error{
				func() error {
					_, err := svc.Start(context.Background(), 7, models.ProviderBridge)
					return err
				},
				func() error {
					_, _, err := svc.Adopt(context.Background(), 7, models.ProviderBridge, "cus_1")
					return err
				},
			}
			var next sync.Mutex
			index := 0
			for _, err := range pair.Run(2, func() error {
				next.Lock()
				call := calls[index]
				index++
				next.Unlock()
				return call()
			}) {
				require.NoError(t, err)
			}
			require.Len(t, links, c.want)
		})
	}
}

// A link an operator unlinked while the provider was asked is gone: the
// refresh answers as it would for no link, and does not write it back.
func TestRefreshOfALinkUnlinkedMeanwhileAnswersIntegrationRequired(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)
	customer := "cus_1"
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).
		Return(&models.CustomerProviderIntegration{CustomerID: 7, Provider: models.ProviderBridge, ExternalCustomerID: &customer,
			Status: models.IntegrationStatusActive}, nil).Once()
	adapter.EXPECT().RefreshIntegration(testifymock.Anything, testifymock.Anything).
		Return(&ramp.Integration{Status: models.IntegrationStatusActive}, nil).Once()
	repo.EXPECT().Update(testifymock.Anything).
		Return(fmt.Errorf("user provider integration repo: update: %w", repositories.ErrIntegrationGone)).Once()

	_, err := svc.Refresh(context.Background(), 7, models.ProviderBridge)

	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}
