package bridge

import "encoding/json"

// virtualAccountRequest is the body of POST /customers/{cid}/virtual_accounts:
// a standing deposit target, where every payment becomes its own conversion.
type virtualAccountRequest struct {
	Source      virtualAccountSource      `json:"source"`
	Destination virtualAccountDestination `json:"destination"`
	// DeveloperFeePercent belongs to the account and applies to every deposit
	// until the account is updated.
	DeveloperFeePercent string `json:"developer_fee_percent,omitempty"`
}

type virtualAccountSource struct {
	Currency string `json:"currency"`
}

// virtualAccountDestination is the user's own wallet; XIP never takes custody.
type virtualAccountDestination struct {
	PaymentRail string `json:"payment_rail"`
	Currency    string `json:"currency"`
	Address     string `json:"address"`
}

// virtualAccountUpdateRequest is the body of PUT …/virtual_accounts/{vaid}.
// Every field is omitempty because Bridge treats it as a patch. Production
// accepts only `destination`, `developer_fee_percent` and `fee_config` there;
// activation is POST …/activate or …/deactivate (see UpdateVirtualAccount).
type virtualAccountUpdateRequest struct {
	Destination         *virtualAccountDestination `json:"destination,omitempty"`
	DeveloperFeePercent string                     `json:"developer_fee_percent,omitempty"`
}

const (
	bridgeVirtualAccountActivated   = "activated"
	bridgeVirtualAccountDeactivated = "deactivated"
)

// virtualAccountResponse is a created, fetched or listed account.
type virtualAccountResponse struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	// SourceDepositInstructions is what the user pays into (for PIX the br_code
	// and the holder name). Its shape differs per currency, so it is carried
	// untouched.
	SourceDepositInstructions map[string]any            `json:"source_deposit_instructions"`
	Destination               virtualAccountDestination `json:"destination"`
	DeveloperFeePercent       string                    `json:"developer_fee_percent"`
	// Source is undocumented but decoded as sourceCurrency's fallback: an
	// account whose currency we cannot read is one the listing cannot match,
	// and that is how a user ends up with a second br_code.
	Source virtualAccountSource `json:"source"`
}

// sourceCurrency is the fiat this account collects, lowercase.
func (r virtualAccountResponse) sourceCurrency() string {
	if currency, ok := r.SourceDepositInstructions["currency"].(string); ok && currency != "" {
		return normalizeCurrency(currency)
	}
	return normalizeCurrency(r.Source.Currency)
}

// virtualAccountList keeps its elements raw, so an account found in the listing
// is returned with the same Raw a create would have produced.
type virtualAccountList struct {
	Data []json.RawMessage `json:"data"`
}

// virtualAccountActivity is one entry of an account's history and, byte for
// byte, the `event_object` of a `virtual_account.activity` delivery (pinned by
// testdata/va_activity_*.json).
type virtualAccountActivity struct {
	Type string `json:"type"`
	// Amount and Currency switch legs between activities: fiat on
	// funds_received, crypto on the payment events.
	Amount    string `json:"amount"`
	Currency  string `json:"currency"`
	CreatedAt string `json:"created_at"`
	// DepositID is present only on activities a transaction triggered.
	DepositID        string `json:"deposit_id"`
	CustomerID       string `json:"customer_id"`
	VirtualAccountID string `json:"virtual_account_id"`
	// DeveloperFeeAmount is known from funds_received on, before any receipt.
	DeveloperFeeAmount string `json:"developer_fee_amount"`
	ExchangeFeeAmount  string `json:"exchange_fee_amount"`
	DestinationTxHash  string `json:"destination_tx_hash"`
	// Source describes the payer and is shaped by the rail, so it stays open.
	// It is PII: persisted, never logged.
	Source map[string]any `json:"source"`
	// Receipt is absent until the payment leg runs, and that absence is what
	// marks Amount as the fiat leg.
	Receipt        virtualAccountReceipt `json:"receipt"`
	AccountUpdates map[string]any        `json:"account_updates"`
	// DestinationPaymentRail rides on every activity, account-level ones
	// included, which is what lets an account event report a destination.
	DestinationPaymentRail string `json:"destination_payment_rail"`
}

// virtualAccountReceipt is what settled. It only looks like transferReceipt:
// a deposit reports `exchange_fee` where a transfer reports `exchange_rate`.
// Every field takes part in isEmpty, the unread ones included.
type virtualAccountReceipt struct {
	InitialAmount     string `json:"initial_amount"` // the fiat, before any fee
	FinalAmount       string `json:"final_amount"`   // the crypto delivered
	DeveloperFee      string `json:"developer_fee"`
	ExchangeFee       string `json:"exchange_fee"`
	GasFee            string `json:"gas_fee"`
	DestinationTxHash string `json:"destination_tx_hash"`
	URL               string `json:"url"`
}

// isEmpty reports whether Bridge sent no receipt: only then is the activity's
// `amount` the fiat leg.
func (r virtualAccountReceipt) isEmpty() bool { return r == virtualAccountReceipt{} }

// fiatCurrency is the currency the user sent, or empty when this activity
// cannot say: once a receipt exists `currency` names the token ("usdc" against
// the "usd" that arrived).
func (a virtualAccountActivity) fiatCurrency() string {
	if a.Receipt.isEmpty() {
		return a.Currency
	}
	return ""
}

// updatedValue reads the current value out of an account_update's pair,
// `{"developer_fee_percent": ["0.0", "2.5"]}`: previous first, current second,
// as the captured Wave 0 delivery shows. Any other shape yields nothing, since
// writing the previous destination back would point the next deposit at a
// wallet the user had just replaced.
func (a virtualAccountActivity) updatedValue(name string) string {
	pair, ok := a.AccountUpdates[name].([]any)
	if !ok || len(pair) != 2 {
		return ""
	}
	value, _ := pair[1].(string)
	return value
}

func (a virtualAccountActivity) sourceField(name string) string {
	value, _ := a.Source[name].(string)
	return value
}

// payer returns who sent the money and the reference they attached. The wire
// shape is measured on captured deliveries (the documented `sender_name` does
// not exist there); the PIX shape is Bridge's documented one, unverified until
// BRL virtual accounts are enabled; ACH and FedNow carry `sender_name` and
// `description` (virtual-account-events, checked 2026-10-14). An unrecognised
// rail returns nothing rather than a guessed name, which is an operator's only
// handle on a third-party deposit.
func (a virtualAccountActivity) payer() (name, reference string) {
	switch a.sourceField("payment_rail") {
	case "pix":
		return a.sourceField("sender_name"), a.sourceField("reference")
	case "wire":
		return a.sourceField("originator_name"), a.sourceField("wire_message")
	case "ach_push", "ach_same_day", "fednow":
		return a.sourceField("sender_name"), a.sourceField("description")
	default:
		return "", ""
	}
}
