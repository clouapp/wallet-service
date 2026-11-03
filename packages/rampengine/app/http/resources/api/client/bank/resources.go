package bank

import (
	"time"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
	banksvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/bank"
	"github.com/macrowallets/waas/packages/rampengine/app/support/wiretime"
)

// BeneficiarySummary is a saved bank account as a list shows it: no name, no
// address, the numbers masked to their last four digits.
type BeneficiarySummary struct {
	ID                  string    `json:"id"`
	Label               string    `json:"label"`
	AccountClass        string    `json:"account_class" enums:"individual,business"`
	AccountType         string    `json:"account_type" enums:"checking,saving"`
	Relationship        string    `json:"relationship"`
	AccountNumberMasked string    `json:"account_number_masked" example:"••••6789"`
	RoutingNumberMasked string    `json:"routing_number_masked" example:"••••0021"`
	Revision            int       `json:"revision"`
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// BeneficiaryDetail adds what the edit screen needs. The numbers stay masked;
// a PUT sends them again in full.
type BeneficiaryDetail struct {
	BeneficiarySummary
	BeneficiaryName  string  `json:"beneficiary_name"`
	AddressLine1     string  `json:"address_line_1"`
	AddressLine2     *string `json:"address_line_2"`
	City             string  `json:"city"`
	StateCode        string  `json:"state_code"`
	PostalCode       string  `json:"postal_code"`
	Country          string  `json:"country" example:"US"`
	BusinessIndustry *string `json:"business_industry"`
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func NewBeneficiarySummary(rec *dto.BeneficiaryRecordDTO) BeneficiarySummary {
	return BeneficiarySummary{
		ID: rec.ExternalID, Label: rec.Label, AccountClass: rec.AccountClass, AccountType: rec.AccountType,
		Relationship:        rec.Relationship,
		AccountNumberMasked: banksvc.Mask(rec.AccountLast4), RoutingNumberMasked: banksvc.Mask(rec.RoutingLast4),
		Revision:  rec.Revision,
		CreatedAt: wiretime.Of(rec.CreatedAt), UpdatedAt: wiretime.Of(rec.UpdatedAt),
	}
}

func NewBeneficiaryDetail(rec *dto.BeneficiaryRecordDTO) BeneficiaryDetail {
	return BeneficiaryDetail{
		BeneficiarySummary: NewBeneficiarySummary(rec),
		BeneficiaryName:    rec.BeneficiaryName, AddressLine1: rec.AddressLine1, AddressLine2: nilIfEmpty(rec.AddressLine2),
		City: rec.City, StateCode: rec.StateCode, PostalCode: rec.PostalCode, Country: "US",
		BusinessIndustry: nilIfEmpty(rec.BusinessIndustry),
	}
}

func NewBeneficiaryCollection(recs []dto.BeneficiaryRecordDTO) []BeneficiarySummary {
	out := make([]BeneficiarySummary, 0, len(recs))
	for i := range recs {
		out = append(out, NewBeneficiarySummary(&recs[i]))
	}
	return out
}
