package bank

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
)

// BeneficiaryRequest is the body of POST and PUT
// /api/offramps/bank/beneficiaries. It checks shape only; the US bank rules
// (ABA checksum, states, NAICS) are app/services/client/bank's, so the two
// endpoints and any later caller share one validation. country is not read:
// it is always US.
type BeneficiaryRequest struct {
	requests.Open
	Label            string `form:"label" json:"label"`
	AccountClass     string `form:"account_class" json:"account_class"`
	AccountType      string `form:"account_type" json:"account_type"`
	Relationship     string `form:"relationship" json:"relationship"`
	BeneficiaryName  string `form:"beneficiary_name" json:"beneficiary_name"`
	RoutingNumber    string `form:"routing_number" json:"routing_number"`
	AccountNumber    string `form:"account_number" json:"account_number"`
	AddressLine1     string `form:"address_line_1" json:"address_line_1"`
	AddressLine2     string `form:"address_line_2" json:"address_line_2"`
	City             string `form:"city" json:"city"`
	StateCode        string `form:"state_code" json:"state_code"`
	PostalCode       string `form:"postal_code" json:"postal_code"`
	BusinessIndustry string `form:"business_industry" json:"business_industry"`
}

func (r *BeneficiaryRequest) Rules(ctx contractshttp.Context) map[string]string {
	return map[string]string{
		"label":             "required|string|max_len:64",
		"account_class":     "required|in:individual,business",
		"account_type":      "required|in:checking,saving",
		"relationship":      "required|string|max_len:32",
		"beneficiary_name":  "required|string|max_len:128",
		"routing_number":    "required|string|max_len:32",
		"account_number":    "required|string|max_len:40",
		"address_line_1":    "required|string|max_len:128",
		"address_line_2":    "string|max_len:128",
		"city":              "required|string|max_len:64",
		"state_code":        "required|string|max_len:8",
		"postal_code":       "required|string|max_len:16",
		"business_industry": "string|max_len:8",
	}
}
