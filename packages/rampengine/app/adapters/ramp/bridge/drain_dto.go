package bridge

import "encoding/json"

// drainResponse is one drain of a liquidation address: crypto that arrived and
// its way to the bank account. It is the event_object of a
// liquidation_address.drain webhook and an element of the drains history.
type drainResponse struct {
	ID                   string           `json:"id"`
	Amount               string           `json:"amount"`
	Currency             string           `json:"currency"`
	State                string           `json:"state"`
	CustomerID           string           `json:"customer_id"`
	LiquidationAddressID string           `json:"liquidation_address_id"`
	DepositTxHash        string           `json:"deposit_tx_hash"`
	FromAddress          string           `json:"from_address"`
	Destination          drainDestination `json:"destination"`
	Receipt              drainReceipt     `json:"receipt"`
	ReturnDetails        drainReturn      `json:"return_details"`
	CreatedAt            string           `json:"created_at"`
	UpdatedAt            string           `json:"updated_at"`
}

// drainDestination is the bank leg. It names the user's external account and
// the bank's own reference for the payment: imad on a wire, trace_number on
// ACH.
type drainDestination struct {
	PaymentRail       string `json:"payment_rail"`
	Currency          string `json:"currency"`
	ExternalAccountID string `json:"external_account_id"`
	IMAD              string `json:"imad"`
	TraceNumber       string `json:"trace_number"`
}

// drainReceipt settles the drain. It is absent until Bridge has priced it.
type drainReceipt struct {
	InitialAmount   string `json:"initial_amount"`
	DeveloperFee    string `json:"developer_fee"`
	SubtotalAmount  string `json:"subtotal_amount"`
	ExchangeRate    string `json:"exchange_rate"`
	ConvertedAmount string `json:"converted_amount"`
	OutgoingAmount  string `json:"outgoing_amount"`
}

// drainReturn is why a drain came back.
type drainReturn struct {
	Reason string `json:"reason"`
}

type drainList struct {
	Data []json.RawMessage `json:"data"`
}

// simulateDepositResponse is the sandbox's answer. Success is a pointer
// because the answer has carried it only sometimes: absent, a drain id alone
// is the success.
type simulateDepositResponse struct {
	Success *bool  `json:"success"`
	DrainID string `json:"drain_id"`
}
