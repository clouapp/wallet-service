package ozav

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

func onrampQuote() ramp.OnrampQuoteRequest {
	return ramp.OnrampQuoteRequest{
		Source:      ramp.FiatLeg{Rail: "pix", Currency: "brl", Amount: "500.00"},
		Destination: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "polygon", Address: "0x742d35Cc"},
		FeePercent:  "1.50",
	}
}

// onrampQuoteBody is the POST /quotes response measured against the sandbox on
// 2026-08-31 15:18Z — BRL 500 in, spread_bps 150, USDC out. See
// .scratch/ozav-medicoes-2026-08-31.md.
const onrampQuoteBody = `{
	"id":"ozav_q_01M1C6CPRT1MYHJN5PHV8S8Z0M","object":"quote","customer_id":"ozav_cus_1",
	"rate_type":"floating","rate":"0.176400",
	"source":{"amount":"500.00","currency":"BRL"},
	"target":{"amount":"88.200000","currency":"USDC"},
	"exchange_conditions":{"spread_bps":150},
	"expires_at":"2026-08-31T15:33:18Z","created_at":"2026-08-31T15:18:18Z"
}`

// The on-ramp quote is the mirror of the off-ramp one, and the mirroring is
// exactly where it can go wrong: the rail names whichever leg is fiat, so an
// on-ramp sends source_rail. Sending destination_rail would describe the
// crypto leg as arriving over Pix.
func TestEstimateOnrampNamesThePixRailOnTheSourceLeg(t *testing.T) {
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(onrampQuoteBody))
	})

	_, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampQuote())
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/quotes", got.path)
	require.Equal(t, "ozav_cus_1", got.body["customer_id"])
	require.Equal(t, "pix", got.body["source_rail"])
	_, hasDestinationRail := got.body["destination_rail"]
	require.False(t, hasDestinationRail, "the crypto leg does not travel over a fiat rail")
	require.Equal(t, float64(150), got.body["spread_bps"])

	source, ok := got.body["source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "BRL", source["currency"], "currency must be uppercase — lowercase is a measured 400")
	require.Equal(t, "500.00", source["amount"])

	target, ok := got.body["target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "USDC", target["currency"])
	_, hasAmount := target["amount"]
	require.False(t, hasAmount,
		"exactly one of source.amount/target.amount — the on-ramp knows the BRL side")
}

// The fee is XIP's cut of the BRL the user sends, so it is reported in BRL at
// the fiat scale — the same convention bridge's EstimateOnramp follows. The
// crypto the user receives is quote.target.amount verbatim: spread_bps has
// already come out of the rate, exactly as on the off-ramp side.
func TestEstimateOnrampReportsTheFeeInBRLAndTheReceiveVerbatim(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(onrampQuoteBody))
	})

	estimate, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampQuote())
	require.NoError(t, err)

	require.Equal(t, "88.200000", estimate.EstimatedReceive,
		"must be quote.target.amount, verbatim, never SubtractAmount")
	require.Equal(t, "5.668934", estimate.Rate,
		"BRL per token, the unit of Bridge's estimate and of the order's exchange_rate: Ozav quotes USDC per BRL")
	require.Equal(t, "7.50", estimate.FeeAmount, "1.50% of 500.00 BRL, at the fiat scale")
	require.Equal(t, "brl", estimate.FeeCurrency)
	require.True(t, estimate.Binding)
	require.Equal(t, "ozav_q_01M1C6CPRT1MYHJN5PHV8S8Z0M", estimate.ProviderQuoteID)
	require.NotNil(t, estimate.ExpiresAt)
	require.WithinDuration(t, time.Date(2026, 8, 31, 15, 33, 18, 0, time.UTC), *estimate.ExpiresAt, time.Second)
}

// An estimate is shown and binding, and the app parses its rate as a number,
// so a rate that cannot be stated in BRL per token is a failed provider call
// rather than a blank.
func TestEstimateOnrampRefusesARateItCannotInvert(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{"id":"ozav_q_1","rate":"0","target":{"currency":"USDC","amount":"88.20"}}`))
	})

	_, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampQuote())
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
}

func offrampQuote() ramp.OfframpQuoteRequest {
	return ramp.OfframpQuoteRequest{
		Source: ramp.CryptoLeg{Currency: "usdc", PaymentRail: "solana", Amount: "100.000000", Address: "8nWZB"},
		Destination: ramp.PixDestination{
			Rail: "pix", Currency: "brl", PixKey: "39053344705", PixKeyType: "cpf",
			AccountOwnerName: "Luiza Souza",
		},
		FeePercent: "1.50",
	}
}

// quoteBody is the plan's captured POST /quotes fixture (spread_bps=150),
// verbatim except for a concrete expires_at.
const quoteBody = `{
	"id":"ozav_q_01M0WR29SAH0CG4H0SQ1JCKQ9A","object":"quote","customer_id":"ozav_cus_1",
	"rate_type":"floating","rate":"5.439000",
	"source":{"amount":"100.000000","currency":"USDC"},
	"target":{"amount":"543.90","currency":"BRL"},
	"exchange_conditions":{"spread_bps":150},
	"expires_at":"2026-08-26T12:15:00Z","created_at":"2026-08-26T12:00:00Z"
}`

func TestEstimateOfframpSendsTheDocumentedQuoteBody(t *testing.T) {
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(quoteBody))
	})

	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.Equal(t, http.MethodPost, got.method)
	require.Equal(t, "/quotes", got.path)
	require.Equal(t, "ozav_cus_1", got.body["customer_id"])
	require.Equal(t, "pix", got.body["destination_rail"])
	// 1.50% -> 150 bps, via ramp.FeePercentToSpreadBps.
	require.Equal(t, float64(150), got.body["spread_bps"])

	source, ok := got.body["source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "USDC", source["currency"], "currency must be uppercase — lowercase is a measured 400")
	require.Equal(t, "100.000000", source["amount"])

	target, ok := got.body["target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "BRL", target["currency"])
	_, hasAmount := target["amount"]
	require.False(t, hasAmount, "exactly one of source.amount/target.amount — off-ramp knows the source side")
}

// This is the regression guard for the 8.16 BRL silent divergence the brief
// warns about: Ozav's quote is already net of spread_bps, so
// EstimatedReceive must be target.amount verbatim. Subtracting FeeAmount
// first — the Bridge pattern — would charge the fee twice.
//
// Proven red/green during development: with a SubtractAmount step inserted
// before the assignment, this test failed asserting "543.90" but getting
// "535.74" (98.5 x 5.439, half-up at 2 places); removing the subtraction
// made it pass. See task-4e5-report.md for the actual command output.
func TestEstimateOfframpDoesNotSubtractTheFeeTwice(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(quoteBody))
	})

	estimate, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.Equal(t, "543.90", estimate.EstimatedReceive,
		"must be quote.target.amount, verbatim, never SubtractAmount")
	require.Equal(t, "5.439000", estimate.Rate)
	require.Equal(t, "1.500000", estimate.FeeAmount, "FeeAmount still reports XIP's cut, comparable with Bridge")
	require.Equal(t, "usdc", estimate.FeeCurrency)
	require.True(t, estimate.Binding)
	require.Equal(t, "ozav_q_01M0WR29SAH0CG4H0SQ1JCKQ9A", estimate.ProviderQuoteID)
	require.NotNil(t, estimate.ExpiresAt)
	require.WithinDuration(t, time.Date(2026, 8, 26, 12, 15, 0, 0, time.UTC), *estimate.ExpiresAt, time.Second)
}

// The schema permits expires_at: null (rate_type floating re-derives at
// execution time). A nil ExpiresAt on our side reads as "never expires" to
// ramp_estimate.IsExpired, so the adapter must never pass one through.
func TestEstimateOfframpNullExpiryFallsBackToALocalWindow(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{
			"id":"ozav_q_2","rate":"5.44",
			"target":{"amount":"544.00","currency":"BRL"},
			"expires_at":null
		}`))
	})

	before := time.Now()
	estimate, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampQuote())
	require.NoError(t, err)

	require.NotNil(t, estimate.ExpiresAt, "a null expiry from Ozav must not become 'never expires' locally")
	require.True(t, estimate.ExpiresAt.After(before), "the local window must still be in the future")
	require.LessOrEqual(t, estimate.ExpiresAt.Sub(before), localExpiryWindow+time.Second)
}

// Ozav prices the network cost of a movement, and cannot do it without knowing
// the chain. An on-ramp's crypto leg is the target, so the chain rides there —
// lowercase, like the vocabulary the supports check uses — and never on the BRL
// source.
func TestEstimateOnrampSendsTheChainOnTheTargetLeg(t *testing.T) {
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(onrampQuoteBody))
	})
	req := onrampQuote()
	req.Destination.PaymentRail = "Solana"

	_, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	target, ok := got.body["target"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "solana", target["chain"], "a buy names the chain it delivers on, on target")
	source, ok := got.body["source"].(map[string]any)
	require.True(t, ok)
	_, sourceHasChain := source["chain"]
	require.False(t, sourceHasChain, "the BRL leg has no chain")
}

// An off-ramp spends crypto, so the chain rides on the source leg and the BRL
// target stays chainless.
func TestEstimateOfframpSendsTheChainOnTheSourceLeg(t *testing.T) {
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(quoteBody))
	})

	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampQuote())
	require.NoError(t, err)

	source, ok := got.body["source"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "solana", source["chain"], "a sell names the chain it spends from, on source")
	target, ok := got.body["target"].(map[string]any)
	require.True(t, ok)
	_, targetHasChain := target["chain"]
	require.False(t, targetHasChain, "the BRL leg has no chain")
}

// A caller that names no chain gets a body without the key, not "chain": "" —
// Ozav would read the empty string as a chain.
func TestEstimateOmitsAnEmptyChain(t *testing.T) {
	adapter, got := newTestAdapter(t, func(c *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(quoteBody))
	})
	req := offrampQuote()
	req.Source.PaymentRail = ""

	_, err := adapter.EstimateOfframp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, req)
	require.NoError(t, err)

	for _, leg := range []string{"source", "target"} {
		fields, ok := got.body[leg].(map[string]any)
		require.True(t, ok)
		_, hasChain := fields["chain"]
		require.False(t, hasChain, "%s must not carry an empty chain", leg)
	}
}

func offrampQuoteDeducting(deducted string) string {
	conditions := `{"spread_bps":150}`
	if deducted != "" {
		conditions = `{"spread_bps":150,"amount_deducted":` + deducted + `}`
	}
	return `{"id":"ozav_q_ded","rate":"5.439000",` +
		`"source":{"amount":"100.000000","currency":"USDC"},` +
		`"target":{"amount":"539.55","currency":"BRL"},` +
		`"exchange_conditions":` + conditions + `,` +
		`"expires_at":"2026-08-26T12:15:00Z"}`
}

func TestEstimateOfframpReportsTheProviderDeduction(t *testing.T) {
	cases := []struct {
		name         string
		deducted     string
		wantAmount   string
		wantCurrency string
	}{
		{name: "money object", deducted: `{"amount":"0.800000","currency":"USDC"}`, wantAmount: "0.800000", wantCurrency: "usdc"},
		{name: "money object without a currency", deducted: `{"amount":"0.8"}`, wantAmount: "0.800000", wantCurrency: "usdc"},
		{name: "money object with a numeric amount", deducted: `{"amount":0.8,"currency":"usdc"}`, wantAmount: "0.800000", wantCurrency: "usdc"},
		{name: "bare string", deducted: `"0.8"`, wantAmount: "0.800000", wantCurrency: "usdc"},
		{name: "bare number", deducted: `0.8`, wantAmount: "0.800000", wantCurrency: "usdc"},
		{name: "absent in the sandbox", deducted: ``},
		{name: "null", deducted: `null`},
		{name: "zero is no deduction", deducted: `{"amount":"0.000000","currency":"USDC"}`},
		{name: "negative is unreadable", deducted: `"-1"`},
		{name: "not a number", deducted: `"abc"`},
		{name: "object without an amount", deducted: `{"currency":"USDC"}`},
		{name: "another currency than the source is not relabelled", deducted: `{"amount":"3.50","currency":"BRL"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
				_, _ = w.Write([]byte(offrampQuoteDeducting(c.deducted)))
			})

			estimate, err := adapter.EstimateOfframp(context.Background(),
				ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, offrampQuote())
			require.NoError(t, err)

			require.Equal(t, c.wantAmount, estimate.ProviderDeductedAmount)
			require.Equal(t, c.wantCurrency, estimate.ProviderDeductedCurrency)
			require.Equal(t, "539.55", estimate.EstimatedReceive,
				"target.amount is net of the deduction already: verbatim, never subtracted again")
			require.Equal(t, "5.439000", estimate.Rate)
			require.Equal(t, "1.500000", estimate.FeeAmount, "XIP's own cut is a separate figure")
		})
	}
}

func TestEstimateOnrampReportsTheProviderDeductionInBRL(t *testing.T) {
	adapter, _ := newTestAdapter(t, func(_ *capture, w http.ResponseWriter) {
		_, _ = w.Write([]byte(`{
			"id":"ozav_q_on_ded","rate":"0.176400",
			"source":{"amount":"500.00","currency":"BRL"},
			"target":{"amount":"87.582600","currency":"USDC"},
			"exchange_conditions":{"spread_bps":150,"amount_deducted":{"amount":"3.5","currency":"BRL"}},
			"expires_at":"2026-08-31T15:33:18Z"
		}`))
	})

	estimate, err := adapter.EstimateOnramp(context.Background(),
		ramp.CustomerRef{CustomerID: 7, ExternalID: "ozav_cus_1"}, onrampQuote())
	require.NoError(t, err)

	require.Equal(t, "3.50", estimate.ProviderDeductedAmount, "scaled to BRL's two places")
	require.Equal(t, "brl", estimate.ProviderDeductedCurrency)
	require.Equal(t, "87.582600", estimate.EstimatedReceive, "target.amount verbatim")
	require.Equal(t, "5.668934", estimate.Rate, "BRL per token, from the pure-FX rate")
}
