package rules

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A rule that reads the fields beside the one it validates reads them at the
// top of the request, or under the scope it is given for a field nested in an
// object or a list entry: a KYB application's partners each declare their own
// residence and identity (API contract §5.2).
func TestSiblingScope(t *testing.T) {
	data := mapData{"country": "BRA", "associated_persons.1.country": "USA"}

	assert.Equal(t, "BRA", sibling(data, nil, "country"))
	assert.Equal(t, "USA", sibling(data, []any{"associated_persons.1"}, "country"))
	assert.Equal(t, "", sibling(data, []any{"associated_persons.0"}, "country"), "nothing declared there")
}

// kyc_tax_id scoped to a partner reads that partner's identity and residence:
// an SSN of a US resident, though the request's own country is Brazil.
func TestKycTaxIdRuleReadsTheScopedPartner(t *testing.T) {
	r := &KycTaxIdRule{}
	data := mapData{
		"country":                             "BRA",
		"associated_persons.1.country":        "USA",
		"associated_persons.1.tax_id_type":    "ssn",
		"associated_persons.1.tax_id_country": "USA",
	}

	assert.True(t, r.Passes(context.Background(), data, "287-65-4321", "associated_persons.1"))
	assert.False(t, r.Passes(context.Background(), data, "287-65-4321"), "unscoped, it is the request's own CPF")
}

// The address rules read the residence the same way.
func TestAddressRulesReadTheScopedCountry(t *testing.T) {
	ctx := context.Background()
	data := mapData{"registered_address.country": "USA"}

	assert.True(t, (&BridgePostalCodeRule{}).Passes(ctx, data, "33131", "registered_address"))
	assert.False(t, (&BridgePostalCodeRule{}).Passes(ctx, data, "33131"), "unscoped, a Brazilian CEP is due")
	assert.True(t, (&BridgeSubdivisionRule{}).Passes(ctx, data, "FL", "registered_address"))
	assert.False(t, (&KycNotPoBoxRule{}).Passes(ctx, data, "PO Box 123", "registered_address"))
	assert.True(t, (&KycPhoneRule{}).Passes(ctx, mapData{"registered_address.country": "BRA"}, "11987654321", "registered_address"))
}
