package blindpay_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func limitsRoute(body string) map[string]stubAnswer {
	return map[string]stubAnswer{"GET limits/customers/" + customerID: {200, body}}
}

func TestRemainingPayoutUSD_IsTheSmallerOfTheTwo(t *testing.T) {
	cases := map[string]string{
		`{"limits":{"payin":{"daily":1,"monthly":1},"payout":{"daily":250000,"monthly":1000000}}}`: "2500.00",
		`{"limits":{"payin":{"daily":1,"monthly":1},"payout":{"daily":250000,"monthly":123456}}}`:  "1234.56",
		`{"limits":{"payout":{"daily":0,"monthly":500}}}`:                                          "0.00",
		`{"limits":{"payout":{"daily":999.9,"monthly":1000}}}`:                                     "9.99",
	}
	for body, want := range cases {
		adapter, _, calls := payinServer(t, models.ProviderModeSandbox, limitsRoute(body))
		got, err := adapter.RemainingPayoutUSD(context.Background(), payinRef)
		require.NoError(t, err, body)
		assert.Equal(t, want, got, body)
		assert.Equal(t, "GET", calls()[0].method)
	}
}

func TestRemainingPayoutUSD_NegativeOrMissingIsProviderFailed(t *testing.T) {
	for _, body := range []string{
		`{"limits":{"payout":{"daily":-1,"monthly":500}}}`,
		`{"limits":{"payout":{"daily":500,"monthly":-1}}}`,
		`{"limits":{"payout":{"daily":500}}}`,
		`{"limits":{"payout":{"daily":null,"monthly":500}}}`,
		`{"limits":{}}`,
		`not json`,
	} {
		adapter, _, _ := payinServer(t, models.ProviderModeSandbox, limitsRoute(body))
		_, err := adapter.RemainingPayoutUSD(context.Background(), payinRef)
		assert.ErrorIs(t, err, ramp.ErrProviderFailed, body)
	}
}

func TestRemainingPayoutUSD_PassesProviderErrorsThroughSentinelFor(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"GET limits/customers/" + customerID: {400, `{"code":"VALIDATION_INVALID_REQUEST","message":"please_accept_terms_of_service"}`},
	})
	_, err := adapter.RemainingPayoutUSD(context.Background(), payinRef)
	assert.ErrorIs(t, err, ramp.ErrTosRequired)
}

func TestRemainingPayoutUSD_RequiresACustomer(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
	_, err := adapter.RemainingPayoutUSD(context.Background(), ramp.CustomerRef{})
	assert.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Empty(t, calls())
}
