package bridge

import "encoding/json"

// Bridge's names for the bank rails a liquidation address pays over.
const (
	bridgeRailACH  = "ach"
	bridgeRailWire = "wire"
)

// The address states the adapter reads. Anything but active is reported
// inactive; only deactivated is skipped when looking for a route to reuse.
const (
	bridgeLiquidationAddressActive      = "active"
	bridgeLiquidationAddressDeactivated = "deactivated"
)

// liquidationAddressPageLimit bounds the listing read before a create. XIP
// opens one address per bank account, rail and token, so a page this size is
// the whole set.
const liquidationAddressPageLimit = 100

// liquidationAddressRequest is the body of POST
// /customers/{id}/liquidation_addresses. The reference goes in the field of
// the rail: destination_ach_reference on ACH, destination_wire_message on wire.
type liquidationAddressRequest struct {
	Chain                     string                        `json:"chain"`
	Currency                  string                        `json:"currency"`
	ExternalAccountID         string                        `json:"external_account_id"`
	DestinationPaymentRail    string                        `json:"destination_payment_rail"`
	DestinationCurrency       string                        `json:"destination_currency"`
	DestinationACHReference   string                        `json:"destination_ach_reference,omitempty"`
	DestinationWireMessage    string                        `json:"destination_wire_message,omitempty"`
	ReturnInstructions        *liquidationReturnInstruction `json:"return_instructions,omitempty"`
	CustomDeveloperFeePercent string                        `json:"custom_developer_fee_percent,omitempty"`
}

// liquidationReturnInstruction is where Bridge sends the crypto back when a
// drain cannot be paid out.
type liquidationReturnInstruction struct {
	Address string `json:"address"`
}

// liquidationAddressResponse is a created, fetched or listed address.
type liquidationAddressResponse struct {
	ID                        string `json:"id"`
	Address                   string `json:"address"`
	State                     string `json:"state"`
	Chain                     string `json:"chain"`
	Currency                  string `json:"currency"`
	ExternalAccountID         string `json:"external_account_id"`
	DestinationPaymentRail    string `json:"destination_payment_rail"`
	DestinationCurrency       string `json:"destination_currency"`
	DestinationACHReference   string `json:"destination_ach_reference"`
	DestinationWireMessage    string `json:"destination_wire_message"`
	CustomDeveloperFeePercent string `json:"custom_developer_fee_percent"`
	// ReturnInstructions is where a drain Bridge cannot pay out goes back.
	ReturnInstructions *liquidationReturnInstruction `json:"return_instructions"`
}

type liquidationAddressList struct {
	Data []json.RawMessage `json:"data"`
}
