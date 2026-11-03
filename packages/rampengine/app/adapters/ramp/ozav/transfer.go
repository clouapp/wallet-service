package ozav

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// GetOrder re-reads a transfer.
func (a *Adapter) GetOrder(ctx context.Context, ref ramp.CustomerRef, providerOrderID string) (*ramp.RampOrder, error) {
	if providerOrderID == "" {
		return nil, errors.New("ozav: get order requires a provider order id")
	}
	resp, raw, err := ramp.Call[transferResponse](ctx, a.do, Request{Method: http.MethodGet, Path: "/transfers/" + providerOrderID})
	if err != nil {
		return nil, err
	}
	return orderFrom(resp, raw), nil
}

// postTransfer creates a transfer and normalizes the answer; the caller adds
// what only it knows.
func (a *Adapter) postTransfer(ctx context.Context, body any, idempotencyKey string) (*ramp.RampOrder, transferResponse, error) {
	resp, raw, err := ramp.Call[transferResponse](ctx, a.do, Request{
		Method: http.MethodPost, Path: "/transfers", Body: body, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return nil, resp, err
	}
	// A retry under a key still claimed answers 202 "processing" and no transfer:
	// the outcome is unknown, so the same key has to be sent again later.
	if resp.ID == "" {
		return nil, resp, fmt.Errorf("ozav: POST /transfers answered %q without a transfer: %w", resp.Status, ramp.ErrRetryableUpstream)
	}
	return orderFrom(resp, raw), resp, nil
}

// orderFrom normalizes a transfer however it arrived: created, re-read or on a
// webhook. Its amounts are what Ozav settled, which wins over the quote on the
// row: the quote floats, and an on-ramp delivery sends the settled token
// amount.
func orderFrom(t transferResponse, raw json.RawMessage) *ramp.RampOrder {
	amounts := map[string]string{
		"source_amount":      t.Amounts.Source.Amount,
		"destination_amount": t.Amounts.Target.Amount,
	}
	// An unreadable or zero rate is left out rather than failing an order that
	// exists either way. A delivery moves no BRL and reports none.
	if rate, ok := brlPerToken(t.Amounts.Source.Currency, t.Amounts.Target.Currency, t.Amounts.Rate); ok {
		amounts["exchange_rate"] = rate
	}
	maps.DeleteFunc(amounts, func(_, value string) bool { return value == "" })
	txHash := strings.TrimSpace(t.TransactionHash)
	order := &ramp.RampOrder{
		ProviderOrderID: t.ID,
		Status:          orderStatusFromTransfer(t, txHash),
		Amounts:         amounts,
		Raw:             raw,
	}
	// Only the delivery's hash is the user's credit. A pay-in's or an
	// off-ramp's lands in deposit_tx_signature too, where it would read as
	// "delivered" and switch the second hop off (rampsvc.OnrampCredited).
	failed := false
	switch strings.ToLower(strings.TrimSpace(t.Status)) {
	case "failed", "cancelled":
		failed = true
		// Ozav's reason for any failed leg: a refused Pix, a payout to a bad
		// key, a delivery the travel rule stopped. The operator reads it on
		// the order instead of an empty failure.
		order.FailureReason = t.StatusDetail.String()
	}
	if t.IsDeliveryTransfer() {
		order.TxHash = txHash
		order.DeliveryFailed = failed
	}
	return order
}

// brlPerToken states an Ozav rate, which is target per source, in BRL per
// token: the unit an estimate and an order's exchange_rate carry in both
// directions, as Bridge's do and the app reads them. An off-ramp's rate
// already is; an on-ramp's is token per BRL (0.1764, measured 31/08) and is
// inverted at the six decimals rates carry. It reports false when neither leg
// is BRL or the rate cannot be inverted.
func brlPerToken(source, target, rate string) (string, bool) {
	switch {
	case strings.EqualFold(target, "BRL"):
		return rate, rate != ""
	case strings.EqualFold(source, "BRL"):
		inverted, err := ramp.DivideAmount("1", rate, rateScale)
		return inverted, err == nil
	}
	return "", false
}

// transferResponse is what POST /transfers, GET /transfers/{id} and a transfer
// event all carry (measured 31/08). No settlement tx hash is decoded: no
// sandbox transfer has reached the chain, so it is not guessed.
type transferResponse struct {
	ID           string       `json:"id"`
	Status       string       `json:"status"`
	StatusDetail statusDetail `json:"status_detail"`
	QuoteID      string       `json:"quote_id"`
	Source       struct {
		// PaymentInstructions is the pay-in's own BR Code, present only on an
		// on-ramp.
		PaymentInstructions *pixPaymentInstructions `json:"payment_instructions"`
		Type                string                  `json:"type"`
	} `json:"source"`
	Destination transferDestination `json:"destination"`
	SourceLeg   *transferLeg        `json:"source_leg"`
	DestLeg     *transferLeg        `json:"destination_leg"`
	// TransactionHash is the on-chain settlement when Ozav exposes it on GET.
	TransactionHash string `json:"transaction_hash"`
	Amounts         struct {
		Source transferAmount `json:"source"`
		Target transferAmount `json:"target"`
		Rate   string         `json:"rate"`
	} `json:"amounts"`
}

// statusDetail is a transfer's failure reason. Live transfers carry it as
// {code, message, retryable} (openapi 2.0.0, and production since 02/11/2026);
// older sandbox captures and the devstub carried a bare string. A field that
// accepts only one of them fails the whole decode, and a webhook decoded that
// way names no order at all.
type statusDetail struct {
	Code    string
	Message string
}

func (d *statusDetail) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		*d = statusDetail{Code: strings.TrimSpace(text)}
		return nil
	}
	var object struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		return fmt.Errorf("ozav: decode status_detail: %w", err)
	}
	*d = statusDetail{Code: strings.TrimSpace(object.Code), Message: strings.TrimSpace(object.Message)}
	return nil
}

// String is what the operator reads on the order: the code, and Ozav's
// provider-neutral message when there is one.
func (d statusDetail) String() string {
	if d.Message == "" || d.Code == "" {
		return d.Code + d.Message
	}
	return d.Code + ": " + d.Message
}

type transferAmount struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

// transferCreateRequest is the body of POST /transfers; the (source.type,
// destination.type) pair selects the flow.
type transferCreateRequest struct {
	CustomerID string `json:"customer_id"`
	// QuoteID is required once the currencies differ and is authoritative for
	// the amount; sending an amount beside it is a 400.
	QuoteID     string              `json:"quote_id,omitempty"`
	Source      transferSource      `json:"source"`
	Destination transferDestination `json:"destination"`
}

type transferSource struct {
	Type     string `json:"type"`
	Currency string `json:"currency,omitempty"`
	Chain    string `json:"chain,omitempty"`
	Rail     string `json:"rail,omitempty"`
	WalletID string `json:"wallet_id,omitempty"`
}

type transferDestination struct {
	Type     string `json:"type"`
	WalletID string `json:"wallet_id,omitempty"`
	// Chain is required for some on-ramps (e.g. BRS on Solana) when the managed
	// wallet does not resolve to a single network (Ozav: DESTINATION_CHAIN_REQUIRED).
	Chain               string `json:"chain,omitempty"`
	PayoutDestinationID string `json:"payout_destination_id,omitempty"`
	// Address, Network and Currency describe a crypto_address (delivery.go).
	Address  string `json:"address,omitempty"`
	Network  string `json:"network,omitempty"`
	Currency string `json:"currency,omitempty"`
}

const (
	sourceFiatPayment       = "fiat_payment"
	sourceWallet            = "wallet"
	destinationWallet       = "wallet"
	destinationPayoutTarget = "payout_destination"
)

// pixPaymentInstructions is the pay-in's own BR Code, not an account's
// standing key.
type pixPaymentInstructions struct {
	Rail        string     `json:"rail"`
	BRCode      string     `json:"br_code"`
	IntentID    string     `json:"intent_id"`
	QRCodeImage string     `json:"qr_code_image"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

// resolveWallet returns the customer's Ozav wallet, which an on-ramp lands in
// and an off-ramp or a delivery spends from. The caller holds no id for it:
// XIP stores the user's own address, which a pay-in may not target
// (fiat_payment -> crypto_address is a measured 422 UNSUPPORTED_COMBINATION).
func (a *Adapter) resolveWallet(ctx context.Context, customerID string) (string, error) {
	var resp walletsResponse
	if err := a.do(ctx, Request{
		Method: http.MethodGet,
		Path:   "/wallets",
		Query:  url.Values{"customer_id": {customerID}},
	}, &resp); err != nil {
		return "", err
	}
	// Checked here: Ozav's 422 WALLET_NOT_MANAGED names a wallet id nobody
	// chose, when the condition is the customer's custody model, which only
	// Ozav support can migrate.
	for _, wallet := range resp.Data {
		if wallet.ID == "" || wallet.Status != walletActive {
			continue
		}
		if wallet.CustodyModel != custodyManaged {
			return "", fmt.Errorf(
				"ozav: customer %s holds a %s wallet; these movements need managed custody, "+
					"which only Ozav support can migrate to", customerID, wallet.CustodyModel)
		}
		return wallet.ID, nil
	}
	return "", fmt.Errorf("ozav: customer %s has no active wallet to receive the on-ramp: %w",
		customerID, ramp.ErrIntegrationRequired)
}

type walletsResponse struct {
	Data []walletResponse `json:"data"`
}

type walletResponse struct {
	ID           string `json:"id"`
	CustodyModel string `json:"custody_model"`
	Status       string `json:"status"`
}

// walletActive is the only status that receives: a deactivated wallet does
// not, in Ozav's own words.
const walletActive = "active"
