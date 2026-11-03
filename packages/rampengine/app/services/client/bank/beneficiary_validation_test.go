package bank

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/bank"
)

func validInput() dto.BeneficiaryDTO {
	return dto.BeneficiaryDTO{
		Label: "Chase", AccountClass: "individual", AccountType: "checking", Relationship: "first_party",
		BeneficiaryName: "Ana Souza", RoutingNumber: "021000021", AccountNumber: "123456789",
		AddressLine1: "350 Fifth Avenue", City: "New York", StateCode: "NY", PostalCode: "10118",
	}
}

func fieldOf(t *testing.T, err error) *FieldError {
	t.Helper()
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrInvalidBeneficiary))
	var fe *FieldError
	require.True(t, errors.As(err, &fe))
	return fe
}

func TestValidateAcceptsAValidIndividualAndBusiness(t *testing.T) {
	require.NoError(t, Validate(Normalize(validInput())))
	b := validInput()
	b.AccountClass, b.BusinessIndustry, b.Relationship = "business", "541511", "vendor_or_supplier"
	require.NoError(t, Validate(Normalize(b)))
}

func TestABARoutingChecksumAndPrefix(t *testing.T) {
	for _, ok := range []string{"021000021", "011000015", "122105155"} {
		assert.True(t, validABARouting(ok), ok)
	}
	for _, bad := range []string{"123456789", "021000022", "000000000", "02100002", "0210000211", "a21000021", "991000013"} {
		assert.False(t, validABARouting(bad), bad)
	}
}

func TestValidateRefusesEachBadField(t *testing.T) {
	cases := []struct {
		name  string
		edit  func(*dto.BeneficiaryDTO)
		field string
	}{
		{"label missing", func(in *dto.BeneficiaryDTO) { in.Label = "" }, "label"},
		{"label too long", func(in *dto.BeneficiaryDTO) { in.Label = string(make([]byte, 65)) }, "label"},
		{"class", func(in *dto.BeneficiaryDTO) { in.AccountClass = "personal" }, "account_class"},
		{"type", func(in *dto.BeneficiaryDTO) { in.AccountType = "savings" }, "account_type"},
		{"relationship", func(in *dto.BeneficiaryDTO) { in.Relationship = "friend" }, "relationship"},
		{"name missing", func(in *dto.BeneficiaryDTO) { in.BeneficiaryName = " " }, "beneficiary_name"},
		{"routing checksum", func(in *dto.BeneficiaryDTO) { in.RoutingNumber = "123456789" }, "routing_number"},
		{"account short", func(in *dto.BeneficiaryDTO) { in.AccountNumber = "123" }, "account_number"},
		{"account long", func(in *dto.BeneficiaryDTO) { in.AccountNumber = "123456789012345678" }, "account_number"},
		{"account letters", func(in *dto.BeneficiaryDTO) { in.AccountNumber = "12345abc" }, "account_number"},
		{"address missing", func(in *dto.BeneficiaryDTO) { in.AddressLine1 = "" }, "address_line_1"},
		{"city missing", func(in *dto.BeneficiaryDTO) { in.City = "" }, "city"},
		{"state unknown", func(in *dto.BeneficiaryDTO) { in.StateCode = "XX" }, "state_code"},
		{"territory", func(in *dto.BeneficiaryDTO) { in.StateCode = "PR" }, "state_code"},
		{"zip", func(in *dto.BeneficiaryDTO) { in.PostalCode = "1011" }, "postal_code"},
		{"zip+4 bad", func(in *dto.BeneficiaryDTO) { in.PostalCode = "10118-12" }, "postal_code"},
		{"business without naics", func(in *dto.BeneficiaryDTO) { in.AccountClass = "business" }, "business_industry"},
		{"business unknown naics", func(in *dto.BeneficiaryDTO) { in.AccountClass, in.BusinessIndustry = "business", "999999" }, "business_industry"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := validInput()
			c.edit(&in)
			assert.Equal(t, c.field, fieldOf(t, Validate(Normalize(in))).Field)
		})
	}
}

func TestNormalizeStripsSeparatorsAndCase(t *testing.T) {
	in := validInput()
	in.RoutingNumber, in.AccountNumber = " 021 000-021 ", "1234-5678 9"
	in.StateCode, in.Label, in.BeneficiaryName = " ny ", "  Chase  ", "  Ana Souza "
	out := Normalize(in)
	assert.Equal(t, "021000021", out.RoutingNumber)
	assert.Equal(t, "123456789", out.AccountNumber)
	assert.Equal(t, "NY", out.StateCode)
	assert.Equal(t, "Chase", out.Label)
	assert.Equal(t, "Ana Souza", out.BeneficiaryName)
	require.NoError(t, Validate(out))
}

func TestNormalizeDropsBusinessFieldsOfAnIndividual(t *testing.T) {
	in := validInput()
	in.BusinessIndustry = "541511"
	assert.Empty(t, Normalize(in).BusinessIndustry)
}

func TestDCIsAState(t *testing.T) {
	in := validInput()
	in.StateCode = "DC"
	require.NoError(t, Validate(Normalize(in)))
}

func TestNormalizeStripsEveryUnicodeSpaceAndDash(t *testing.T) {
	for name, pasted := range map[string]string{
		"nbsp": "021\u00a0000\u00a0021", "hyphen": "021\u2010000\u2010021", "non-breaking hyphen": "021\u2011000\u2011021",
		"figure dash": "021\u2012000\u2012021", "en dash": "021\u2013000\u2013021", "em dash": "021\u2014000\u2014021",
		"tab and ideographic space": "021\t000\u3000021", "ascii": " 021-000 021 ",
	} {
		t.Run(name, func(t *testing.T) {
			in := validInput()
			in.RoutingNumber, in.AccountNumber = pasted, "1234\u00a0\u20135678\u20149"
			out := Normalize(in)
			assert.Equal(t, "021000021", out.RoutingNumber)
			assert.Equal(t, "123456789", out.AccountNumber)
			require.NoError(t, Validate(out))
		})
	}
}

func TestNormalizeKeepsDotsAndSlashesSoTheyAreRefused(t *testing.T) {
	for _, pasted := range []string{"1234.56789", "1234/56789"} {
		in := validInput()
		in.AccountNumber = pasted
		assert.Equal(t, "account_number", fieldOf(t, Validate(Normalize(in))).Field, pasted)
	}
}
