package ozav

import (
	"context"
	"maps"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// CreateOnramp places the pay-in order and returns the Pix instructions the
// user pays. The crypto lands in the customer's Ozav wallet, the only pay-in
// destination Ozav takes; DeliverOnramp moves it on once funded.
//
// The on-ramp is order-first here, unlike Bridge's standing account: Ozav's
// named fiat account (POST /accounts) receives BRL, but nothing reports a Pix
// arriving into it (measured 31/08: deposit.credited is crypto into custody
// only), so nothing could create the transaction.
func (a *Adapter) CreateOnramp(ctx context.Context, ref ramp.CustomerRef, req ramp.OnrampCreateRequest) (*ramp.RampOrder, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	// Without the quote the order would settle at a rate the user was never
	// shown; Ozav would refuse it anyway.
	if req.ProviderQuoteID == "" {
		return nil, ramp.ErrEstimateRequired
	}

	walletID, err := a.resolveWallet(ctx, ref.ExternalID)
	if err != nil {
		return nil, ramp.NotSent(err)
	}

	dest := transferDestination{Type: destinationWallet, WalletID: walletID}
	if chain := normalizeVocabulary(req.Destination.PaymentRail); chain != "" {
		dest.Chain = chain
	}

	body := transferCreateRequest{
		CustomerID: ref.ExternalID,
		QuoteID:    req.ProviderQuoteID,
		Source: transferSource{
			Type:     sourceFiatPayment,
			Currency: quoteCurrency(req.Source.Currency),
			Rail:     pixRailName,
		},
		Destination: dest,
	}

	order, resp, err := a.postTransfer(ctx, body, req.IdempotencyKey)
	if err != nil {
		return nil, err
	}
	order.FeePercent = req.FeePercent
	// The BR Code is what makes the order payable, so it travels as deposit
	// instructions rather than staying buried in Raw.
	if pix := resp.Source.PaymentInstructions; pix != nil {
		order.DepositInstructions = map[string]any{
			"br_code": pix.BRCode, "qr_code_image": pix.QRCodeImage, "intent_id": pix.IntentID,
		}
		maps.DeleteFunc(order.DepositInstructions, func(_ string, value any) bool { return value == "" })
		order.ExpiresAt = pix.ExpiresAt
	}
	return order, nil
}
