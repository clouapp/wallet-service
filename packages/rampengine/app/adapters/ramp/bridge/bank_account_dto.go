package bridge

// usAccountType is what Bridge calls a US bank account destination.
const usAccountType = "us"

// usExternalAccountRequest is the body of POST /customers/{id}/external_accounts
// for a US account, adapted from PR #61 with what the docs require of an
// individual: first_name and last_name beside account_owner_name. It is its own
// type, apart from the PIX externalAccountRequest, so registering a bank
// account cannot change what a PIX key registration sends.
type usExternalAccountRequest struct {
	Currency         string                 `json:"currency"`
	AccountType      string                 `json:"account_type"`
	BankName         string                 `json:"bank_name,omitempty"`
	AccountName      string                 `json:"account_name,omitempty"`
	AccountOwnerType string                 `json:"account_owner_type"`
	AccountOwnerName string                 `json:"account_owner_name"`
	FirstName        string                 `json:"first_name"`
	LastName         string                 `json:"last_name"`
	Account          usBankAccountPayload   `json:"account"`
	Address          externalAccountAddress `json:"address"`
}

// usBankAccountPayload is the `account` object. The number is write-only:
// Bridge answers with last_4 instead.
type usBankAccountPayload struct {
	RoutingNumber     string `json:"routing_number"`
	AccountNumber     string `json:"account_number,omitempty"`
	Last4             string `json:"last_4,omitempty"`
	CheckingOrSavings string `json:"checking_or_savings"`
}

// externalAccountAddress is the holder's address. Bridge requires one on every
// US account; `state` is the ISO 3166-2 code without the country prefix.
type externalAccountAddress struct {
	StreetLine1 string `json:"street_line_1"`
	StreetLine2 string `json:"street_line_2,omitempty"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	Country     string `json:"country"`
}

// usExternalAccountResponse is the registered account. Only id and
// payment_rails are read; the rest is decoded for the tests that pin the
// shape the docs describe.
type usExternalAccountResponse struct {
	ID                      string                `json:"id"`
	CustomerID              string                `json:"customer_id"`
	BankName                string                `json:"bank_name"`
	AccountName             string                `json:"account_name"`
	AccountOwnerName        string                `json:"account_owner_name"`
	Active                  *bool                 `json:"active"`
	Currency                string                `json:"currency"`
	AccountOwnerType        string                `json:"account_owner_type"`
	AccountType             string                `json:"account_type"`
	FirstName               string                `json:"first_name"`
	LastName                string                `json:"last_name"`
	Account                 *usBankAccountPayload `json:"account"`
	BeneficiaryAddressValid *bool                 `json:"beneficiary_address_valid"`
	Last4                   string                `json:"last_4"`
	// PaymentRails is documented only in the USD guide's FedNow section; its
	// absence decodes to an empty list, which is not a refusal.
	PaymentRails        paymentRailsResponse `json:"payment_rails"`
	DeactivationReason  string               `json:"deactivation_reason"`
	DeactivationDetails string               `json:"deactivation_details"`
}

type paymentRailsResponse struct {
	Supported          []string          `json:"supported"`
	UnsupportedReasons map[string]string `json:"unsupported_reasons"`
}
