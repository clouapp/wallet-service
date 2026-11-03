package ramp_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// gatedPix opens Pix to a CPF holder living in Brazil and bank rails to
// anyone, and declares that its Pix needs a declared residence.
type gatedPix struct {
	ramp.ProviderAdapter
	declares bool
}

func (gatedPix) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
		return append(ramp.BankRails(), ramp.PixRails()...)
	}
	return ramp.BankRails()
}
func (g gatedPix) PixNeedsDeclaredResidence() bool { return g.declares }

func TestNeedsDeclaredResidenceForPixReadsTheMarker(t *testing.T) {
	assert.True(t, ramp.NeedsDeclaredResidenceForPix(gatedPix{declares: true}))
	assert.False(t, ramp.NeedsDeclaredResidenceForPix(gatedPix{declares: false}))
	assert.False(t, ramp.NeedsDeclaredResidenceForPix(supportsOnly{rail: ramp.RailPixPayin, token: models.TargetTokenUSDC}))
}

func TestOpensPixIsWhetherThePersonsRailsIncludeAPixRail(t *testing.T) {
	brazilian := &ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "BRA"}
	abroad := &ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "PRT"}
	american := &ramp.Person{Identity: ramp.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "USA"}, Residence: "USA"}

	assert.True(t, brazilian.OpensPix(gatedPix{}))
	assert.False(t, abroad.OpensPix(gatedPix{}))
	assert.False(t, american.OpensPix(gatedPix{}))
}

func TestOpensPixIsFalseForNobodyInParticular(t *testing.T) {
	var nobody *ramp.Person
	assert.False(t, nobody.OpensPix(gatedPix{}))
	assert.True(t, nobody.OpensAny(gatedPix{}))
}
