package kyc

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ports_test.go can only prove the contract struct holds Number and
// Neighborhood; a struct literal compiles either way. Only exercising
// applicationFrom catches the mapping being dropped.
func TestApplicationFrom_CarriesNumberAndNeighborhood(t *testing.T) {
	app := applicationFrom(dto.ApplicationDTO{
		StreetLine1:  "Avenida Paulista",
		Number:       "1000",
		Neighborhood: "Bela Vista",
		City:         "São Paulo",
		Subdivision:  "SP",
		PostalCode:   "01310000",
		Country:      "BRA",
	})

	assert.Equal(t, "1000", app.Profile.Address.Number)
	assert.Equal(t, "Bela Vista", app.Profile.Address.Neighborhood)
}

// The bridge_subdivision rule ignores a subdivision for a country that is
// neither Brazil nor one whose addresses must name one, and Bridge checks the
// value against a list that country does not have: it would refuse at KYC a
// "state" the form let through. So only Brazil's state and a code from the
// country's list travel.
func TestApplicationFrom_DropsASubdivisionTheCountryDoesNotUse(t *testing.T) {
	cases := []struct {
		name, country, subdivision, want string
	}{
		{"brazil keeps its state", "BRA", "SP", "SP"},
		{"an installed build's lower-case brazil keeps it", "bra", "Minas Gerais", "Minas Gerais"},
		{"a country with a required list keeps its code", "ARE", "DU", "DU"},
		{"a country with no list drops it", "PRT", "Lisboa", ""},
		{"lower-case spelling of a country with no list drops it", "gbr", "London", ""},
		{"a country with no list and nothing sent", "ARG", "", ""},
		{"the united states keeps its state", "USA", "CA", "CA"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := applicationFrom(dto.ApplicationDTO{Country: c.country, Subdivision: c.subdivision})
			assert.Equal(t, c.want, app.Profile.Address.Subdivision)
		})
	}
}

// A postal code goes out as its country prints it: Bridge checks it against
// patterns written in capitals, and the rule already read it that way.
func TestApplicationFrom_UpperCasesThePostalCode(t *testing.T) {
	for _, c := range []struct{ country, postalCode, want string }{
		{"GBR", " sw1a 1aa ", "SW1A 1AA"},
		{"CAN", "k1a 0b1", "K1A 0B1"},
		{"BRA", "01310-100", "01310-100"},
		{"ARE", "", ""},
	} {
		t.Run(c.country, func(t *testing.T) {
			app := applicationFrom(dto.ApplicationDTO{Country: c.country, PostalCode: c.postalCode})
			assert.Equal(t, c.want, app.Profile.Address.PostalCode)
		})
	}
}

// ramp.Profile.Phone promises E.164 whatever the client typed, and this is
// the single-provider and fan-out paths' shared conversion point, so a phone
// that reaches any adapter unconverted is this function's fault.
//
// The bare-digits case is not hypothetical: KycApiRepository.kt posts
// form.phone.filter(Char::isDigit), because the "+55" the user sees next to
// the field is UI and never reaches the wire.
func TestApplicationFrom_CanonicalisesPhone(t *testing.T) {
	cases := []struct {
		name    string
		phone   string
		country string
		want    string
	}{
		{"bare digits, as the android app sends them", "11987654321", "BRA", "+5511987654321"},
		{"human separators", "(11) 98765-4321", "BRA", "+5511987654321"},
		{"already E.164", "+5511987654321", "BRA", "+5511987654321"},
		{"empty stays empty", "", "BRA", ""},
		// The rule refuses this at the boundary; if it ever reaches here the
		// raw value is kept rather than prefixed with a guess, and the
		// adapter refuses it locally.
		{"unconvertible is left alone", "1198", "BRA", "1198"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := applicationFrom(dto.ApplicationDTO{Phone: c.phone, Country: c.country})
			assert.Equal(t, c.want, app.Profile.Phone)
		})
	}
}

// ramp.Profile.MotherName was declared, mapped by the Ozav adapter and given a
// wire tag, and never written: nothing set it. Ozav's OpenAPI marks it
// required for Brazilian onboarding, so every application reached that
// provider with an empty value and came back asking for it.
func TestApplicationFrom_CarriesMotherName(t *testing.T) {
	app := applicationFrom(dto.ApplicationDTO{
		FirstName:  "Joao",
		LastName:   "Silva",
		MotherName: "  Maria Silva  ",
		Email:      "joao@example.com",
		Country:    "BRA",
	})

	assert.Equal(t, "Maria Silva", app.Profile.MotherName)
}

func TestApplicationFrom_CarriesTheRiskProfile(t *testing.T) {
	pep := false
	app := applicationFrom(dto.ApplicationDTO{
		Nationality:               " bra ",
		Occupation:                "  Software developer ",
		SourceOfFunds:             "employment_salary",
		AccountPurpose:            "receive_payments",
		MonthlyIncomeBand:         "0_1000",
		ExpectedMonthlyVolumeBand: "0_5000",
		IsPEP:                     &pep,
	})

	assert.Equal(t, "BRA", app.Profile.Nationality)
	assert.Equal(t, "Software developer", app.Profile.Occupation)
	assert.Equal(t, "employment_salary", app.Profile.SourceOfFunds)
	assert.Equal(t, "receive_payments", app.Profile.AccountPurpose)
	if assert.NotNil(t, app.Profile.MonthlyIncomeUSD) {
		assert.Equal(t, 500.0, *app.Profile.MonthlyIncomeUSD)
	}
	if assert.NotNil(t, app.Profile.ExpectedMonthlyVolumeUSD) {
		assert.Equal(t, 5000.0, *app.Profile.ExpectedMonthlyVolumeUSD)
	}
	if assert.NotNil(t, app.Profile.IsPEP) {
		assert.False(t, *app.Profile.IsPEP)
	}
}

// A build that predates the fields sends none of them, and the application
// must say nothing about them — nil, not zero.
func TestApplicationFrom_LeavesTheRiskProfileNilWhenAbsent(t *testing.T) {
	app := applicationFrom(dto.ApplicationDTO{})
	assert.Nil(t, app.Profile.MonthlyIncomeUSD)
	assert.Nil(t, app.Profile.ExpectedMonthlyVolumeUSD)
	assert.Nil(t, app.Profile.IsPEP)
	assert.Empty(t, app.Profile.Nationality)
}

// The tax id travels as an identifying document: every adapter already knows
// how to place that list. It is typed by the identity the application
// declares (tax_id_type, tax_id_country), derived for builds that send
// neither: an installed build, which always sends BRA, still sends a CPF. The
// issuing country is alpha-3 in capitals, as every country in the contract
// is; the Bridge adapter spells it the way Bridge has always received it.
func TestApplicationFrom_TurnsTheTaxIDIntoTheDeclaredDocument(t *testing.T) {
	cases := []struct {
		name  string
		input dto.ApplicationDTO
		want  ramp.IdentifyingDocument
	}{
		{"an installed build", dto.ApplicationDTO{TaxID: "39053344705", Country: "BRA"},
			ramp.IdentifyingDocument{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"}},
		{"a cpf spelled out", dto.ApplicationDTO{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA", Country: "BRA"},
			ramp.IdentifyingDocument{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"}},
		{"a cpf living abroad", dto.ApplicationDTO{TaxID: "39053344705", TaxIDType: "cpf", Country: "PRT"},
			ramp.IdentifyingDocument{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"}},
		{"a passport", dto.ApplicationDTO{TaxID: " ab123456 ", TaxIDType: "passport", TaxIDCountry: "ARG", Country: "PRT"},
			ramp.IdentifyingDocument{Type: "passport", IssuingCountry: "ARG", Number: "AB123456"}},
		{"a passport issued where the person lives", dto.ApplicationDTO{TaxID: "AB123456", TaxIDType: "passport", Country: "PRT"},
			ramp.IdentifyingDocument{Type: "passport", IssuingCountry: "PRT", Number: "AB123456"}},
		{"an ssn, by its digits and issued by the united states", dto.ApplicationDTO{TaxID: "287-65-4321", TaxIDType: "ssn", Country: "USA"},
			ramp.IdentifyingDocument{Type: "ssn", IssuingCountry: "USA", Number: "287654321"}},
		{"an itin typed with spaces", dto.ApplicationDTO{TaxID: " 912 78 1234 ", TaxIDType: "itin", TaxIDCountry: "USA", Country: "USA"},
			ramp.IdentifyingDocument{Type: "itin", IssuingCountry: "USA", Number: "912781234"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			app := applicationFrom(c.input)
			require.Len(t, app.IdentifyingInformation, 1)
			assert.Equal(t, c.want, app.IdentifyingInformation[0])
		})
	}
}

func TestApplicationFrom_SendsNoDocumentWithoutATaxID(t *testing.T) {
	app := applicationFrom(dto.ApplicationDTO{Country: "BRA"})
	assert.Empty(t, app.IdentifyingInformation)
}

func TestApplicationFrom_CopiesTheEnvelope(t *testing.T) {
	app := applicationFrom(dto.ApplicationDTO{
		SignedAgreementID: "sa_1",
		Endorsements:      []string{"pix", "base"},
	})

	assert.Equal(t, "sa_1", app.SignedAgreementID)
	assert.Equal(t, []string{"pix", "base"}, app.Endorsements)
}

func TestDocumentsFrom_MapsTheImages(t *testing.T) {
	docs, err := documentsFrom(dto.DocumentsDTO{
		DocumentType:   "passport",
		IssuingCountry: "BRA",
		ImageFront:     "data:image/png;base64,front",
		ImageBack:      "data:image/png;base64,back",
		Selfie:         "data:image/png;base64,selfie",
		ProofOfAddress: "data:image/png;base64,proof",
	})
	require.NoError(t, err)

	assert.Equal(t, ramp.Documents{
		IdentifyingInformation: []ramp.IdentifyingDocument{{
			Type:           "passport",
			IssuingCountry: "BRA",
			ImageFront:     "data:image/png;base64,front",
			ImageBack:      "data:image/png;base64,back",
		}},
		LivenessCheckSelfies: []ramp.Selfie{{Image: "data:image/png;base64,selfie"}},
		ProofOfAddress:       "data:image/png;base64,proof",
	}, docs)
}

// The issuing country reaches every adapter as alpha-3 in capitals, whatever
// case the client sent: installed builds send "bra", and the tax id's own
// document is already spelled that way.
func TestDocumentsFrom_NormalisesTheIssuingCountry(t *testing.T) {
	for sent, want := range map[string]string{"bra": "BRA", " prt ": "PRT", "ARG": "ARG", "": ""} {
		t.Run(sent, func(t *testing.T) {
			docs, err := documentsFrom(dto.DocumentsDTO{DocumentType: "passport", IssuingCountry: sent,
				ImageFront: "data:image/png;base64,front"})
			require.NoError(t, err)
			assert.Equal(t, want, docs.IdentifyingInformation[0].IssuingCountry)
		})
	}
}

func TestDocumentsFrom_CarriesTheProofOfAddress(t *testing.T) {
	docs, err := documentsFrom(dto.DocumentsDTO{ProofOfAddress: "data:image/jpeg;base64,AAAA"})
	require.NoError(t, err)
	assert.Equal(t, "data:image/jpeg;base64,AAAA", docs.ProofOfAddress)
	assert.Len(t, docs.IdentifyingInformation, 1, "the identity entry is still built; the adapter skips one with no images")
	assert.Nil(t, docs.LivenessCheckSelfies)
}

// A bank statement is a PDF more often than a photo, and it travels as the
// client sent it: the adapter that takes it reads the mime off the data URI.
func TestDocumentsFrom_CarriesTheBankStatement(t *testing.T) {
	docs, err := documentsFrom(dto.DocumentsDTO{BankStatement: "data:application/pdf;base64,JVBERi0="})
	require.NoError(t, err)
	assert.Equal(t, "data:application/pdf;base64,JVBERi0=", docs.BankStatement)
	assert.Nil(t, docs.LivenessCheckSelfies)
}

// At least one file per call, and a selfie counts as one on its own: a
// provider that rejected only the liveness image asks for that image alone,
// and the retry that answers it carries no other file.
func TestDocumentsFrom_RequiresAtLeastOneFile(t *testing.T) {
	cases := []struct {
		name  string
		input dto.DocumentsDTO
		want  error
	}{
		{"nothing at all", dto.DocumentsDTO{}, ErrDocumentsRequired},
		{"a document type describing no file", dto.DocumentsDTO{DocumentType: "passport"}, ErrDocumentsRequired},
		{"an identity image", dto.DocumentsDTO{DocumentType: "passport", ImageFront: "data:image/png;base64,AAAA"}, nil},
		{"a proof of address alone", dto.DocumentsDTO{ProofOfAddress: "data:image/png;base64,AAAA"}, nil},
		{"a selfie alone", dto.DocumentsDTO{Selfie: "data:image/png;base64,AAAA"}, nil},
		// The retry Ozav's compliance asks for owes the statement and nothing else.
		{"a bank statement alone", dto.DocumentsDTO{BankStatement: "data:application/pdf;base64,AAAA"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := documentsFrom(c.input)
			if c.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, c.want)
		})
	}
}

// document_type is not required on its own — a proof-of-address-only retry,
// or one that owes only a selfie, has no identity document for it to name —
// but an image_front it would describe still needs one.
func TestDocumentsFrom_RequiresADocumentTypeForAnIdentityImage(t *testing.T) {
	cases := []struct {
		name  string
		input dto.DocumentsDTO
		want  error
	}{
		{"an identity image with no type", dto.DocumentsDTO{ImageFront: "data:image/png;base64,AAAA"}, ErrDocumentTypeRequired},
		{"an identity image with its type", dto.DocumentsDTO{ImageFront: "data:image/png;base64,AAAA", DocumentType: "passport"}, nil},
		{"no identity image and no type", dto.DocumentsDTO{ProofOfAddress: "data:image/png;base64,AAAA"}, nil},
		{"no identity image and a type", dto.DocumentsDTO{Selfie: "data:image/png;base64,AAAA", DocumentType: "passport"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := documentsFrom(c.input)
			if c.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, c.want)
		})
	}
}

// An SSN or an ITIN is held by its nine digits — in the application, the
// retained profile and the uniqueness digest alike — so the same number typed
// two ways is one person. Any other tax id is only trimmed here.
func TestCanonicalTaxID(t *testing.T) {
	ssn := models.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "USA"}
	itin := models.TaxIdentity{Type: models.TaxIDTypeITIN, Country: "USA"}
	passport := models.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}
	// The digits themselves are ustaxid.Canonical's (TestCanonical); this is
	// the dispatch by type.
	assert.Equal(t, "287654321", canonicalTaxID("287-65-4321", ssn))
	assert.Equal(t, "912781234", canonicalTaxID("912-78-1234", itin))
	assert.Equal(t, "AB-123", canonicalTaxID(" AB-123 ", passport))
}

// The controller hands over the address as the request saw it; the documents
// a provider records carry it canonical.
func TestDocumentsFrom_CanonicalisesTheClientAddress(t *testing.T) {
	for raw, want := range map[string]string{"::ffff:203.0.113.7": "203.0.113.7", "not-an-ip": ""} {
		docs, err := documentsFrom(dto.DocumentsDTO{ProofOfAddress: "data:image/jpeg;base64,AAAA", ClientIPAddress: raw})
		require.NoError(t, err, raw)
		require.Equal(t, want, docs.ClientIPAddress, raw)
	}
}
