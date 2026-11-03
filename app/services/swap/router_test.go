package swap_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/tests/mocks"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func routingRequest() swap.QuoteRequest {
	return swap.QuoteRequest{From: swap.Asset{Symbol: "ETH", Chain: "eth", NetworkID: 1, Decimals: 18, Native: true}, To: swap.Asset{Symbol: "USDC", Chain: "eth", NetworkID: 1, Decimals: 6, Contract: "0x1111111111111111111111111111111111111111"}, FromAddress: "0x2222222222222222222222222222222222222222", ToAddress: "0x3333333333333333333333333333333333333333", AmountRaw: "1000000000000000000"}
}

func routingConfig() swap.RoutingConfig {
	return swap.RoutingConfig{Enabled: true, Preferred: swap.ProviderSwapKit, Fallback: true, SlippageBPS: 100, QuoteTTL: time.Minute, Providers: map[string]swap.ProviderConfig{swap.ProviderSwapKit: {Enabled: true}, swap.ProviderLiFi: {Enabled: true}}}
}

func routingQuote(provider string) *swap.Quote {
	return &swap.Quote{Provider: provider, RouteID: "route-fixture", ExpectedRaw: "1000000", MinimumRaw: "990000", ExpiresAt: time.Now().Add(5 * time.Minute), Payload: json.RawMessage(`{}`)}
}

func TestRouter_Quote_UsesConfiguredPreferenceAndKeepsAnAcceptedRoute(t *testing.T) {
	ctx := context.Background()
	policy := mocks.NewMockSwapPolicy(t)
	primary := mocks.NewMockSwapProvider(t)
	secondary := mocks.NewMockSwapProvider(t)
	primary.On("Name").Return(swap.ProviderSwapKit)
	secondary.On("Name").Return(swap.ProviderLiFi)
	policy.On("Resolve", mock.Anything, "ETH", "eth").Return(routingConfig(), nil)
	primary.On("Quote", mock.Anything, mock.MatchedBy(func(request swap.QuoteRequest) bool { return request.SlippageBPS == 100 }), mock.Anything).Return(routingQuote(swap.ProviderSwapKit), nil).Once()
	factory, err := swap.NewFactory(primary, secondary)
	require.NoError(t, err)
	quote, err := swap.NewRouter(factory, policy).Quote(ctx, routingRequest())
	require.NoError(t, err)
	require.Equal(t, swap.ProviderSwapKit, quote.Provider)
	require.Len(t, quote.Attempts, 1)
	require.Equal(t, "selected", quote.Attempts[0].Outcome)
	require.WithinDuration(t, time.Now().Add(time.Minute), quote.ExpiresAt, time.Second)
	secondary.AssertNotCalled(t, "Quote", mock.Anything, mock.Anything, mock.Anything)
}

func TestRouter_Quote_FallsBackOnlyForAvailabilityOrMissingRoutes(t *testing.T) {
	for _, failure := range []error{swap.ErrNoRoute, swap.ErrUnsupported, swap.ErrProviderUnavailable} {
		t.Run(failure.Error(), func(t *testing.T) {
			policy := mocks.NewMockSwapPolicy(t)
			primary := mocks.NewMockSwapProvider(t)
			secondary := mocks.NewMockSwapProvider(t)
			primary.On("Name").Return(swap.ProviderSwapKit)
			secondary.On("Name").Return(swap.ProviderLiFi)
			policy.On("Resolve", mock.Anything, "ETH", "eth").Return(routingConfig(), nil)
			primary.On("Quote", mock.Anything, mock.Anything, mock.Anything).Return(nil, failure).Once()
			secondary.On("Quote", mock.Anything, mock.Anything, mock.Anything).Return(routingQuote(swap.ProviderLiFi), nil).Once()
			factory, err := swap.NewFactory(primary, secondary)
			require.NoError(t, err)
			quote, err := swap.NewRouter(factory, policy).Quote(context.Background(), routingRequest())
			require.NoError(t, err)
			require.Equal(t, swap.ProviderLiFi, quote.Provider)
			require.Len(t, quote.Attempts, 2)
		})
	}
}

func TestRouter_Quote_DoesNotBypassProviderRejectionsOrInvalidQuotes(t *testing.T) {
	for _, failure := range []error{swap.ErrProviderRejected, swap.ErrInvalidQuote} {
		t.Run(failure.Error(), func(t *testing.T) {
			policy := mocks.NewMockSwapPolicy(t)
			primary := mocks.NewMockSwapProvider(t)
			secondary := mocks.NewMockSwapProvider(t)
			primary.On("Name").Return(swap.ProviderSwapKit)
			secondary.On("Name").Return(swap.ProviderLiFi)
			policy.On("Resolve", mock.Anything, "ETH", "eth").Return(routingConfig(), nil)
			primary.On("Quote", mock.Anything, mock.Anything, mock.Anything).Return(nil, failure).Once()
			factory, err := swap.NewFactory(primary, secondary)
			require.NoError(t, err)
			_, err = swap.NewRouter(factory, policy).Quote(context.Background(), routingRequest())
			require.ErrorIs(t, err, failure)
			secondary.AssertNotCalled(t, "Quote", mock.Anything, mock.Anything, mock.Anything)
		})
	}
}

func TestRouter_Quote_DisabledFallbackAndCancelledRequestsNeverTryAnotherProvider(t *testing.T) {
	policy := mocks.NewMockSwapPolicy(t)
	primary := mocks.NewMockSwapProvider(t)
	primary.On("Name").Return(swap.ProviderSwapKit)
	config := routingConfig()
	config.Fallback = false
	policy.On("Resolve", mock.Anything, "ETH", "eth").Return(config, nil)
	primary.On("Quote", mock.Anything, mock.Anything, mock.Anything).Return(nil, swap.ErrNoRoute).Once()
	factory, err := swap.NewFactory(primary)
	require.NoError(t, err)
	_, err = swap.NewRouter(factory, policy).Quote(context.Background(), routingRequest())
	require.ErrorIs(t, err, swap.ErrNoRoute)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = swap.NewRouter(factory, policy).Quote(ctx, routingRequest())
	require.ErrorIs(t, err, context.Canceled)
}
