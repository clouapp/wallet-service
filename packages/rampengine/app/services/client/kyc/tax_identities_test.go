package kyc

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Internal: countries_test.go is package kyc_test, and it cannot become
// package kyc because tests/mocks imports this package.
func TestTheTaxIdentitiesAResidentMayDeclare(t *testing.T) {
	assert.Equal(t, []models.TaxIdentity{{Type: "cpf", Country: "BRA"}}, taxIdentitiesFor("BRA"))
	assert.Equal(t, []models.TaxIdentity{{Type: "cpf", Country: "BRA"}}, taxIdentitiesFor(""))
	assert.Equal(t, []models.TaxIdentity{{Type: "ssn", Country: "USA"}, {Type: "itin", Country: "USA"}},
		taxIdentitiesFor("USA"))
	assert.Equal(t, taxIdentitiesFor("USA"), taxIdentitiesFor("us"))
	assert.Equal(t, []models.TaxIdentity{{Type: "cpf", Country: "BRA"}, {Type: "passport", Country: "PRT"}},
		taxIdentitiesFor("pt"))
}
