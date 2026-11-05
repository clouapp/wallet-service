package blindpay

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/digits"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// The payload fields the uploaded files fill, in the order they are uploaded.
const (
	fieldIDDocFront = "id_doc_front_file"
	fieldIDDocBack  = "id_doc_back_file"
	fieldSelfie     = "selfie_file"
)

// maxOccupationLen is BlindPay's maxLength on occupation (characters).
const maxOccupationLen = 255

// customerCreateRequest is POST /instances/{id}/customers for an individual
// with standard KYC. Every tag is the OpenAPI property's own name (see
// TestCustomerCreateRequestMatchesTheContract); empty fields are omitted, so
// the body states only what the user declared.
type customerCreateRequest struct {
	Type                string `json:"type"`
	KycType             string `json:"kyc_type"`
	Email               string `json:"email"`
	TaxID               string `json:"tax_id,omitempty"`
	FirstName           string `json:"first_name,omitempty"`
	LastName            string `json:"last_name,omitempty"`
	DateOfBirth         string `json:"date_of_birth,omitempty"`
	PhoneNumber         string `json:"phone_number,omitempty"`
	AddressLine1        string `json:"address_line_1,omitempty"`
	AddressLine2        string `json:"address_line_2,omitempty"`
	City                string `json:"city,omitempty"`
	StateProvinceRegion string `json:"state_province_region,omitempty"`
	Country             string `json:"country"`
	PostalCode          string `json:"postal_code,omitempty"`
	IPAddress           string `json:"ip_address,omitempty"`
	TosID               string `json:"tos_id,omitempty"`
	ExternalID          string `json:"external_id,omitempty"`
	IDDocCountry        string `json:"id_doc_country,omitempty"`
	IDDocType           string `json:"id_doc_type,omitempty"`
	IDDocFrontFile      string `json:"id_doc_front_file,omitempty"`
	IDDocBackFile       string `json:"id_doc_back_file,omitempty"`
	SelfieFile          string `json:"selfie_file,omitempty"`
	AccountPurpose      string `json:"account_purpose,omitempty"`
	Occupation          string `json:"occupation,omitempty"`
	SourceOfWealth      string `json:"source_of_wealth,omitempty"`
}

// idDocTypes maps the canonical document types onto BlindPay's id_doc_type.
var idDocTypes = map[string]string{
	"passport":        "PASSPORT",
	"national_id":     "ID_CARD",
	"drivers_license": "DRIVERS",
}

// accountPurposes maps XIP's account-purpose keys (kyc.AccountPurposeKeys,
// copied here: an adapter does not import the service) onto BlindPay's
// account_purpose. A key with no entry is omitted rather than guessed.
//
// "other" is deliberately absent: BlindPay requires account_purpose_other
// alongside it, and XIP collects no free text for it, so sending it would
// make the create fail after the files are already uploaded.
var accountPurposes = map[string]string{
	"receive_payments":       "receive_payments_for_goods_and_services",
	"send_to_family_friends": "payments_to_friends_or_family_abroad",
	"pay_goods_services":     "purchase_goods_and_services",
	"hold_crypto":            "investment_purposes",
}

// sourcesOfWealth maps XIP's source-of-funds keys (kyc.SourceOfFundsKeys)
// onto BlindPay's source_of_wealth. loan, marital, grant_government and
// other have no honest counterpart and are omitted.
var sourcesOfWealth = map[string]string{
	"employment_salary": "salary_or_wages",
	"savings":           "savings",
	"investment":        "investments",
	"business_income":   "business_dividends_or_profits",
	"company_capital":   "client_investor_contributions",
	"real_estate":       "asset_sales",
	"trust":             "inheritance",
	"private_capital":   "investments",
	"winnings":          "gambling",
}

// atomicImages are the decoded images of one submission, already checked.
type atomicImages struct {
	document  ramp.IdentifyingDocument
	front     decodedImage
	back      *decodedImage
	selfie    decodedImage
	ipAddress string
}

type decodedImage struct {
	mime string
	data []byte
}

// buildCustomerCreateRequest maps a validated submission and the handles of
// its uploaded files onto the create body.
func buildCustomerCreateRequest(sub ramp.AtomicKycSubmission, images atomicImages, handles map[string]string) (customerCreateRequest, error) {
	app := sub.Application
	p := app.Profile
	country, ok := iso3166.Alpha2(p.Address.Country)
	if !ok {
		return customerCreateRequest{}, invalid("address country is not a known country")
	}
	region, err := stateProvinceRegion(p.Address.Subdivision, country)
	if err != nil {
		return customerCreateRequest{}, err
	}
	docCountry, ok := iso3166.Alpha2(images.document.IssuingCountry)
	if !ok {
		return customerCreateRequest{}, invalid("document issuing country is not a known country")
	}
	return customerCreateRequest{
		Type:                "individual",
		KycType:             "standard",
		Email:               strings.TrimSpace(p.Email),
		TaxID:               taxID(app),
		FirstName:           strings.TrimSpace(p.FirstName),
		LastName:            strings.TrimSpace(p.LastName),
		DateOfBirth:         strings.TrimSpace(p.BirthDate) + "T00:00:00Z",
		PhoneNumber:         strings.TrimSpace(p.Phone),
		AddressLine1:        joinNonEmpty(" ", p.Address.StreetLine1, p.Address.Number),
		AddressLine2:        joinNonEmpty(", ", p.Address.StreetLine2, p.Address.Neighborhood),
		City:                strings.TrimSpace(p.Address.City),
		StateProvinceRegion: region,
		Country:             country,
		PostalCode:          strings.TrimSpace(p.Address.PostalCode),
		IPAddress:           images.ipAddress,
		TosID:               app.SignedAgreementID,
		ExternalID:          sub.Reference,
		IDDocCountry:        docCountry,
		IDDocType:           idDocTypes[images.document.Type],
		IDDocFrontFile:      handles[fieldIDDocFront],
		IDDocBackFile:       handles[fieldIDDocBack],
		SelfieFile:          handles[fieldSelfie],
		AccountPurpose:      accountPurposes[p.AccountPurpose],
		Occupation:          truncateRunes(strings.TrimSpace(p.Occupation), maxOccupationLen),
		SourceOfWealth:      sourcesOfWealth[p.SourceOfFunds],
	}, nil
}

// taxID is the number the application carries for the person — in
// IdentifyingInformation, where every adapter reads it — or, failing that,
// the one retained on the profile.
func taxID(app ramp.Application) string {
	for _, doc := range app.IdentifyingInformation {
		if number := strings.TrimSpace(doc.Number); number != "" {
			return normalizedTaxID(doc.Type, number)
		}
	}
	return normalizedTaxID(app.Profile.TaxIDType, strings.TrimSpace(app.Profile.TaxID))
}

// normalizedTaxID spells a CPF as its digits, the way BlindPay takes it; any
// other number goes as declared.
func normalizedTaxID(taxIDType, number string) string {
	if taxIDType == models.TaxIDTypeCPF {
		return digits.Only(number)
	}
	return number
}

// stateProvinceRegion is the subdivision as BlindPay spells it: the code
// after an ISO 3166-2 country prefix ("BR-SP" → "SP"), in capitals. A US
// address must name its state by the two-letter code.
func stateProvinceRegion(subdivision, alpha2 string) (string, error) {
	region := strings.ToUpper(strings.TrimSpace(subdivision))
	if prefix, code, ok := strings.Cut(region, "-"); ok && len(prefix) == 2 {
		region = code
	}
	if alpha2 == "US" && !twoLetters(region) {
		return "", invalid("a US address needs its two-letter state code")
	}
	return region, nil
}

func twoLetters(s string) bool {
	return len(s) == 2 && s[0] >= 'A' && s[0] <= 'Z' && s[1] >= 'A' && s[1] <= 'Z'
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, sep)
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// validBirthDate is a real calendar date written YYYY-MM-DD.
func validBirthDate(s string) bool {
	parsed, err := time.Parse(time.DateOnly, s)
	return err == nil && parsed.Format(time.DateOnly) == s
}
