package ozav

import (
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// Adapter is the Ozav provider adapter.
type Adapter struct {
	clients        ClientFactory
	webhookSecrets SecretFactory
}

// New returns the Ozav adapter. Both factories are resolved per call, so an
// admin rotating the key, the mode or the webhook secret needs no restart.
func New(clients ClientFactory, webhookSecrets SecretFactory) *Adapter {
	return &Adapter{clients: clients, webhookSecrets: webhookSecrets}
}

// Code identifies the provider.
func (a *Adapter) Code() ramp.ProviderCode { return ramp.ProviderOzav }

// PreferredKycMode is api_docs: XIP uploads the images from its own form.
// Ozav's docs disagree on whether uploads advance an individual (the prose
// says a hosted session, the endpoint accepts identity_document and selfie)
// and the sandbox cannot tell; if production says hosted, this is the one line
// to change.
func (a *Adapter) PreferredKycMode() ramp.KycMode { return ramp.KycModeAPIDocs }

// RequiresTos is true: Ozav fails customer provisioning closed without an
// accepted terms session (422 TERMS_ACCEPTANCE_REQUIRED), even though the
// OpenAPI prose still says no endpoint exists to open one.
func (a *Adapter) RequiresTos() bool { return true }

// Supports declares both halves of Pix, each served by a working create: the
// router prices a rail with whoever declares it, and the create is sticky to
// that provider. The token and chain must be ones this code quotes and
// settles; an unnamed one means the caller did not ask.
func (a *Adapter) Supports(c ramp.Capability) bool {
	if c.Rail != ramp.RailPixPayout && c.Rail != ramp.RailPixPayin {
		return false
	}
	if c.Token != "" && !quotableTokens[strings.ToUpper(strings.TrimSpace(c.Token))] {
		return false
	}
	return c.Chain == "" || movableChains[normalizeVocabulary(c.Chain)]
}

// quotableTokens (keyed by the uppercase token) and movableChains are what
// Ozav prices and moves for XIP.
var (
	quotableTokens = map[string]bool{
		models.TargetTokenUSDC: true,
		models.TargetTokenUSDT: true,
		models.TargetTokenBRS:  true,
	}
	movableChains = map[string]bool{models.ChainSolana: true}
)

// normalizeVocabulary lowercases a chain, a currency or a key type for a local
// comparison; what travels upstream keeps the client's case (an address is
// case-sensitive).
func normalizeVocabulary(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

var (
	_ ramp.ProviderAdapter              = (*Adapter)(nil)
	_ ramp.ApplicationDocumentsRequirer = (*Adapter)(nil)
)

// Decimal places by leg (a stablecoin's six, and BRL), and of a rate.
const (
	cryptoScale = 6
	fiatScale   = 2
	rateScale   = 6
)

// pixRailName is Ozav's name for the rail on whichever leg is fiat.
const pixRailName = "pix"
