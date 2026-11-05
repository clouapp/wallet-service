package rules

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// The tax id is read against the identity the request declares: tax_id_type
// and tax_id_country, derived from the residence when a build predates them.
func TestKycTaxIdRule_Passes(t *testing.T) {
	r := &KycTaxIdRule{}
	ctx := context.Background()

	tests := []struct {
		name  string
		data  mapData
		taxID string
		want  bool
	}{
		{"bra + cpf, valid digits", mapData{"country": "BRA", "tax_id_type": "cpf"}, "390.533.447-05", true},
		{"bra + cpf, bare digits", mapData{"country": "BRA", "tax_id_type": "cpf"}, "39053344705", true},
		{"bra + cpf, check digits fail", mapData{"country": "BRA", "tax_id_type": "cpf"}, "39053344706", false},
		{"bra + cpf, one digit repeated", mapData{"country": "BRA", "tax_id_type": "cpf"}, "00000000000", false},
		{"bra + cpf, too short", mapData{"country": "BRA", "tax_id_type": "cpf"}, "123", false},
		{"bra + passport refused: a resident has a cpf", mapData{"country": "BRA", "tax_id_type": "passport", "tax_id_country": "ARG"}, "AB123456", false},
		// A resident of the United States declares an SSN or an ITIN, issued
		// by USA: Bridge refuses a CPF or a passport there (spec §8.1).
		{"usa, no type: a passport is no tax id there", mapData{"country": "USA"}, "AB123456", false},
		{"usa, no type: an ssn is no passport", mapData{"country": "USA"}, "287-65-4321", false},
		{"usa + passport", mapData{"country": "us", "tax_id_type": "passport", "tax_id_country": "ARG"}, "AB123456", false},
		{"usa + cpf (bra)", mapData{"country": "USA", "tax_id_type": "cpf"}, "39053344705", false},
		{"usa + ssn", mapData{"country": "USA", "tax_id_type": "ssn"}, "287-65-4321", true},
		{"usa + ssn, bare digits", mapData{"country": "USA", "tax_id_type": "ssn", "tax_id_country": "USA"}, "287654321", true},
		{"usa + ssn, alpha-2 residence", mapData{"country": "us", "tax_id_type": "ssn"}, "287-65-4321", true},
		{"usa + ssn of a fake pattern", mapData{"country": "USA", "tax_id_type": "ssn"}, "123-45-6789", false},
		{"usa + ssn issued by brazil", mapData{"country": "USA", "tax_id_type": "ssn", "tax_id_country": "BRA"}, "287-65-4321", false},
		{"usa + itin", mapData{"country": "USA", "tax_id_type": "itin"}, "912-78-1234", true},
		{"usa + itin outside the irs groups", mapData{"country": "USA", "tax_id_type": "itin"}, "912-12-1234", false},
		{"usa + an ssn declared as an itin", mapData{"country": "USA", "tax_id_type": "itin"}, "287-65-4321", false},
		{"prt + ssn: a us tax id is a us resident's", mapData{"country": "PRT", "tax_id_type": "ssn"}, "287-65-4321", false},
		{"bra + ssn", mapData{"country": "BRA", "tax_id_type": "ssn"}, "287-65-4321", false},
		{"prt + cpf (bra)", mapData{"country": "PRT", "tax_id_type": "cpf", "tax_id_country": "BRA"}, "39053344705", true},
		{"prt + cpf, country derived", mapData{"country": "PRT", "tax_id_type": "cpf"}, "39053344705", true},
		{"prt + cpf, check digits fail", mapData{"country": "PRT", "tax_id_type": "cpf"}, "39053344706", false},
		{"prt + cpf issued by arg refused", mapData{"country": "PRT", "tax_id_type": "cpf", "tax_id_country": "ARG"}, "39053344705", false},
		{"prt + passport (arg)", mapData{"country": "PRT", "tax_id_type": "passport", "tax_id_country": "ARG"}, "AB123456", true},
		{"prt + passport, trimmed", mapData{"country": "PRT", "tax_id_type": "passport", "tax_id_country": "ARG"}, "  AB123456 ", true},
		{"prt + passport too short", mapData{"country": "PRT", "tax_id_type": "passport", "tax_id_country": "ARG"}, "A1", false},
		{"prt + passport too long", mapData{"country": "PRT", "tax_id_type": "passport", "tax_id_country": "ARG"}, "A12345678901234567890", false},
		{"prt + passport with separators", mapData{"country": "PRT", "tax_id_type": "passport", "tax_id_country": "ARG"}, "AB-123456", false},
		{"prt, no type: a passport issued by the residence", mapData{"country": "PRT"}, "AB123456", true},
		{"prt, no type: a cpf shape is no passport", mapData{"country": "PRT"}, "390.533.447-05", false},
		{"old build: no type, bra, valid cpf", mapData{"country": "BRA"}, "39053344705", true},
		{"old build: no type, bra, invalid cpf", mapData{"country": "BRA"}, "39053344706", false},
		{"no country at all reads as brazil", mapData{}, "39053344705", true},
		{"unknown type", mapData{"country": "PRT", "tax_id_type": "tin"}, "123456789", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := r.Passes(ctx, tt.data, tt.taxID); got != tt.want {
				t.Errorf("Passes(%v, %q) = %v, want %v", tt.data, tt.taxID, got, tt.want)
			}
		})
	}

	t.Run("not a string", func(t *testing.T) {
		if r.Passes(ctx, mapData{"country": "BRA"}, 39053344705) {
			t.Error("expected a number to be refused")
		}
	})
}

func TestValidTaxID(t *testing.T) {
	tests := []struct {
		name     string
		taxID    string
		identity models.TaxIdentity
		want     bool
	}{
		{"cpf", "390.533.447-05", models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, true},
		{"cpf, bad digits", "390.533.447-06", models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, false},
		{"cpf not issued by brazil", "39053344705", models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "ARG"}, false},
		{"passport", "AB123456", models.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}, true},
		{"passport, lower case", "ab123456", models.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}, true},
		{"passport out of shape", "A1", models.TaxIdentity{Type: models.TaxIDTypePassport, Country: "ARG"}, false},
		{"ssn", "287-65-4321", models.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "USA"}, true},
		{"ssn out of shape", "287-65-432", models.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "USA"}, false},
		{"ssn not issued by the united states", "287654321", models.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "BRA"}, false},
		{"itin", "912 78 1234", models.TaxIdentity{Type: models.TaxIDTypeITIN, Country: "USA"}, true},
		{"itin outside the irs groups", "912-66-1234", models.TaxIdentity{Type: models.TaxIDTypeITIN, Country: "USA"}, false},
		{"unknown type", "123456789", models.TaxIdentity{Type: "tin", Country: "USA"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidTaxID(tt.taxID, tt.identity); got != tt.want {
				t.Errorf("ValidTaxID(%q, %v) = %v, want %v", tt.taxID, tt.identity, got, tt.want)
			}
		})
	}
}
