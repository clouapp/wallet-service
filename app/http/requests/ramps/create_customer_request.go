package ramps

import (
	"github.com/goravel/framework/contracts/http"
	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/models"
)

type CreateCustomerRequest struct {
	requests.Open
	ExternalID           string `json:"external_id" form:"external_id"`
	Relationship         string `json:"relationship" form:"relationship"`
	EntityType           string `json:"entity_type" form:"entity_type"`
	Name                 string `json:"name" form:"name"`
	Email                string `json:"email" form:"email"`
	RepresentativeEmail  string `json:"representative_email" form:"representative_email"`
	TaxID                string `json:"tax_id" form:"tax_id"`
	TaxIDType            string `json:"tax_id_type" form:"tax_id_type"`
	IncorporationCountry string `json:"incorporation_country" form:"incorporation_country"`
}

func (*CreateCustomerRequest) Rules(http.Context) map[string]string {
	return map[string]string{"external_id": "required|max_len:255", "relationship": "required|in:account_holder,end_customer", "entity_type": "required|in:individual,business", "name": "required|max_len:255", "email": "required|email|max_len:255", "representative_email": "required_if:entity_type,business|email|max_len:255", "tax_id": "required_if:entity_type,business|max_len:64|business_tax_id", "tax_id_type": "required_if:entity_type,business|in:cnpj,ein,crn,rfc,vat,other", "incorporation_country": "required_if:entity_type,business|len:3|kyc_country"}
}
func (*CreateCustomerRequest) Messages(http.Context) map[string]string   { return nil }
func (*CreateCustomerRequest) Attributes(http.Context) map[string]string { return nil }
func (*CreateCustomerRequest) Filters(http.Context) map[string]string    { return nil }
func (r *CreateCustomerRequest) PrepareForValidation(http.Context) error { return nil }
func (r *CreateCustomerRequest) After(http.Context) map[string][]string {
	if r.Relationship == models.RampAccountHolder && r.EntityType != "business" {
		return map[string][]string{"entity_type": {"account holder must be a business"}}
	}
	return nil
}
func (r *CreateCustomerRequest) Draft() models.RampCustomerDraft {
	return models.RampCustomerDraft{ExternalID: r.ExternalID, Relationship: r.Relationship, EntityType: r.EntityType, Name: r.Name, Email: r.Email, RepresentativeEmail: r.RepresentativeEmail, TaxID: r.TaxID, TaxIDType: r.TaxIDType, IncorporationCountry: r.IncorporationCountry}
}
