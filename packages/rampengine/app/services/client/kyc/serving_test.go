package kyc

import (
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// pixForAnyone is a provider with no jurisdiction policy that supports every
// Pix rail, the way Bridge reads to RailsOf.
type pixForAnyone struct{ ramp.ProviderAdapter }

func (pixForAnyone) Supports(c ramp.Capability) bool { return slices.Contains(ramp.PixRails(), c.Rail) }

// pixForBrazilians serves bank rails to anyone and Pix only to a CPF holder
// living in Brazil, and declares that its Pix needs a declared residence, the
// way BlindPay does.
type pixForBrazilians struct{ ramp.ProviderAdapter }

func (pixForBrazilians) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	if identity.Type == models.TaxIDTypeCPF && residence == "BRA" {
		return append(ramp.BankRails(), ramp.PixRails()...)
	}
	return ramp.BankRails()
}
func (pixForBrazilians) PixNeedsDeclaredResidence() bool { return true }
func (pixForBrazilians) Code() ramp.ProviderCode         { return models.ProviderBlindPay }

var (
	bridgeLike ramp.ProviderAdapter = pixForAnyone{}
	gated      ramp.ProviderAdapter = pixForBrazilians{}
)

// listedOnPix is a pixListed naming codes on some Pix rail's provider_order.
func listedOnPix(codes ...ramp.ProviderCode) func(ramp.ProviderCode) (bool, error) {
	return func(code ramp.ProviderCode) (bool, error) { return slices.Contains(codes, code), nil }
}

// neverAsked fails the test if serving reads the Pix rails' order: nobody in
// the call is gated on a declared residence that opens them Pix.
func neverAsked(t *testing.T) func(ramp.ProviderCode) (bool, error) {
	return func(ramp.ProviderCode) (bool, error) {
		t.Fatal("the Pix rails' provider_order was read")
		return false, nil
	}
}

var brazilianResident = &ramp.Person{Identity: models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "BRA"}

func servingOf(t *testing.T, adapters []ramp.ProviderAdapter, who *ramp.Person,
	pixListed func(ramp.ProviderCode) (bool, error)) []ramp.ProviderAdapter {
	t.Helper()
	got, err := serving(adapters, who, pixListed)
	require.NoError(t, err)
	return got
}

func TestServingKeepsAResidenceGatedProviderOutWithoutAPerson(t *testing.T) {
	got := servingOf(t, []ramp.ProviderAdapter{bridgeLike, gated}, nil, neverAsked(t))
	assert.Equal(t, []ramp.ProviderAdapter{bridgeLike}, got)
}

func TestServingLetsAResidenceGatedProviderInForABrazilianResident(t *testing.T) {
	got := servingOf(t, []ramp.ProviderAdapter{bridgeLike, gated}, brazilianResident, listedOnPix(models.ProviderBlindPay))
	assert.Equal(t, []ramp.ProviderAdapter{bridgeLike, gated}, got)
}

// Enabled is not enough: a provider no Pix rail routes to (BlindPay switched
// on for its bank rails alone) stays out of the Pix wizard.
func TestServingKeepsAResidenceGatedProviderOutWhenNoPixRailRoutesToIt(t *testing.T) {
	got := servingOf(t, []ramp.ProviderAdapter{bridgeLike, gated}, brazilianResident, listedOnPix(models.ProviderBridge))
	assert.Equal(t, []ramp.ProviderAdapter{bridgeLike}, got)
}

func TestServingFailsWhenThePixRailsCannotBeRead(t *testing.T) {
	boom := errors.New("settings down")
	_, err := serving([]ramp.ProviderAdapter{gated}, brazilianResident,
		func(ramp.ProviderCode) (bool, error) { return false, boom })
	require.ErrorIs(t, err, boom)
}

func TestServingKeepsItOutForAUSResidentWhoseOnlyRailsAreBank(t *testing.T) {
	who := &ramp.Person{Identity: models.TaxIdentity{Type: models.TaxIDTypeSSN, Country: "USA"}, Residence: "USA"}
	assert.Empty(t, servingOf(t, []ramp.ProviderAdapter{gated}, who, neverAsked(t)))
}

func TestServingKeepsItOutForABrazilianLivingAbroad(t *testing.T) {
	who := &ramp.Person{Identity: models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, Residence: "PRT"}
	assert.Empty(t, servingOf(t, []ramp.ProviderAdapter{gated}, who, neverAsked(t)))
}
