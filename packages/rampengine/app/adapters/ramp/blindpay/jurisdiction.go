package blindpay

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// RailsFor is what BlindPay opens: its bank rails to anyone, and Pix to a
// resident of Brazil who declared a CPF.
func (a *Adapter) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	rails := ramp.BankRails()
	res, ok := iso3166.Alpha3(residence)
	if !ok || res != "BRA" || identity.Type != models.TaxIDTypeCPF {
		return rails
	}
	return append(rails, ramp.PixRails()...)
}

// PixNeedsDeclaredResidence marks BlindPay as joining the Pix wizard only for
// a person whose declared identity and residence open it a Pix rail (see
// ramp.PixByDeclaredResidence).
func (a *Adapter) PixNeedsDeclaredResidence() bool { return true }
