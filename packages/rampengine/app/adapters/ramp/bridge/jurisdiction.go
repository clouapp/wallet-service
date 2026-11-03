package bridge

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// refusedResidences are the residences Bridge does not serve for Pix, from
// https://apidocs.bridge.xyz/platform/customers/compliance/supported-countries-list
// (checked 2026-10-07): the Prohibited ones, then the ones where PIX = No.
var refusedResidences = map[string]struct{}{
	// Prohibited.
	"AFG": {}, "BLR": {}, "COD": {}, "CUB": {}, "IRN": {}, "IRQ": {}, "LBN": {}, "LBY": {}, "MMR": {},
	"PRK": {}, "PSE": {}, "RUS": {}, "SDN": {}, "SOM": {}, "SSD": {}, "SYR": {}, "VEN": {}, "YEM": {},
	// PIX = No.
	"DZA": {}, "BDI": {}, "CAF": {}, "CHN": {}, "ERI": {}, "GNB": {}, "JPN": {}, "MLI": {}, "TUN": {},
}

// eeaResidences are the residences in the European Economic Area, with the
// EU's outermost regions that carry their own ISO code. Bridge serves them,
// but since 15/06/2026 it requires of a customer living there, by residence,
// what our form does not collect yet: place of birth, nationalities, account
// purpose, both a national ID and a local tax id (whose types vary by
// country), and a photo ID that has not expired
// (https://apidocs.bridge.xyz/platform/customers/customers/eea-updated-requirements).
// A resident submitted without them is left incomplete with nothing the app
// can ask for, so they are not served in this delivery, whatever tax
// identity they declare, until the form collects those fields.
var eeaResidences = map[string]struct{}{
	"AUT": {}, "BEL": {}, "BGR": {}, "CYP": {}, "CZE": {}, "DNK": {}, "ESP": {}, "EST": {}, "FIN": {}, "FRA": {},
	"DEU": {}, "GRC": {}, "HRV": {}, "HUN": {}, "IRL": {}, "ISL": {}, "ITA": {}, "LIE": {}, "LTU": {},
	"LUX": {}, "LVA": {}, "MLT": {}, "NLD": {}, "NOR": {}, "POL": {}, "PRT": {}, "ROU": {}, "SVK": {},
	"SVN": {}, "SWE": {},
	// Outermost regions: Guadeloupe, French Guiana, Saint Martin, Martinique,
	// Mayotte, Réunion.
	"GLP": {}, "GUF": {}, "MAF": {}, "MTQ": {}, "MYT": {}, "REU": {},
}

// usdUnservedResidences are where Bridge has no US ACH/FedWire, from the
// "US ACH/FedWire" column of
// https://apidocs.bridge.xyz/platform/customers/compliance/supported-countries-list
// (checked 2026-10-14). Bridge serves Pix there, so they keep the Pix rails
// and get no USD one.
var usdUnservedResidences = map[string]struct{}{
	"BGD": {}, "HTI": {}, "KEN": {}, "MOZ": {}, "NPL": {}, "NER": {},
	"PAK": {}, "ZWE": {}, "BTN": {}, "XKX": {}, "MAR": {}, "QAT": {},
}

// servesUSD reports whether Bridge offers the USD rails to a resident of code
// (ISO alpha-3).
func servesUSD(code string) bool {
	_, unserved := usdUnservedResidences[code]
	return !unserved
}

// RailsFor is what Bridge opens. For a company, residence is the country it
// is incorporated in, and the answer is businessRails', with no USD rail yet.
// For a person: a CPF holder gets both Pix rails (the payin
// still needs the directional pix_onramp endorsement approved, which is the
// router's concern), a passport holder only the payout, and a resident of the
// United States declaring an SSN or an ITIN only the payout: Bridge grants
// pix_onramp to no non-Brazilian individual and takes neither a CPF nor a
// passport as a US resident's tax id (KYC USA spec §8). Whoever Bridge serves
// at all also gets the three USD rails (the base endorsement decides them),
// unless they live where Bridge has no US ACH/FedWire. A residence Bridge
// refuses, one in the EEA, or one that is not a country, gets nothing.
func (a *Adapter) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	code, ok := iso3166.Alpha3(residence)
	if !ok {
		return nil
	}
	rails := pixRailsFor(identity, code)
	if len(rails) == 0 || identity.IsBusiness() || !servesUSD(code) {
		return rails
	}
	return append(rails, ramp.USDRails()...)
}

// pixRailsFor is the Pix half of RailsFor, for a residence already read as
// an ISO alpha-3 code.
func pixRailsFor(identity ramp.TaxIdentity, code string) []ramp.Rail {
	if _, refused := refusedResidences[code]; refused {
		return nil
	}
	if _, eea := eeaResidences[code]; eea {
		return nil
	}
	if identity.IsBusiness() {
		return businessRails(identity, code)
	}
	if code == "USA" {
		if identity.IsUSTaxID() {
			return []ramp.Rail{ramp.RailPixPayout}
		}
		return nil
	}
	// Bridge relies on the caller for "cpf => BRA": identity.Country is ignored.
	// An ssn or an itin anywhere else falls through to nothing.
	switch identity.Type {
	case models.TaxIDTypeCPF:
		return ramp.PixRails()
	case models.TaxIDTypePassport:
		return []ramp.Rail{ramp.RailPixPayout}
	}
	return nil
}

// businessRails is what Bridge opens to a company incorporated in
// incorporation, by the company's own tax identity and never by whoever
// registers it (KYB spec §7.2): a CNPJ gets both Pix rails; a company from
// abroad only the deposit — Bridge approved pix_onramp for a Florida LLC and a
// London Ltd in the sandbox (18/10/2026), and the router still deposits only
// once it is approved — and never the withdrawal, which a company makes only
// to its own CNPJ key (§18.8). A tax id the country does not issue is no
// company's.
//
// The bank rails (bank_payin, ach_payout, wire_payout, §18.11) join both
// answers when #119/#120 bring them, and do not depend on Pix here as they do
// for a person. The refused set then needs splitting: its "PIX = No" half
// closes Pix, not a bank rail.
func businessRails(identity ramp.TaxIdentity, incorporation string) []ramp.Rail {
	if !identity.FitsIncorporation(incorporation) {
		return nil
	}
	if identity.Type == models.TaxIDTypeCNPJ {
		return ramp.PixRails()
	}
	return []ramp.Rail{ramp.RailPixPayin}
}
