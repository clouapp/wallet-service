package ramps

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
)

// BlockedByResource is why one capability is not available. Deliberately the
// same field names responses.ErrorResponse uses for the same three facts, so
// the JIT screen renders identically whether it opened from a 409 or from the
// listing below.
type BlockedByResource struct {
	Reason       string   `json:"reason"`
	Provider     string   `json:"provider,omitempty"`
	PendingSteps []string `json:"pending_steps,omitempty"`
} //	@name	ClientKycBlockedByResource

// CapabilityResource is one (rail, token, chain) tuple and whether the user
// may use it right now.
type CapabilityResource struct {
	Rail      string             `json:"rail"`
	Token     string             `json:"token"`
	Chain     string             `json:"chain"`
	Available bool               `json:"available"`
	BlockedBy *BlockedByResource `json:"blocked_by,omitempty"`
} //	@name	ClientKycCapabilityResource

// PendingProviderResource is one enabled provider still short of every
// capability it might serve — what feeds the banner.
type PendingProviderResource struct {
	Provider     string   `json:"provider"`
	Status       string   `json:"status"`
	PendingSteps []string `json:"pending_steps"`
} //	@name	ClientKycPendingProviderResource

// CapabilitiesResource is GET /api/kyc/capabilities's whole body.
type CapabilitiesResource struct {
	Usable           bool                      `json:"usable"`
	Capabilities     []CapabilityResource      `json:"capabilities"`
	PendingProviders []PendingProviderResource `json:"pending_providers"`
} //	@name	ClientKycCapabilitiesResource

// NewCapabilitiesResource projects the service's view.
func NewCapabilitiesResource(view *kycsvc.CapabilitiesView) CapabilitiesResource {
	capabilities := make([]CapabilityResource, 0, len(view.Capabilities))
	for _, state := range view.Capabilities {
		capabilities = append(capabilities, newCapabilityResource(state))
	}
	pending := make([]PendingProviderResource, 0, len(view.PendingProviders))
	for _, provider := range view.PendingProviders {
		steps := provider.PendingSteps
		if steps == nil {
			steps = []string{}
		}
		pending = append(pending, PendingProviderResource{
			Provider: provider.Provider, Status: provider.Status, PendingSteps: steps,
		})
	}
	return CapabilitiesResource{
		Usable: view.Usable, Capabilities: capabilities, PendingProviders: pending,
	}
}

func newCapabilityResource(state kycsvc.CapabilityState) CapabilityResource {
	resource := CapabilityResource{
		Rail: state.Rail, Token: state.Token, Chain: state.Chain, Available: state.Available,
	}
	if state.BlockedBy == nil {
		return resource
	}
	steps := state.BlockedBy.PendingSteps
	if steps == nil {
		steps = []string{}
	}
	resource.BlockedBy = &BlockedByResource{
		Reason: state.BlockedBy.Reason, Provider: state.BlockedBy.Provider, PendingSteps: steps,
	}
	return resource
}

// ProfileResource is GET /api/kyc/profile's body: the retained fields, never
// an image — ramp.Profile carries none — so pre-filling a new provider's
// wizard from this never re-sends a photograph.
type ProfileResource struct {
	// TaxID is the retained tax id: a CPF, or a passport number. Absent for a
	// profile retained before 24/09/2026, which kept none, and for an SSN or
	// an ITIN, which never leaves whole: a build that predates them reads the
	// type as a CPF, and the app asks for the number again (TaxIDLast4).
	TaxID string `json:"tax_id,omitempty"`
	// TaxIDLast4 is the last four digits of a retained SSN or ITIN, for the
	// app to show; absent for any other tax id.
	TaxIDLast4 string `json:"tax_id_last4,omitempty"`
	// TaxIDType (cpf, passport, ssn, itin) and TaxIDCountry (ISO alpha-3, BRA
	// for a CPF, USA for an SSN or an ITIN) say what the tax id is, derived for
	// a profile retained before they were (a CPF, for one declared in Brazil).
	// Absent when no tax id is retained.
	TaxIDType                string   `json:"tax_id_type,omitempty"`
	TaxIDCountry             string   `json:"tax_id_country,omitempty"`
	FirstName                string   `json:"first_name"`
	LastName                 string   `json:"last_name"`
	LegalName                string   `json:"legal_name"`
	MotherName               string   `json:"mother_name"`
	Email                    string   `json:"email"`
	Phone                    string   `json:"phone"`
	BirthDate                string   `json:"birth_date"`
	Nationality              string   `json:"nationality"`
	StreetLine1              string   `json:"street_line_1"`
	StreetLine2              string   `json:"street_line_2,omitempty"`
	Number                   string   `json:"number"`
	Neighborhood             string   `json:"neighborhood"`
	City                     string   `json:"city"`
	Subdivision              string   `json:"subdivision"`
	PostalCode               string   `json:"postal_code"`
	Country                  string   `json:"country"`
	Occupation               string   `json:"occupation,omitempty"`
	SourceOfFunds            string   `json:"source_of_funds,omitempty"`
	AccountPurpose           string   `json:"account_purpose,omitempty"`
	MonthlyIncomeUSD         *float64 `json:"monthly_income_usd,omitempty"`
	ExpectedMonthlyVolumeUSD *float64 `json:"expected_monthly_volume_usd,omitempty"`
	IsPEP                    *bool    `json:"is_pep,omitempty"`
} //	@name	ClientKycProfileResource

// NewProfileResource projects the retained profile.
func NewProfileResource(profile *ramp.Profile) ProfileResource {
	resource := ProfileResource{
		TaxID:     profile.TaxID,
		FirstName: profile.FirstName, LastName: profile.LastName, LegalName: profile.LegalName,
		MotherName: profile.MotherName, Email: profile.Email, Phone: profile.Phone,
		BirthDate: profile.BirthDate, Nationality: profile.Nationality,
		StreetLine1: profile.Address.StreetLine1, StreetLine2: profile.Address.StreetLine2,
		Number: profile.Address.Number, Neighborhood: profile.Address.Neighborhood,
		City: profile.Address.City, Subdivision: profile.Address.Subdivision,
		PostalCode: profile.Address.PostalCode, Country: profile.Address.Country,
		Occupation: profile.Occupation, SourceOfFunds: profile.SourceOfFunds,
		AccountPurpose:   profile.AccountPurpose,
		MonthlyIncomeUSD: profile.MonthlyIncomeUSD, ExpectedMonthlyVolumeUSD: profile.ExpectedMonthlyVolumeUSD,
		IsPEP: profile.IsPEP,
	}
	if profile.TaxID != "" {
		identity := profile.TaxIdentity()
		resource.TaxIDType, resource.TaxIDCountry = identity.Type, identity.Country
		if identity.IsUSTaxID() {
			resource.TaxID, resource.TaxIDLast4 = "", ustaxid.Last4(profile.TaxID)
		}
	}
	return resource
}
