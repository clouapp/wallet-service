package integration

import (
	"context"
	"errors"
	"sync"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

// passthroughLocker invokes fn immediately. Real lock semantics are covered by
// the AdvisoryLocker integration test.
type passthroughLocker struct{}

func (passthroughLocker) WithLock(ctx context.Context, _ string, fn func(context.Context) error) error {
	return fn(ctx)
}

func newService(t *testing.T, adapter *mocks.MockProviderAdapter) (*Service, *mocks.MockCustomerProviderIntegrationRepository) {
	t.Helper()
	repo := mocks.NewMockCustomerProviderIntegrationRepository(t)
	adapter.EXPECT().Code().Return(models.ProviderBridge).Maybe()
	return NewService(ramp.NewFactory(adapter), repo, passthroughLocker{}), repo
}

func TestStartCreatesPendingIntegration(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Twice()
	adapter.EXPECT().StartIntegration(testifymock.Anything, ramp.CustomerRef{CustomerID: 7}).
		Return(&ramp.Integration{Status: models.IntegrationStatusPending}, nil).Once()
	repo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	got, err := svc.Start(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusPending, got.Status)
	require.Nil(t, got.ExternalCustomerID)
	require.Equal(t, "{}", got.Metadata, "the jsonb column is NOT NULL: an absent metadata is an empty object")
}

// A second tap must return the row the first one created, not open a second
// customer upstream.
func TestStartIsIdempotent(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	existing := &models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
	}
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(existing, nil).Once()

	got, err := svc.Start(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Same(t, existing, got)
	adapter.AssertNotCalled(t, "StartIntegration", testifymock.Anything, testifymock.Anything)
}

func TestStartPersistsExternalCustomerID(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Twice()
	adapter.EXPECT().StartIntegration(testifymock.Anything, testifymock.Anything).
		Return(&ramp.Integration{
			ExternalCustomerID: "cus_1",
			Status:             models.IntegrationStatusActive,
			Metadata:           map[string]any{"source": "existing_customer"},
		}, nil).Once()
	repo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	got, err := svc.Start(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.NotNil(t, got.ExternalCustomerID)
	require.Equal(t, "cus_1", *got.ExternalCustomerID)
	require.Contains(t, got.Metadata, "existing_customer")
}

func TestStartRejectsUnknownProvider(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, _ := newService(t, adapter)

	_, err := svc.Start(context.Background(), 7, "nope")
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

func TestRefreshRequiresIntegration(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()

	_, err := svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
}

// Before a customer exists there is nothing upstream to reconcile against, so
// the call is skipped rather than sent and rejected.
func TestRefreshWithoutCustomerSkipsAdapter(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	pending := &models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusPending,
	}
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(pending, nil).Once()

	got, err := svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusPending, got.Status)
	adapter.AssertNotCalled(t, "RefreshIntegration", testifymock.Anything, testifymock.Anything)
}

func TestRefreshAppliesUpstreamStatus(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	externalID := "cus_1"
	linked := &models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
		ExternalCustomerID: &externalID,
	}
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linked, nil).Once()
	adapter.EXPECT().RefreshIntegration(testifymock.Anything, ramp.CustomerRef{CustomerID: 7, ExternalID: "cus_1"}).
		Return(&ramp.Integration{ExternalCustomerID: "cus_1", Status: models.IntegrationStatusDisabled}, nil).Once()
	repo.EXPECT().Update(linked).Return(nil).Once()

	got, err := svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, models.IntegrationStatusDisabled, got.Status)
}

// An upstream failure must not leave the local row claiming a state nobody
// confirmed.
func TestRefreshDoesNotPersistOnAdapterError(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	externalID := "cus_1"
	linked := &models.CustomerProviderIntegration{
		CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive,
		ExternalCustomerID: &externalID,
	}
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(linked, nil).Once()
	adapter.EXPECT().RefreshIntegration(testifymock.Anything, testifymock.Anything).
		Return(nil, errors.New("upstream down")).Once()

	_, err := svc.Refresh(context.Background(), 7, models.ProviderBridge)
	require.Error(t, err)
	require.Equal(t, models.IntegrationStatusActive, linked.Status)
	repo.AssertNotCalled(t, "Update", testifymock.Anything)
}

func TestListReturnsUserIntegrations(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)

	repo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		{CustomerID: 7, Provider: models.ProviderBridge, Status: models.IntegrationStatusActive},
	}, nil).Once()

	got, err := svc.List(7)
	require.NoError(t, err)
	require.Len(t, got, 1)
}

func TestStartWritesNothingTheProviderRefused(t *testing.T) {
	adapter := mocks.NewMockProviderAdapter(t)
	svc, repo := newService(t, adapter)
	refused := errors.New("bridge: 502")
	repo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Once()
	adapter.EXPECT().StartIntegration(testifymock.Anything, ramp.CustomerRef{CustomerID: 7}).Return(nil, refused).Once()

	_, err := svc.Start(context.Background(), 7, models.ProviderBridge)
	require.ErrorIs(t, err, refused)
	repo.AssertNotCalled(t, "Create", testifymock.Anything)
}

// A double tap links once: the loser re-reads under the lock and answers the
// winner's row. The negative control proves the test sees the race it guards.
func TestConcurrentStartsWriteOneLink(t *testing.T) {
	cases := map[string]struct {
		serialize bool
		want      int
	}{
		"serialized by the lock":              {serialize: true, want: 1},
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

			for _, err := range pair.Run(2, func() error {
				_, err := svc.Start(context.Background(), 7, models.ProviderBridge)
				return err
			}) {
				require.NoError(t, err)
			}
			require.Len(t, links, c.want)
		})
	}
}
