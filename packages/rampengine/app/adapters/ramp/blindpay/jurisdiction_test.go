package blindpay_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

func TestRailsForIsTheBankRailsForAnyone(t *testing.T) {
	a := blindpay.New(nil, nil, nil)
	want := []ramp.Rail{ramp.Rail(models.RailACHPayout), ramp.Rail(models.RailWirePayout)}

	require.Equal(t, want, a.RailsFor(ramp.TaxIdentity{}, "BRA"))
	require.Equal(t, want, ramp.RailsOf(a, ramp.TaxIdentity{}, "USA"))
	require.Equal(t, ramp.BankRails(), a.RailsFor(ramp.TaxIdentity{}, ""))
}

func TestBlindPayNeedsADeclaredResidenceForPix(t *testing.T) {
	require.True(t, ramp.NeedsDeclaredResidenceForPix(blindpay.New(nil, nil, nil)))
}

func TestRailsForOpensPixToABrazilianResidentWithCPF(t *testing.T) {
	a := blindpay.New(nil, nil, nil)
	rails := a.RailsFor(models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, "BRA")
	require.ElementsMatch(t, append(ramp.BankRails(), ramp.PixRails()...), rails)
}

func TestRailsForKeepsPixClosedOtherwise(t *testing.T) {
	a := blindpay.New(nil, nil, nil)
	for _, c := range []struct{ typ, country, residence string }{
		{models.TaxIDTypeCPF, "BRA", "PRT"},
		{models.TaxIDTypePassport, "BRA", "BRA"},
		{models.TaxIDTypeSSN, "USA", "USA"},
		{models.TaxIDTypeCPF, "BRA", "xx"},
	} {
		rails := a.RailsFor(models.TaxIdentity{Type: c.typ, Country: c.country}, c.residence)
		require.ElementsMatch(t, ramp.BankRails(), rails, c)
	}
}

func TestRailsForReadsTheResidenceInAnySpelling(t *testing.T) {
	a := blindpay.New(nil, nil, nil)
	for _, residence := range []string{"BR", "bra"} {
		rails := a.RailsFor(models.TaxIdentity{Type: models.TaxIDTypeCPF, Country: "BRA"}, residence)
		require.ElementsMatch(t, append(ramp.BankRails(), ramp.PixRails()...), rails, residence)
	}
}
