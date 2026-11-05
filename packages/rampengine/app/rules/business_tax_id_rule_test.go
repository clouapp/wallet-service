package rules

import (
	"context"
	"testing"
)

// business_tax_id is kyc_tax_id for a company (spec §7.2): the type has to
// fit the country of incorporation, and the number the shape of its type.
func TestBusinessTaxIdRule_Passes(t *testing.T) {
	r := &BusinessTaxIdRule{}
	ctx := context.Background()
	data := func(typ, country string) mapData {
		return mapData{"tax_id_type": typ, "incorporation_country": country}
	}

	tests := []struct {
		name  string
		data  mapData
		taxID string
		want  bool
	}{
		{"cnpj, numeric", data("cnpj", "BRA"), "11222333000181", true},
		{"cnpj, punctuated", data("cnpj", "BRA"), "11.222.333/0001-81", true},
		{"cnpj, alphanumeric (Serpro)", data("cnpj", "BRA"), "12.ABC.345/01DE-35", true},
		{"cnpj, alphanumeric in lower case", data("cnpj", "br"), "12abc34501de35", true},
		{"cnpj, check digits fail", data("cnpj", "BRA"), "12ABC34501DE36", false},
		{"cnpj, stray characters", data("cnpj", "BRA"), "12ABC34501DE35!", false},
		{"cnpj, a cpf", data("cnpj", "BRA"), "39053344705", false},
		{"cnpj abroad", data("cnpj", "PRT"), "11222333000181", false},
		{"ein, dashed", data("ein", "USA"), "12-3456789", true},
		{"ein, bare", data("ein", "USA"), "123456789", true},
		{"ein, eight digits", data("ein", "USA"), "12345678", false},
		{"ein, a letter", data("ein", "USA"), "12-345678A", false},
		{"ein outside the united states", data("ein", "GBR"), "123456789", false},
		{"crn", data("crn", "GBR"), "12345678", true},
		{"crn, scottish", data("crn", "GBR"), "SC123456", true},
		{"crn, seven characters", data("crn", "GBR"), "1234567", false},
		{"crn, nine characters", data("crn", "GBR"), "123456789", false},
		{"rfc", data("rfc", "MEX"), "ABC120101A12", true},
		{"rfc with an ampersand and an eñe", data("rfc", "MEX"), "A&Ñ120101A12", true},
		{"rfc, a person's thirteen", data("rfc", "MEX"), "ABCD120101A12", false},
		{"rfc, no date", data("rfc", "MEX"), "ABCXXXXXXA12", false},
		{"vat in the united kingdom", data("vat", "GBR"), "GB123456789", true},
		{"vat in argentina", data("vat", "ARG"), "30712345678", true},
		{"other in argentina", data("other", "ARG"), "AB12345", true},
		{"other, too short", data("other", "ARG"), "A1", false},
		{"other, separators", data("other", "ARG"), "AB-12345", false},
		{"vat in brazil: a cnpj there", data("vat", "BRA"), "GB123456789", false},
		{"a person's type", data("cpf", "BRA"), "39053344705", false},
		{"no type", data("", "BRA"), "11222333000181", false},
		{"no country", data("cnpj", ""), "11222333000181", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Passes(ctx, tt.data, tt.taxID); got != tt.want {
				t.Errorf("Passes(%v, %q) = %v, want %v", tt.data, tt.taxID, got, tt.want)
			}
		})
	}

	t.Run("not a string", func(t *testing.T) {
		if r.Passes(ctx, data("ein", "USA"), 123456789) {
			t.Error("expected a number to be refused")
		}
	})
	if r.Signature() != "business_tax_id" {
		t.Errorf("signature = %q", r.Signature())
	}
}
