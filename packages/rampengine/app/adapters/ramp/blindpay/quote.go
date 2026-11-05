package blindpay

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// minPayoutCents is BlindPay's floor on a payout's request_amount (US$ 5,00).
const minPayoutCents = 500

// sandboxBankPayoutCents are the smallest bank quotes the sandbox answers per
// rail: below them it replies 502 (seen in the PR 5/6 sandbox verification,
// 29/10/2026: ACH 10, wire 16). Production has no such evidence, so it keeps
// minPayoutCents until its floor is read.
var sandboxBankPayoutCents = map[ramp.Rail]int64{
	ramp.RailACHPayout:  1000,
	ramp.RailWirePayout: 1600,
}

// bankPayoutFloorCents is the floor of a bank payout on a rail in the
// instance's mode.
func bankPayoutFloorCents(mode string, rail ramp.Rail) int64 {
	if floor, ok := sandboxBankPayoutCents[rail]; ok && mode == models.ProviderModeSandbox {
		return floor
	}
	return minPayoutCents
}

// quoteRequest is the QuoteIn body; the field order is fixed. A bank payout
// carries no partner fee (its rails are locked at 0); a Pix payout carries the
// profile of the rail's fee, and the field is absent from the bank body.
type quoteRequest struct {
	BankAccountID string `json:"bank_account_id"`
	CurrencyType  string `json:"currency_type"`
	CoverFees     bool   `json:"cover_fees"`
	RequestAmount int64  `json:"request_amount"`
	Network       string `json:"network"`
	Token         string `json:"token"`
	PartnerFeeID  string `json:"partner_fee_id,omitempty"`
}

// quoteResponse is the slice of QuoteOut the adapter reads. Every amount is in
// cents of its currency.
type quoteResponse struct {
	ID                string      `json:"id"`
	ExpiresAt         quoteNumber `json:"expires_at"`
	BlindpayQuotation quoteNumber `json:"blindpay_quotation"`
	ReceiverAmount    quoteNumber `json:"receiver_amount"`
	SenderAmount      quoteNumber `json:"sender_amount"`
	FlatFee           quoteNumber `json:"flat_fee"`
	BillingFeeAmount  quoteNumber `json:"billing_fee_amount"`
	PartnerFeeAmount  quoteNumber `json:"partner_fee_amount"`
}

// EstimateOfframp quotes a bank payout (ACH or wire) of a stablecoin on the
// instance's Solana leg to the customer's registered account. The quote is
// binding: the create redeems this id.
func (a *Adapter) EstimateOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpQuoteRequest) (*ramp.Estimate, error) {
	if req.Bank == nil {
		return a.estimatePixOfframp(ctx, ref, req)
	}
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if err := req.ValidateDestination(req.Bank.Rail); err != nil {
		return nil, err
	}
	cents, err := tokenCents(req.Source.Amount)
	if err != nil {
		return nil, err
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	if floor := bankPayoutFloorCents(client.Mode(), req.Bank.Rail); cents < floor {
		return nil, &ramp.LimitViolation{LimitUSD: centsString(big.NewRat(floor, 1)), Err: ramp.ErrAmountBelowMinimum}
	}
	leg, err := LegFor(client.Mode(), req.Source.PaymentRail, req.Source.Currency)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "quotes",
		Body: quoteRequest{
			BankAccountID: req.Bank.ExternalAccountID, CurrencyType: "sender", CoverFees: false,
			RequestAmount: cents, Network: leg.Network, Token: leg.Token,
		},
	}, &raw); err != nil {
		return nil, err
	}

	var quote quoteResponse
	if err := json.Unmarshal(raw, &quote); err != nil {
		return nil, fmt.Errorf("blindpay: quote: decode: %w", ramp.ErrProviderFailed)
	}
	expiresAt, err := validQuote(quote, cents, time.Now())
	if err != nil {
		return nil, err
	}

	// billing_fee_amount is invoiced to XIP at month end and never deducted
	// from receiver_amount, so only the flat fee is shown to the customer.
	providerFee := ""
	if quote.FlatFee.rat != nil && quote.FlatFee.rat.Sign() > 0 {
		providerFee = centsString(quote.FlatFee.rat)
	}
	return &ramp.Estimate{
		ProviderQuoteID:   quote.ID,
		Binding:           true,
		ExpiresAt:         &expiresAt,
		Rate:              new(big.Rat).Quo(quote.BlindpayQuotation.rat, big.NewRat(100, 1)).FloatString(rateScale),
		FeePercent:        req.FeePercent,
		FeeAmount:         "0.00",
		FeeCurrency:       strings.ToLower(strings.TrimSpace(req.Source.Currency)),
		EstimatedReceive:  centsString(quote.ReceiverAmount.rat),
		ProviderFeeAmount: providerFee,
		SourceAmount:      centsString(big.NewRat(cents, 1)),
		Raw:               raw,
	}, nil
}

// validQuote holds the answer to what was asked and answers its expiry. The
// errors never carry an amount.
func validQuote(q quoteResponse, requestCents int64, now time.Time) (time.Time, error) {
	fail := func(what string) (time.Time, error) {
		return time.Time{}, fmt.Errorf("blindpay: quote: %s: %w", what, ramp.ErrProviderFailed)
	}
	if len(q.ID) != quoteIDLen || !strings.HasPrefix(q.ID, "qu_") {
		return fail("id is malformed")
	}
	expiresAt := quoteExpiry(q.ExpiresAt, now)
	if !expiresAt.After(now) {
		return fail("already expired")
	}
	if q.SenderAmount.rat == nil || !q.SenderAmount.rat.IsInt() || q.SenderAmount.rat.Cmp(big.NewRat(requestCents, 1)) != 0 {
		return fail("sender amount differs from the request")
	}
	if !q.ReceiverAmount.positive() || !q.ReceiverAmount.rat.IsInt() {
		return fail("receiver amount is not a positive integer")
	}
	if !q.BlindpayQuotation.positive() {
		return fail("quotation is not positive")
	}
	if !q.FlatFee.nonNegative() || !q.BillingFeeAmount.nonNegative() {
		return fail("fee is negative")
	}
	return expiresAt, nil
}
