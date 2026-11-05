package ramp

import "slices"

// PixByDeclaredResidence is a provider that serves Pix only to some people,
// and serves rails other than Pix to anyone (BlindPay: bank payouts to anyone,
// Pix to Brazilian residents). It joins the Pix wizard's fan-outs and
// listings only once the person has declared who they are and where they
// live, and that declaration opens it a Pix rail. The routes that name it
// stay open to anyone.
type PixByDeclaredResidence interface {
	PixNeedsDeclaredResidence() bool
}

// NeedsDeclaredResidenceForPix reports whether adapter declares it.
func NeedsDeclaredResidenceForPix(adapter ProviderAdapter) bool {
	gate, ok := adapter.(PixByDeclaredResidence)
	return ok && gate.PixNeedsDeclaredResidence()
}

// OpensPix reports whether adapter opens a Pix rail to p. A nil person — no
// declaration yet — opens nothing here, unlike OpensAny.
func (p *Person) OpensPix(adapter ProviderAdapter) bool {
	if p == nil {
		return false
	}
	return slices.ContainsFunc(RailsOf(adapter, p.Identity, p.Residence), func(r Rail) bool {
		return slices.Contains(PixRails(), r)
	})
}
