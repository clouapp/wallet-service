package ozav

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// EstimateOnramp prices BRL in against the crypto the user would receive. The
// fee comes out of the BRL the user sends. The crypto leg is the target, so the
// destination chain is what Ozav needs to price the network cost.
func (a *Adapter) EstimateOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampQuoteRequest) (*ramp.Estimate, error) {
	return a.quote(ctx, ref, quoteInput{
		source: req.Source.Currency, target: req.Destination.Currency, amount: req.Source.Amount,
		chain:      req.Destination.PaymentRail,
		feePercent: req.FeePercent, feeScale: fiatScale, pixOnSource: true,
	})
}

// EstimateOfframp prices crypto in against the BRL the user would receive. The
// fee comes out of the token. The crypto leg is the source, so the chain the
// token is spent from is what Ozav needs to price the network cost.
func (a *Adapter) EstimateOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpQuoteRequest) (*ramp.Estimate, error) {
	return a.quote(ctx, ref, quoteInput{
		source: req.Source.Currency, target: req.Destination.Currency, amount: req.Source.Amount,
		chain:      req.Source.PaymentRail,
		feePercent: req.FeePercent, feeScale: cryptoScale,
	})
}

// quoteInput is what both estimates reduce to.
type quoteInput struct {
	source, target, amount, feePercent string
	// chain is the crypto leg's network: the target's on an on-ramp, the
	// source's on an off-ramp. Ozav's quote prices the network cost with it.
	chain string
	// feeScale is the decimal places of the source leg, which the fee and the
	// provider's deduction are both stated in.
	feeScale int
	// pixOnSource names the leg that travels over Pix: an on-ramp collects
	// over it (source_rail), an off-ramp pays out over it (destination_rail).
	// The wrong one is not refused; Ozav prices something else.
	pixOnSource bool
}

// quote prices one direction through POST /quotes.
//
// Ozav's quote is already net of spread_bps, XIP's own cut. Measured against
// the sandbox, same pair, same instant:
//
//	spread_bps=0    rate 5.522250   target.amount 552.22
//	spread_bps=150  rate 5.439000   target.amount 543.90
//
// So EstimatedReceive is target.amount verbatim, never SubtractAmount: taking
// the fee off first, as Bridge's gross rate requires, would charge it twice
// (535.74 quoted against 543.90 paid on 100 USDC). FeeAmount only reports the
// cut, on the leg it comes out of.
//
// The same holds for the provider's own costs. In the current format `rate` is
// pure FX and the network and Pix costs are deducted from the source before
// converting, reported in exchange_conditions.amount_deducted; target.amount is
// the net the user receives. That figure is reported beside the estimate
// (ProviderDeductedAmount) and never subtracted again, so source x rate no
// longer equals target.amount whenever a deduction applies. The sandbox does
// not send amount_deducted: absent means the fields stay empty.
func (a *Adapter) quote(ctx context.Context, ref ramp.CustomerRef, in quoteInput) (*ramp.Estimate, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	spreadBps, err := ramp.FeePercentToSpreadBps(in.feePercent)
	if err != nil {
		return nil, fmt.Errorf("ozav: fee: %w", err)
	}
	feeAmount, err := ramp.FeeAmount(in.amount, in.feePercent, in.feeScale)
	if err != nil {
		return nil, fmt.Errorf("ozav: fee: %w", err)
	}

	resp, raw, err := ramp.Call[quoteResponse](ctx, a.do, Request{
		Method: http.MethodPost, Path: "/quotes", Body: newQuoteRequest(ref, in, spreadBps),
	})
	if err != nil {
		return nil, err
	}
	rate, ok := brlPerToken(in.source, in.target, resp.Rate)
	if !ok {
		return nil, fmt.Errorf("ozav: quote %s rate %q: %w", resp.ID, resp.Rate, ramp.ErrProviderFailed)
	}
	deducted, deductedCurrency := providerDeduction(resp.ExchangeConditions.AmountDeducted, in)
	return &ramp.Estimate{
		ProviderQuoteID: resp.ID,
		// Binding for uniqueness of use, not for the value: the rate floats
		// until execution, but quote_id is authoritative for the amount, so a
		// non-binding estimate would let one quote fund two orders.
		Binding:                  true,
		ExpiresAt:                quoteExpiry(resp.ExpiresAt),
		Rate:                     rate,
		FeePercent:               in.feePercent,
		FeeAmount:                feeAmount,
		FeeCurrency:              normalizeVocabulary(in.source),
		EstimatedReceive:         resp.Target.Amount,
		ProviderDeductedAmount:   deducted,
		ProviderDeductedCurrency: deductedCurrency,
		Raw:                      raw,
	}, nil
}

// newQuoteRequest builds the POST /quotes body. The Pix rail names whichever
// leg is fiat and the chain names the other one, so each is set on exactly one
// leg: an on-ramp collects over Pix (source_rail) and delivers on a chain
// (target.chain); an off-ramp spends from a chain (source.chain) and pays out
// over Pix (destination_rail). An empty chain is left out of the body.
func newQuoteRequest(ref ramp.CustomerRef, in quoteInput, spreadBps int) quoteRequest {
	body := quoteRequest{
		CustomerID: ref.ExternalID,
		Source:     quoteLeg{Currency: quoteCurrency(in.source), Amount: in.amount},
		Target:     quoteLeg{Currency: quoteCurrency(in.target)},
		SpreadBps:  spreadBps,
	}
	chain := normalizeVocabulary(in.chain)
	if in.pixOnSource {
		body.SourceRail = pixRailName
		body.Target.Chain = chain
		return body
	}
	body.DestinationRail = pixRailName
	body.Source.Chain = chain
	return body
}

// providerDeduction reads exchange_conditions.amount_deducted: what Ozav took
// out of the source leg for network and Pix costs before converting. It is
// optional information about a quote that is correct without it, so anything
// short of a positive amount in the source currency yields ("", "") instead of
// failing the estimate — absent or null (the sandbox), zero, unreadable, or in
// another currency than the leg it was deducted from (relabelling it would put
// a wrong figure on screen; omitting it only hides a correct one).
//
// The amount is accepted as a money object ({"amount","currency"}) or as a
// bare string/number, and is written at the source leg's scale and vocabulary,
// the way FeeAmount and FeeCurrency are.
func providerDeduction(raw json.RawMessage, in quoteInput) (amount, currency string) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", ""
	}
	value, stated, ok := decodeDeduction(trimmed)
	if !ok {
		return "", ""
	}
	source := normalizeVocabulary(in.source)
	if stated != "" && normalizeVocabulary(stated) != source {
		return "", ""
	}
	scaled, err := ramp.ScaleAmount(value, in.feeScale)
	// A string of only zeros and the point is zero: there was no deduction.
	if err != nil || strings.Trim(scaled, "0.") == "" {
		return "", ""
	}
	return scaled, source
}

// decodeDeduction splits amount_deducted into its amount and, when it names
// one, its currency. ok is false for anything that is neither a money object
// nor a number.
func decodeDeduction(raw []byte) (amount, currency string, ok bool) {
	if len(raw) == 0 {
		return "", "", false
	}
	if raw[0] == '{' {
		var money struct {
			Amount   json.Number `json:"amount"`
			Currency string      `json:"currency"`
		}
		if err := json.Unmarshal(raw, &money); err != nil {
			return "", "", false
		}
		return money.Amount.String(), money.Currency, true
	}
	var bare json.Number
	if err := json.Unmarshal(raw, &bare); err != nil {
		return "", "", false
	}
	return bare.String(), "", true
}

// quoteRequest is the body of POST /quotes. Exactly one rail field is set.
type quoteRequest struct {
	CustomerID      string   `json:"customer_id"`
	Source          quoteLeg `json:"source"`
	Target          quoteLeg `json:"target"`
	SourceRail      string   `json:"source_rail,omitempty"`
	DestinationRail string   `json:"destination_rail,omitempty"`
	SpreadBps       int      `json:"spread_bps"`
}

// quoteLeg is one side of a quote. Amount is omitted on the side Ozav is asked
// to compute: the schema takes exactly one of source.amount and target.amount.
// Chain is set only on the crypto leg; a fiat leg has none.
type quoteLeg struct {
	Currency string `json:"currency"`
	Amount   string `json:"amount,omitempty"`
	Chain    string `json:"chain,omitempty"`
}

type quoteResponse struct {
	ID     string   `json:"id"`
	Rate   string   `json:"rate"`
	Target quoteLeg `json:"target"`
	// ExchangeConditions carries what the quote says about how it was priced.
	// Only amount_deducted is read, kept raw because its shape is decoded
	// leniently by providerDeduction; spread_bps is XIP's own and is not echoed.
	ExchangeConditions struct {
		AmountDeducted json.RawMessage `json:"amount_deducted"`
	} `json:"exchange_conditions"`
	ExpiresAt *time.Time `json:"expires_at"`
}

// quoteCurrency uppercases a currency: "brl" is a measured 400 despite the
// prose promising case-insensitive matching (the opposite of Bridge).
func quoteCurrency(currency string) string { return strings.ToUpper(strings.TrimSpace(currency)) }

// quoteExpiry is Ozav's expiry, or localExpiryWindow from now when it sent none.
func quoteExpiry(expiresAt *time.Time) *time.Time {
	if expiresAt != nil {
		return expiresAt
	}
	expiry := time.Now().Add(localExpiryWindow)
	return &expiry
}

// localExpiryWindow stands in for a quote answered with `expires_at: null`
// (a floating rate): an estimate with no deadline is one IsExpired would treat
// as never expiring.
const localExpiryWindow = 5 * time.Minute
