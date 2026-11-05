package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// CreateBankAccountRequest validates POST /api/bank-accounts: a US bank account
// to receive USD payouts, held to what Bridge's external account takes
// (external-accounts/validations, checked 2026-10-14).
//
// The address is required: Bridge requires a valid beneficiary address on
// every US external account, ACH included. Only addresses in the United States
// are taken in this delivery (Bridge USD spec Q9).
//
// code carries the MFA step-up, optional here for the reason
// UpdateVirtualAccountRequest gives: whether it is needed is the user's own
// MFA setting, which the service knows.
type CreateBankAccountRequest struct {
	requests.Open
	Label            string                    `form:"label" json:"label"`
	AccountOwnerName string                    `form:"account_owner_name" json:"account_owner_name"`
	BankName         string                    `form:"bank_name" json:"bank_name"`
	RoutingNumber    string                    `form:"routing_number" json:"routing_number"`
	AccountNumber    string                    `form:"account_number" json:"account_number"`
	AccountType      string                    `form:"account_type" json:"account_type"`
	Address          BankAccountAddressRequest `form:"address" json:"address"`
	Code             string                    `form:"code" json:"code"`
	CodeType         string                    `form:"code_type" json:"code_type"`
} //	@name	ClientCreateBankAccountRequest

// BankAccountAddressRequest is the holder's address in the United States.
type BankAccountAddressRequest struct {
	StreetLine1 string `form:"street_line_1" json:"street_line_1"`
	StreetLine2 string `form:"street_line_2" json:"street_line_2"`
	City        string `form:"city" json:"city"`
	State       string `form:"state" json:"state"`
	PostalCode  string `form:"postal_code" json:"postal_code"`
	Country     string `form:"country" json:"country"`
} //	@name	ClientBankAccountAddressRequest

func (r *CreateBankAccountRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"label":                 "required|max_len:64",
		"account_owner_name":    "required|bank_owner_name",
		"bank_name":             "required|max_len:64",
		"routing_number":        "required|aba_routing",
		"account_number":        "required|us_account_number",
		"account_type":          "required|in:checking,savings",
		"address":               "required|map",
		"address.street_line_1": "required|min_len:4|max_len:35|us_street_line",
		"address.street_line_2": "max_len:35",
		"address.city":          "required|max_len:35",
		"address.state":         "required|us_state",
		"address.postal_code":   "required|us_zip",
		"address.country":       "required|in:USA",
		"code":                  "max_len:16",
		"code_type":             "in:totp,backup",
	}
}
