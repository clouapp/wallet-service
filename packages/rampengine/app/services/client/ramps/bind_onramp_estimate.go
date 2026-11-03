package ramps

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
)

// bindOnrampCreate refuses a create whose body diverges from the quote the
// user was shown.
//
// It binds fewer fields than the off-ramp's counterpart, and the difference is
// the direction rather than an oversight: an on-ramp has no PIX key, no
// recipient and no source wallet — the money leaves the user's bank and lands
// at an address they already own. What is left to bind is the amount they pay
// and the crypto leg they are paying for.
func bindOnrampCreate(in dto.CreateOnrampDTO, snap *estimateSnapshot) error {
	// The wallet is the one that matters most: a create that changed it would
	// deliver the crypto somewhere the user never quoted.
	if !amountsEqual(in.AmountBRL, snap.SourceAmount) || in.Token != snap.DestCurrency ||
		in.Chain != snap.DestPaymentRail || in.Wallet != snap.DestAddress {
		return ramp.ErrEstimateMismatch
	}
	return nil
}
