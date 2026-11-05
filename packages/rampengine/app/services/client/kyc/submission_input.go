package kyc

import (
	"errors"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/rules"
	"github.com/macrowallets/waas/packages/rampengine/app/support/clientip"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
)

// ErrDocumentsRequired refuses a documents submission that carries no file.
var ErrDocumentsRequired = errors.New("kyc: no document in the submission")

// ErrDocumentTypeRequired refuses an identity image sent with no
// document_type to name it.
var ErrDocumentTypeRequired = errors.New("kyc: identity image without a document type")

// applicationFrom canonicalises the input into the application every adapter
// reads.
//
// The tax id goes in as an identifying document rather than a profile field:
// it is one of several documents a provider may want, and every adapter
// already knows how to place that list. It is typed by the tax identity the
// application declares (taxIdentityOf), never by the residence.
//
// The phone is canonicalised into E.164 here, which is the one place both the
// single-provider and the fan-out submissions pass through. Adapters below may
// therefore rely on the format instead of each inventing its own reading of
// it — the reason ramp.Profile.Phone documents E.164 at all. Its type is the
// customer's (individualApplication).
func applicationFrom(input dto.ApplicationDTO) ramp.Application {
	app := ramp.Application{
		SignedAgreementID: input.SignedAgreementID,
		Endorsements:      input.Endorsements,
		Profile: ramp.Profile{
			FirstName:      input.FirstName,
			LastName:       input.LastName,
			MotherName:     strings.TrimSpace(input.MotherName),
			Email:          input.Email,
			Phone:          canonicalPhone(input.Phone, input.Country),
			BirthDate:      input.BirthDate,
			Nationality:    strings.ToUpper(strings.TrimSpace(input.Nationality)),
			Occupation:     strings.TrimSpace(input.Occupation),
			SourceOfFunds:  input.SourceOfFunds,
			AccountPurpose: input.AccountPurpose,
			IsPEP:          input.IsPEP,
			Address: ramp.Address{
				StreetLine1:  input.StreetLine1,
				StreetLine2:  input.StreetLine2,
				Number:       input.Number,
				Neighborhood: input.Neighborhood,
				City:         input.City,
				Subdivision:  subdivisionFor(input.Country, input.Subdivision),
				PostalCode:   rules.CanonicalPostalCode(input.PostalCode),
				Country:      input.Country,
			},
		},
	}
	if input.TaxID != "" {
		app.IdentifyingInformation = []ramp.IdentifyingDocument{taxIDDocument(input.TaxID, taxIdentityOf(input))}
	}
	// Bands arrive as keys and leave as the USD each stands for; the table is
	// the backend's so the number can change without an app release. An
	// unknown key cannot reach here — the request rules are rendered from the
	// same table — so a miss is left nil rather than guessed.
	if usd, ok := IncomeUSDForBand(input.MonthlyIncomeBand); ok {
		app.Profile.MonthlyIncomeUSD = &usd
	}
	if usd, ok := VolumeUSDForBand(input.ExpectedMonthlyVolumeBand); ok {
		app.Profile.ExpectedMonthlyVolumeUSD = &usd
	}
	return app
}

// subdivisionFor is the subdivision an address in country carries: Brazil's
// state, or a code from the list of a country whose addresses must name one
// (rules.SubdivisionRequired, the predicate the request reads). Anywhere else
// the bridge_subdivision rule let the value through unread, and Bridge would
// check it against a list that country does not have, refusing at KYC a
// "state" our validation accepted — so it is dropped here, before the
// application is sent or retained.
func subdivisionFor(country, subdivision string) string {
	if !rules.SubdivisionRequired(country) {
		return ""
	}
	return subdivision
}

// taxIdentityOf is the tax identity an application declares: tax_id_type and
// tax_id_country, derived from the residence for a build that sends neither —
// so an installed build, which always sends BRA, declares a CPF.
func taxIdentityOf(input dto.ApplicationDTO) models.TaxIdentity {
	return models.DeriveTaxIdentity(input.TaxIDType, input.TaxIDCountry, input.Country)
}

// taxIDDocument types the tax id by the identity declared: a CPF goes as it
// always has, issued by Brazil and with the number as sent; an SSN or an ITIN
// issued by the United States, by its nine digits (the Bridge adapter spells
// them as Bridge's examples do); a passport issued by its country and with its
// number upper-cased (the kyc_tax_id rule already held it to letters and
// digits). The issuing country is alpha-3 in capitals, as every country in the
// contract is; an adapter spells it as its provider takes it (Bridge in lower
// case).
func taxIDDocument(taxID string, identity models.TaxIdentity) ramp.IdentifyingDocument {
	if identity.IsCPF() {
		return ramp.IdentifyingDocument{Type: models.TaxIDTypeCPF, IssuingCountry: "BRA", Number: taxID}
	}
	if identity.IsUSTaxID() {
		return ramp.IdentifyingDocument{Type: identity.Type, IssuingCountry: "USA", Number: canonicalTaxID(taxID, identity)}
	}
	return ramp.IdentifyingDocument{
		Type:           identity.Type,
		IssuingCountry: identity.Country,
		Number:         strings.ToUpper(strings.TrimSpace(taxID)),
	}
}

// canonicalTaxID is the tax id as it is held: an SSN or an ITIN by its nine
// digits (ustaxid.Canonical), anything else trimmed.
func canonicalTaxID(taxID string, identity models.TaxIdentity) string {
	if identity.IsUSTaxID() {
		return ustaxid.Canonical(taxID)
	}
	return strings.TrimSpace(taxID)
}

// issuingCountry is a document's issuing country as the contract carries it:
// alpha-3 in capitals, whatever case or spelling the client sent (installed
// builds send "bra"). The kyc_country rule has refused what is no country;
// one that reaches here anyway is kept trimmed and upper-cased.
func issuingCountry(country string) string {
	if alpha3, ok := iso3166.Alpha3(country); ok {
		return alpha3
	}
	return strings.ToUpper(strings.TrimSpace(country))
}

// canonicalPhone converts the input's phone into the E.164 form the contract
// promises. The kyc_phone validation rule has already refused anything this
// cannot convert, so the error path is unreachable through the HTTP boundary;
// when it is reached anyway the raw value is passed through untouched rather
// than prefixed with a guessed calling code, and the adapter that needs the
// calling code refuses it locally with a message naming the field.
func canonicalPhone(phone, country string) string {
	normalized, err := ramp.NormalizePhone(phone, country)
	if err != nil {
		return phone
	}
	return normalized
}

// documentsFrom builds the documents every adapter reads, refusing a body
// that cannot make one.
//
// Both refusals are pairings the request rules cannot express:
// required_with/without misfire on struct-bound requests (the zero value
// counts as present).
//
// At least one file per call, and a selfie counts as one on its own. The retry
// the wizard opens is per step (retry_steps.go), and `selfie` is one of them:
// a provider that rejected only the liveness image asks for that image alone,
// and the body answering it carries no identity document and no proof of
// address. Leaving the selfie out of this count refused exactly the retry we
// had asked for. A bank statement counts the same way: Ozav's compliance asks
// for it alone, long after the rest was sent.
//
// document_type stopped being required so a proof-of-address-only retry could
// send that alone, but an image_front it does describe still needs a
// document_type to name it.
func documentsFrom(input dto.DocumentsDTO) (ramp.Documents, error) {
	if input.ImageFront == "" && input.ProofOfAddress == "" && input.Selfie == "" && input.BankStatement == "" {
		return ramp.Documents{}, ErrDocumentsRequired
	}
	if input.ImageFront != "" && input.DocumentType == "" {
		return ramp.Documents{}, ErrDocumentTypeRequired
	}
	docs := ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{{
			Type:           input.DocumentType,
			IssuingCountry: issuingCountry(input.IssuingCountry),
			ImageFront:     input.ImageFront,
			ImageBack:      input.ImageBack,
		}},
		ProofOfAddress:  input.ProofOfAddress,
		BankStatement:   input.BankStatement,
		ClientIPAddress: clientip.Canonical(input.ClientIPAddress),
	}
	if input.Selfie != "" {
		docs.LivenessCheckSelfies = []ramp.Selfie{{Image: input.Selfie}}
	}
	return docs, nil
}
