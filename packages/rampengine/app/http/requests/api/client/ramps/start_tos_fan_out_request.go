package ramps

import (
	contractshttp "github.com/goravel/framework/contracts/http"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/http/requests"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
)

// StartTosFanOutRequest validates POST /api/kyc/tos-sessions.
//
// Its fields mirror StartTosRequest, minus provider: the client asks for the
// terms it still owes and does not name who owes them to, which is the whole
// point of a multi-provider stack. It is a distinct struct rather than an
// embedding so a field this endpoint does not accept cannot still surface in
// its generated schema.
type StartTosFanOutRequest struct {
	requests.Open
	RedirectURI string `form:"redirect_uri" json:"redirect_uri"`
	// Modes is what this client can present. Terms come in two shapes now —
	// a page the provider hosts and signs, and a document XIP presents with a
	// checkbox of its own — and a build that only knows the first would sit
	// forever on a session of the second, waiting for a page that never
	// answers. The terms step gates the wizard, so that stalls every other
	// provider's KYC too.
	//
	// Omitting it means hosted only, which is what every build shipped before
	// 02/09/2026 understands.
	Modes []string `form:"modes" json:"modes"`
	// Country, TaxIDType and TaxIDCountry are the identity the user is about
	// to declare — the residence, cpf, passport, ssn or itin, and the
	// country that issued it — so only the terms of providers that serve that
	// person are offered. All optional, and validated like the application's
	// when present; a build that predates them sends none.
	Country      string `form:"country" json:"country"`
	TaxIDType    string `form:"tax_id_type" json:"tax_id_type"`
	TaxIDCountry string `form:"tax_id_country" json:"tax_id_country"`
}

// Input copies the request into the service's input as the client sent it.
func (r StartTosFanOutRequest) Input() dto.StartTosDTO {
	return dto.StartTosDTO{
		RedirectURI:  r.RedirectURI,
		Modes:        r.Modes,
		Country:      r.Country,
		TaxIDType:    r.TaxIDType,
		TaxIDCountry: r.TaxIDCountry,
	}
}

// Rules reuses StartTosRequest's table for the field the two share, minus the
// one this endpoint does not take — the same reason the submission fan-out
// requests do it.
func (r *StartTosFanOutRequest) Rules(ctx contractshttp.Context) map[string]string {
	rules := (&StartTosRequest{}).Rules(ctx)
	delete(rules, "provider")
	// A closed vocabulary: an unknown mode must not read as a capability, or a
	// typo in a future client would hand it terms it cannot finish.
	rules["modes.*"] = "in:hosted,attested"
	rules["country"] = "len:3|kyc_country"
	rules["tax_id_type"] = kycsvc.RuleIn(models.PersonTaxIDTypes)
	rules["tax_id_country"] = "len:3|kyc_country"
	return rules
}
