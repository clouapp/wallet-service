package blindpay_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

const bankAccountID = "ba_000000000001"

func bankQuoteBody(t *testing.T, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"id":                   quoteID,
		"expires_at":           time.Now().Add(5 * time.Minute).Unix(),
		"commercial_quotation": 100,
		"blindpay_quotation":   100,
		"receiver_amount":      990,
		"sender_amount":        1012,
		"flat_fee":             20,
		"billing_fee_amount":   5,
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	encoded, err := json.Marshal(body)
	require.NoError(t, err)
	return string(encoded)
}

func offrampRequest(amount string) ramp.OfframpQuoteRequest {
	return ramp.OfframpQuoteRequest{
		Source:     ramp.CryptoLeg{Currency: "USDC", PaymentRail: "solana", Amount: amount},
		Bank:       &ramp.BankDestination{Rail: ramp.RailACHPayout, Currency: "usd", ExternalAccountID: bankAccountID},
		FeePercent: "1.00",
	}
}

func TestEstimateOfframp_HappyPathSendsTheExactBody(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, bankQuoteBody(t, nil)},
	})

	est, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.123456"))
	require.NoError(t, err)

	got := calls()
	require.Len(t, got, 1)
	assert.Equal(t, "POST", got[0].method)
	assert.Equal(t, instancePrefix+"quotes", got[0].path)
	assert.Empty(t, got[0].idempotency)
	assert.Equal(t,
		`{"bank_account_id":"ba_000000000001","currency_type":"sender","cover_fees":false,"request_amount":1012,"network":"solana_devnet","token":"USDB"}`,
		string(got[0].body))

	assert.Equal(t, quoteID, est.ProviderQuoteID)
	assert.True(t, est.Binding)
	require.NotNil(t, est.ExpiresAt)
	assert.Equal(t, "1.000000", est.Rate)
	assert.Equal(t, "1.00", est.FeePercent)
	assert.Equal(t, "0.00", est.FeeAmount)
	assert.Equal(t, "usdc", est.FeeCurrency)
	assert.Equal(t, "9.90", est.EstimatedReceive)
	assert.Equal(t, "0.20", est.ProviderFeeAmount)
	assert.Equal(t, "10.12", est.SourceAmount)
	assert.NotNil(t, est.Raw)
}

func TestEstimateOfframp_ProductionUsesUSDCOnSolana(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeProduction, map[string]stubAnswer{
		"POST quotes": {200, bankQuoteBody(t, nil)},
	})
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.12"))
	require.NoError(t, err)
	assert.Contains(t, string(calls()[0].body), `"network":"solana","token":"USDC"`)
}

func TestEstimateOfframp_BillingFeeIsNotShown(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, bankQuoteBody(t, map[string]any{"flat_fee": 0, "billing_fee_amount": 75})},
	})
	est, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.12"))
	require.NoError(t, err)
	assert.Empty(t, est.ProviderFeeAmount)
}

func TestEstimateOfframp_NoProviderFeeWhenBothAreZeroOrAbsent(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, bankQuoteBody(t, map[string]any{"flat_fee": 0, "billing_fee_amount": nil})},
	})
	est, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.12"))
	require.NoError(t, err)
	assert.Empty(t, est.ProviderFeeAmount)
}

func TestEstimateOfframp_BelowTheSandboxMinimumNamesTheRailsLimit(t *testing.T) {
	for _, tc := range []struct {
		rail   ramp.Rail
		amount string
		limit  string
	}{
		{ramp.RailACHPayout, "9.99", "10.00"},
		{ramp.RailWirePayout, "15.99", "16.00"},
	} {
		adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
		req := offrampRequest(tc.amount)
		req.Bank.Rail = tc.rail
		_, err := adapter.EstimateOfframp(context.Background(), payinRef, req)

		var violation *ramp.LimitViolation
		require.ErrorAs(t, err, &violation, tc.rail)
		assert.Equal(t, tc.limit, violation.LimitUSD, tc.rail)
		assert.ErrorIs(t, err, ramp.ErrAmountBelowMinimum, tc.rail)
		assert.Empty(t, calls(), tc.rail)
	}
}

func TestEstimateOfframp_SandboxFloorIsInclusive(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {200, bankQuoteBody(t, map[string]any{"sender_amount": 1000})},
	})
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.00"))
	require.NoError(t, err)
	assert.NotEmpty(t, calls())
}

func TestEstimateOfframp_ProductionKeepsTheFiveDollarFloor(t *testing.T) {
	for _, rail := range []ramp.Rail{ramp.RailACHPayout, ramp.RailWirePayout} {
		adapter, _, calls := payinServer(t, models.ProviderModeProduction, nil)
		req := offrampRequest("4.99")
		req.Bank.Rail = rail
		_, err := adapter.EstimateOfframp(context.Background(), payinRef, req)

		var violation *ramp.LimitViolation
		require.ErrorAs(t, err, &violation, rail)
		assert.Equal(t, "5.00", violation.LimitUSD, rail)
		assert.Empty(t, calls(), rail)
	}
}

func TestEstimateOfframp_RefusesBadAmountsWithoutACall(t *testing.T) {
	for _, amount := range []string{"-10", "abc", "", "1e3", "99999999999999999999999"} {
		adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
		_, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest(amount))
		require.Error(t, err, amount)
		assert.ErrorIs(t, err, ramp.ErrRequestNotSent, amount)
		assert.Empty(t, calls(), amount)
	}
}

func TestEstimateOfframp_NoBankAndNoRegisteredPixAccountIsAnInvalidDestination(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
	req := offrampRequest("10")
	req.Bank = nil
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, req)
	assert.ErrorIs(t, err, ramp.ErrInvalidDestination)
	assert.Empty(t, calls())
}

func TestEstimateOfframp_RefusesAMismatchedDestination(t *testing.T) {
	adapter, _, calls := payinServer(t, models.ProviderModeSandbox, nil)
	req := offrampRequest("10")
	req.Bank.Rail = ramp.RailWirePayout
	req.Bank.ExternalAccountID = ""
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, req)
	assert.ErrorIs(t, err, ramp.ErrInvalidDestination)
	assert.Empty(t, calls())
}

func TestEstimateOfframp_RefusesAResponseThatBreaksTheRules(t *testing.T) {
	past := time.Now().Add(-time.Minute).Unix()
	cases := map[string]map[string]any{
		"wrong prefix":         {"id": "pq_000000000001"},
		"empty id":             {"id": ""},
		"expired":              {"expires_at": past},
		"sender differs":       {"sender_amount": 1013},
		"sender fractional":    {"sender_amount": 1012.5},
		"sender absent":        {"sender_amount": nil},
		"receiver zero":        {"receiver_amount": 0},
		"receiver fractional":  {"receiver_amount": 990.5},
		"quotation zero":       {"blindpay_quotation": 0},
		"quotation negative":   {"blindpay_quotation": -1},
		"flat fee negative":    {"flat_fee": -1},
		"billing fee negative": {"billing_fee_amount": -1},
	}
	for name, override := range cases {
		t.Run(name, func(t *testing.T) {
			adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
				"POST quotes": {200, bankQuoteBody(t, override)},
			})
			est, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10.12"))
			assert.ErrorIs(t, err, ramp.ErrProviderFailed)
			assert.Nil(t, est)
		})
	}
}

func TestEstimateOfframp_TermsNotAcceptedIsTosRequired(t *testing.T) {
	adapter, _, _ := payinServer(t, models.ProviderModeSandbox, map[string]stubAnswer{
		"POST quotes": {400, `{"code":"VALIDATION_INVALID_REQUEST","message":"please_accept_terms_of_service"}`},
	})
	_, err := adapter.EstimateOfframp(context.Background(), payinRef, offrampRequest("10"))
	assert.ErrorIs(t, err, ramp.ErrTosRequired)
}
