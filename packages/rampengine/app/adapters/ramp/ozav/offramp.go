package ozav

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
)

// payoutDestinationRequest is the body of POST /payout-destinations; `rail`
// selects the `details` shape, and XIP registers Pix only.
type payoutDestinationRequest struct {
	CustomerID    string           `json:"customer_id"`
	Currency      string           `json:"currency"`
	Rail          string           `json:"rail"`
	AccountHolder accountHolder    `json:"account_holder"`
	Details       payoutDetailsPix `json:"details"`
}

type accountHolder struct {
	Name  string        `json:"name"`
	Type  string        `json:"type"`
	TaxID *taxIDPayload `json:"tax_id,omitempty"`
}

type payoutDetailsPix struct {
	PixKey string `json:"pix_key"`
}

type payoutDestinationResponse struct {
	ID string `json:"id"`
}

const (
	taxIDCountryBR = "BR"
	taxIDTypeCPF   = "cpf"
	taxIDTypeCNPJ  = "cnpj"
	// cnpjLength tells a CNPJ from a CPF on a Brazilian holder: fourteen
	// characters, letters included for an alphanumeric one.
	cnpjLength = 14
)

// holderTaxIDFor derives the account holder's document: a CPF or CNPJ key is
// the document, any other key takes RecipientDocument. With neither it is
// omitted (optional on Ozav's side) rather than guessed, which would register
// the destination to the wrong person.
func holderTaxIDFor(destination ramp.PixDestination) *taxIDPayload {
	keyType := normalizeVocabulary(destination.PixKeyType)
	if keyType == taxIDTypeCPF || keyType == taxIDTypeCNPJ {
		return &taxIDPayload{Country: taxIDCountryBR, Type: keyType, Value: destination.PixKey}
	}
	document := brazil.CanonicalDocument(destination.RecipientDocument)
	if document == "" {
		return nil
	}
	documentType := taxIDTypeCPF
	if len(document) == cnpjLength {
		documentType = taxIDTypeCNPJ
	}
	return &taxIDPayload{Country: taxIDCountryBR, Type: documentType, Value: document}
}

// registerPayoutDestination turns the Pix key into an Ozav object a transfer
// can name.
//
// The key goes out in its canonical DICT form, not as typed. Ozav stores it
// verbatim, so "+55 11 98765-4321" and "11987654321" would be two
// destinations; and it refuses a replay whose body differs from the first
// (IDEMPOTENCY_KEY_REUSED), so a retry spelled another way must send the same
// bytes, not only the same key. The canonical form is the one
// offramp_destinations identifies the key by, and the one the app sends.
func (a *Adapter) registerPayoutDestination(ctx context.Context, ref ramp.CustomerRef, destination ramp.PixDestination) (string, error) {
	destination.PixKey = brazil.NormalizePixKey(destination.PixKeyType, destination.PixKey)
	taxID := holderTaxIDFor(destination)
	body := payoutDestinationRequest{
		CustomerID: ref.ExternalID,
		Currency:   quoteCurrency(destination.Currency),
		Rail:       pixRailName,
		AccountHolder: accountHolder{
			Name:  destination.AccountOwnerName,
			Type:  typeIndividual,
			TaxID: taxID,
		},
		Details: payoutDetailsPix{PixKey: destination.PixKey},
	}

	var resp payoutDestinationResponse
	if err := a.do(ctx, Request{
		Method:         http.MethodPost,
		Path:           "/payout-destinations",
		Body:           body,
		IdempotencyKey: payoutDestinationKey(body),
	}, &resp); err != nil {
		return "", err
	}
	if resp.ID == "" {
		return "", errors.New("ozav: payout destination created without an id")
	}
	return resp.ID, nil
}

// payoutDestinationKey keys a registration on the body fields that tell one
// destination from another (the customer, the canonical Pix key, the holder
// document and the holder name, each as sent) rather than on the order:
// registering the same destination twice is one act. A field left out would
// let a retry change it under the same key, which Ozav refuses with
// IDEMPOTENCY_KEY_REUSED. It is a digest because the key and the name are
// personal data and, once normalized into Ozav's alphabet, two keys could
// collide ("a.b@x" and "a-b@x").
func payoutDestinationKey(body payoutDestinationRequest) string {
	document := ""
	if body.AccountHolder.TaxID != nil {
		document = body.AccountHolder.TaxID.Value
	}
	sum := sha256.Sum256([]byte(body.CustomerID + "\x00" + body.Details.PixKey + "\x00" + document +
		"\x00" + body.AccountHolder.Name))
	return "ozav-pd-" + hex.EncodeToString(sum[:16])
}

// CreateOfframp debits the customer's wallet into a registered Pix
// destination.
//
// A new destination is `active` at once but unusable until Ozav registers it
// with the settlement rail, and the transfer answers 422 PROVISIONING_REQUIRED
// meanwhile (21 of 21 in the sandbox, 19 to 31/08). Ozav's schema says to
// treat it as retryable, so it travels as ramp.ErrOzavPayoutNotProvisioned.
func (a *Adapter) CreateOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpCreateRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	// The quote is required by Ozav and is the only record of the rate the
	// user agreed to.
	if req.ProviderQuoteID == "" {
		return nil, ramp.ErrEstimateRequired
	}

	// A destination the service already knows is named, never re-registered.
	destinationID := req.ExternalAccountID
	if destinationID == "" {
		var err error
		destinationID, err = a.registerPayoutDestination(ctx, ref, req.Destination)
		if err != nil {
			return nil, ramp.NotSent(err)
		}
	}

	funding, err := a.OfframpFundingInstructions(ctx, ref, ramp.OfframpFundingRequest{OfframpCreateRequest: req})
	if err != nil {
		return nil, err
	}

	return &ramp.RampOrder{
		Status: models.TxStatusAwaitingFunds,
		DepositInstructions: map[string]any{
			"to_address":   funding.ToAddress,
			"payment_rail": funding.PaymentRail,
			"currency":     funding.Currency,
			"amount":       funding.Amount,
		},
		FeePercent:             req.FeePercent,
		ProviderDestinationRef: destinationID,
		ExpiresAt:              offrampFundingExpiresAt(req.QuoteExpiresAt, req.OfframpFundingWindowMinutes),
	}, nil
}

// CancelOfframp withdraws an off-ramp nobody has funded. Ozav holds no order
// for it yet: CreateOfframp only registers the destination and hands out the
// managed wallet's address, and the payout transfer is posted when the deposit
// settles. There is nothing upstream to cancel, so an order without a transfer
// is canceled here. One with a transfer was already funded and is refused.
func (a *Adapter) CancelOfframp(_ context.Context, _ ramp.CustomerRef, providerOrderID string) error {
	if providerOrderID != "" {
		return errors.New("ozav: cancel offramp: the deposit already settled into payout " + providerOrderID)
	}
	return nil
}

var _ ramp.OfframpCanceler = (*Adapter)(nil)

func offrampFundingExpiresAt(quoteExpires *time.Time, windowMinutes int) *time.Time {
	if windowMinutes <= 0 {
		windowMinutes = ramp.DefaultOzavOfframpFundingWindowMinutes
	}
	windowEnd := time.Now().UTC().Add(time.Duration(windowMinutes) * time.Minute)
	if quoteExpires == nil {
		return &windowEnd
	}
	quote := quoteExpires.UTC()
	if quote.Before(windowEnd) {
		return &quote
	}
	return &windowEnd
}

// SettleOfframp debits the managed wallet into a registered Pix destination.
func (a *Adapter) SettleOfframp(ctx context.Context, ref ramp.CustomerRef, req ramp.OfframpSettleRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if req.ProviderQuoteID == "" {
		return nil, ramp.ErrEstimateRequired
	}
	destinationID := req.ExternalAccountID
	if destinationID == "" {
		return nil, errors.New("ozav: settle off-ramp requires a payout destination id")
	}

	walletID, err := a.resolveWallet(ctx, ref.ExternalID)
	if err != nil {
		return nil, err
	}

	src := transferSource{
		Type:     sourceWallet,
		WalletID: walletID,
		Currency: quoteCurrency(req.Source.Currency),
	}
	if chain := normalizeVocabulary(req.Source.PaymentRail); chain != "" {
		src.Chain = chain
	}

	body := transferCreateRequest{
		CustomerID: ref.ExternalID,
		QuoteID:    req.ProviderQuoteID,
		Source:     src,
		Destination: transferDestination{
			Type:                destinationPayoutTarget,
			PayoutDestinationID: destinationID,
		},
	}

	order, _, err := a.postTransfer(ctx, body, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	order.FeePercent = req.FeePercent
	order.ProviderDestinationRef = destinationID
	return order, nil
}
