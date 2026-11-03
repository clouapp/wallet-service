// Package mutualpay is the MutualPay provider adapter. It is a stub: the
// contract is in place so routing, admin settings and the client API can treat
// MutualPay like any other provider, but every operation refuses until there
// is an API to map. The router skips it without calling it.
package mutualpay

import (
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// Adapter carries the full provider contract with no behaviour behind it.
type Adapter struct {
	ramp.NotImplemented
}

// New returns the MutualPay stub adapter.
func New() *Adapter { return &Adapter{} }

// Code identifies the provider.
func (Adapter) Code() ramp.ProviderCode { return ramp.ProviderMutualPay }

// PreferredKycMode is a placeholder: MutualPay has published no KYC flow yet,
// so this is a guess that costs nothing while the adapter refuses every call.
// It gets decided for real alongside the implementation.
func (Adapter) PreferredKycMode() ramp.KycMode { return ramp.KycModeAPIDocs }

// RequiresTos is false: MutualPay has published no KYC flow, so there is no
// terms step to run. It flips when one is published, not before.
func (Adapter) RequiresTos() bool { return false }

var _ ramp.ProviderAdapter = (*Adapter)(nil)
