package bridge

import (
	"cmp"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// transferRequest is the body of POST /transfers: Bridge takes the source leg,
// converts, and pays out the destination leg.
type transferRequest struct {
	Amount     string `json:"amount"` // in the source currency
	OnBehalfOf string `json:"on_behalf_of"`
	// DeveloperFeePercent is XIP's cut, sent unrounded: Bridge applies it to
	// the settled amount, and rounding here would disagree with the receipt.
	DeveloperFeePercent string      `json:"developer_fee_percent,omitempty"`
	Source              transferLeg `json:"source"`
	Destination         transferLeg `json:"destination"`
}

// transferLeg is one side of a transfer: a crypto source names from_address,
// a crypto destination to_address, a PIX destination external_account_id. The
// addresses are the user's own wallet; XIP never takes custody.
type transferLeg struct {
	PaymentRail       string `json:"payment_rail"`
	Currency          string `json:"currency"`
	FromAddress       string `json:"from_address,omitempty"`
	ToAddress         string `json:"to_address,omitempty"`
	ExternalAccountID string `json:"external_account_id,omitempty"`
}

type transferResponse struct {
	ID     string `json:"id"`
	State  string `json:"state"`
	Amount string `json:"amount"`
	// SourceDepositInstructions is what the user acts on (a br_code, a deposit
	// address). Its shape differs per rail, so it is carried untouched.
	SourceDepositInstructions map[string]any  `json:"source_deposit_instructions"`
	Receipt                   transferReceipt `json:"receipt"`
}

type transferReceipt struct {
	InitialAmount string `json:"initial_amount"`
	DeveloperFee  string `json:"developer_fee"`
	// ExchangeFee is Bridge's own conversion cut, in the source currency.
	ExchangeFee       string `json:"exchange_fee"`
	ExchangeRate      string `json:"exchange_rate"`
	SubtotalAmount    string `json:"subtotal_amount"`
	FinalAmount       string `json:"final_amount"`
	DestinationTxHash string `json:"destination_tx_hash"`
}

// externalAccountRequest registers a payout destination: a PIX transfer names
// an account id, so the key has to become one first.
type externalAccountRequest struct {
	Currency         string `json:"currency"`
	AccountType      string `json:"account_type"`
	AccountOwnerType string `json:"account_owner_type,omitempty"`
	AccountOwnerName string `json:"account_owner_name,omitempty"`
	// BusinessName is the company's name, for an account_owner_type of
	// business; Bridge wants account_owner_name beside it all the same.
	BusinessName string         `json:"business_name,omitempty"`
	PixKey       *pixKeyPayload `json:"pix_key,omitempty"`
}

type pixKeyPayload struct {
	PixKey         string `json:"pix_key"`
	DocumentNumber string `json:"document_number,omitempty"`
}

// externalAccountResponse is read for its id only; Bridge returns the stored
// key masked (see ensureExternalAccount).
type externalAccountResponse struct {
	ID string `json:"id"`
}

// exchangeRateResponse is the courtesy rate behind an estimate. The field
// names have moved between revisions of Bridge's docs, so the market is
// `midmarket_rate` when present and `rate` otherwise.
//
// Bridge converts at the side of the market that costs the user, 0.55% away
// (live, 02/10/2026: usd→brl mid 5.2108, buy 5.239459, sell 5.182141), and its
// transfer receipts call the gap "exchange_fee". Quoting the market promised
// every off-ramp 0.55% more than it settled.
type exchangeRateResponse struct {
	MidmarketRate string `json:"midmarket_rate"`
	Rate          string `json:"rate"`
	BuyRate       string `json:"buy_rate"`
	SellRate      string `json:"sell_rate"`
}

func (r exchangeRateResponse) market() string { return cmp.Or(r.MidmarketRate, r.Rate) }

// applied is the rate Bridge converts at: the sell side when the pair is the
// one asked for, the buy side when the amount divides by the inverse (more
// reais per dollar of the inverse is fewer reais out). Without the sides it is
// the market.
func (r exchangeRateResponse) applied(inverse bool) string {
	if inverse {
		return cmp.Or(r.BuyRate, r.market())
	}
	return cmp.Or(r.SellRate, r.market())
}

// depositExpiry is when the br_code or the deposit address stops being
// payable. An unreadable value yields nil: the order exists either way, and
// failing it over a countdown would be worse.
func depositExpiry(instructions map[string]any) *time.Time {
	value, _ := instructions["expires_at"].(string)
	return ramp.ParseTime(value)
}
