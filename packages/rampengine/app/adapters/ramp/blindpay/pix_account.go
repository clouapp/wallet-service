package blindpay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

var _ ramp.PixAccountRegistrar = (*Adapter)(nil)

// pixAccountRequest is the create body of a Pix destination; the field order
// is fixed, so the same registration always encodes to the same bytes, which
// is what a replay under the same Idempotency-Key has to send. Nothing in it
// is ever logged or wrapped into an error.
type pixAccountRequest struct {
	Type                  string `json:"type"`
	Name                  string `json:"name"`
	RecipientRelationship string `json:"recipient_relationship"`
	PixKey                string `json:"pix_key"`
	BeneficiaryName       string `json:"beneficiary_name,omitempty"`
	TaxID                 string `json:"tax_id,omitempty"`
}

// pixAccountResponse is what the echo check reads: the id, the type, the name
// and the status. The key the answer may echo is deliberately not decoded.
type pixAccountResponse struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Name   string  `json:"name"`
	Status *string `json:"status"`
}

func encodePixAccount(reg ramp.PixAccountRegistration) ([]byte, error) {
	if reg.PixKey == "" || reg.Name == "" || reg.Relationship == "" {
		return nil, fmt.Errorf("blindpay: pix account is incomplete: %w", ramp.ErrRequestNotSent)
	}
	return json.Marshal(pixAccountRequest{
		Type: "pix", Name: reg.Name, RecipientRelationship: reg.Relationship, PixKey: reg.PixKey,
		BeneficiaryName: reg.RecipientName, TaxID: reg.RecipientDocument,
	})
}

func pixAccountResult(r pixAccountResponse) (*ramp.PixAccountResult, error) {
	if !strings.HasPrefix(r.ID, "ba_") {
		return nil, fmt.Errorf("blindpay: pix account echo: id has no ba_ prefix: %w", ramp.ErrBankAccountEchoMismatch)
	}
	return &ramp.PixAccountResult{ExternalAccountID: r.ID, Status: bankAccountStatus(r.Status)}, nil
}

// RegisterPixAccount registers the destination of a Pix payout. The error
// never wraps the body, so the key and the document stay out of every log.
func (a *Adapter) RegisterPixAccount(ctx context.Context, ref ramp.CustomerRef, reg ramp.PixAccountRegistration, idempotencyKey string) (*ramp.PixAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: pix account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	body, err := encodePixAccount(reg)
	if err != nil {
		return nil, err
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var created pixAccountResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "customers/" + ref.ExternalID + "/bank-accounts",
		RawBody: body, IdempotencyKey: idempotencyKey, Quiet: true,
	}, &created); err != nil {
		if errors.Is(err, errUndecodable) {
			return nil, fmt.Errorf("blindpay: pix account echo: undecodable answer: %w", ramp.ErrBankAccountEchoMismatch)
		}
		return nil, err
	}
	res, err := pixAccountResult(created)
	if err != nil {
		return nil, err
	}
	switch {
	case created.Name != reg.Name:
		return nil, fmt.Errorf("blindpay: pix account echo: name differs: %w", ramp.ErrBankAccountEchoMismatch)
	case created.Type != "pix":
		return nil, fmt.Errorf("blindpay: pix account echo: type %q: %w", created.Type, ramp.ErrBankAccountEchoMismatch)
	}
	return res, nil
}

func (a *Adapter) GetPixAccount(ctx context.Context, ref ramp.CustomerRef, externalID string) (*ramp.PixAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: pix account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	if !strings.HasPrefix(externalID, "ba_") {
		return nil, fmt.Errorf("blindpay: pix account id %q: %w", externalID, ramp.ErrRequestNotSent)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var got pixAccountResponse
	if err := client.Do(ctx, Request{Method: http.MethodGet, Path: "customers/" + ref.ExternalID + "/bank-accounts/" + externalID}, &got); err != nil {
		return nil, err
	}
	if got.Type != "pix" {
		return nil, fmt.Errorf("blindpay: pix account echo: type %q: %w", got.Type, ramp.ErrBankAccountEchoMismatch)
	}
	return pixAccountResult(got)
}

func (a *Adapter) FindPixAccountByName(ctx context.Context, ref ramp.CustomerRef, name string) (*ramp.PixAccountResult, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("blindpay: pix account without a customer: %w", ramp.ErrIntegrationRequired)
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	var list []pixAccountResponse
	if err := client.Do(ctx, Request{
		Method: http.MethodGet, Path: "customers/" + ref.ExternalID + "/bank-accounts", Query: url.Values{"name": {name}},
	}, &list); err != nil {
		return nil, err
	}
	var match []pixAccountResponse
	for _, item := range list {
		// A deprecated account is gone: it is never the one to link.
		if item.Name == name && bankAccountStatus(item.Status) != ramp.BankAccountDeprecated {
			match = append(match, item)
		}
	}
	switch len(match) {
	case 0:
		return nil, nil
	case 1:
		return pixAccountResult(match[0])
	}
	return nil, fmt.Errorf("blindpay: %d pix accounts named %s: %w", len(match), name, ramp.ErrBankAccountNameAmbiguous)
}

// sandboxPixPayoutCents is the smallest Pix quote the sandbox answers: below
// US$ 10 it replies 502 (seen in the PR 7 verification, 29/10/2026). Production
// has no such evidence, so it keeps minPayoutCents until its floor is read.
const sandboxPixPayoutCents = 1000

// pixPayoutFloorCents is the floor of a Pix payout in the instance's mode.
func pixPayoutFloorCents(mode string) int64 {
	if mode == models.ProviderModeSandbox {
		return sandboxPixPayoutCents
	}
	return minPayoutCents
}

// estimatePixOfframp quotes a Pix payout of a stablecoin to the account the
// service registered (req.Destination.ExternalAccountID). The quote is binding
// and in reais; the partner fee profile of the rail's fee rides in the body.
// The key never travels here: the account is the destination.
func (a *Adapter) estimatePixOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpQuoteRequest) (*ramp.Estimate, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	accountID := req.Destination.ExternalAccountID
	if !strings.HasPrefix(accountID, "ba_") {
		return nil, fmt.Errorf("blindpay: pix payout needs a registered account: %w", ramp.ErrInvalidDestination)
	}
	cents, err := tokenCents(req.Source.Amount)
	if err != nil {
		return nil, err
	}
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	if floor := pixPayoutFloorCents(client.Mode()); cents < floor {
		return nil, &ramp.LimitViolation{LimitUSD: centsString(big.NewRat(floor, 1)), Err: ramp.ErrAmountBelowMinimum}
	}
	bps, err := feeBps(req.FeePercent)
	if err != nil {
		return nil, err
	}
	leg, err := LegFor(client.Mode(), req.Source.PaymentRail, req.Source.Currency)
	if err != nil {
		return nil, err
	}
	feeID, err := a.partnerFeeID(ctx, client, 0, bps)
	if err != nil {
		return nil, err
	}

	var raw json.RawMessage
	if err := client.Do(ctx, Request{
		Method: http.MethodPost, Path: "quotes",
		Body: quoteRequest{
			BankAccountID: accountID, CurrencyType: "sender", CoverFees: false,
			RequestAmount: cents, Network: leg.Network, Token: leg.Token, PartnerFeeID: feeID,
		},
	}, &raw); err != nil {
		if feeID != "" && partnerFeeRejected(err) {
			a.forgetPartnerFee(client.InstanceID(), 0, bps)
		}
		return nil, err
	}
	return pixEstimate(raw, req, cents, bps)
}

// pixEstimate reads a Pix quote answer. The receiver amount is in reais; the
// partner fee and the flat fee are in the token the customer sends.
func pixEstimate(raw json.RawMessage, req ramp.OfframpQuoteRequest, cents int64, bps int) (*ramp.Estimate, error) {
	var quote quoteResponse
	if err := json.Unmarshal(raw, &quote); err != nil {
		return nil, fmt.Errorf("blindpay: quote: decode: %w", ramp.ErrProviderFailed)
	}
	expiresAt, err := validQuote(quote, cents, time.Now())
	if err != nil {
		return nil, err
	}
	if !quote.PartnerFeeAmount.nonNegative() {
		return nil, fmt.Errorf("blindpay: quote: fee is negative: %w", ramp.ErrProviderFailed)
	}
	feePercent, feeAmount := "", "0.00"
	if req.FeePercent != "" {
		feePercent = fmt.Sprintf("%d.%02d", bps/100, bps%100)
	}
	if quote.PartnerFeeAmount.rat != nil {
		feeAmount = centsString(quote.PartnerFeeAmount.rat)
	}
	providerFee := ""
	if quote.FlatFee.rat != nil && quote.FlatFee.rat.Sign() > 0 {
		providerFee = centsString(quote.FlatFee.rat)
	}
	return &ramp.Estimate{
		ProviderQuoteID:   quote.ID,
		Binding:           true,
		ExpiresAt:         &expiresAt,
		Rate:              new(big.Rat).Quo(quote.BlindpayQuotation.rat, big.NewRat(100, 1)).FloatString(rateScale),
		FeePercent:        feePercent,
		FeeAmount:         feeAmount,
		FeeCurrency:       strings.ToLower(strings.TrimSpace(req.Source.Currency)),
		EstimatedReceive:  centsString(quote.ReceiverAmount.rat),
		ProviderFeeAmount: providerFee,
		SourceAmount:      centsString(big.NewRat(cents, 1)),
		Raw:               raw,
	}, nil
}
