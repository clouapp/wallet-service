package ramp

import "context"

// NotImplemented is the base for a provider that exists in the contract but
// has no behaviour yet. Embedding it gives a stub the whole ProviderAdapter
// surface with every operation refusing, so the stub file only declares what
// is actually decided about that provider: its code, its preferred KYC mode
// and whether it runs a terms step.
//
// Only stubs embed this. A real adapter implements every method explicitly, so
// that forgetting one is a compile error rather than a silent refusal in
// production.
type NotImplemented struct{}

// IsStub marks the adapter as a placeholder. The factory and the router read it
// to skip the provider without making a call; a provider becomes real by
// dropping this embed, which removes the method.
func (NotImplemented) IsStub() bool { return true }

// Supports refuses every capability. A stub serves nothing: each of its
// operations returns ErrProviderNotImplemented, so a rail it declared would be
// a quote the user could never redeem.
func (NotImplemented) Supports(Capability) bool { return false }

func (NotImplemented) CreateTosSession(context.Context, CustomerRef, TosOpts) (*TosSession, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) SubmitApplication(context.Context, CustomerRef, Application) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) SubmitDocuments(context.Context, CustomerRef, Documents) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) GetKyc(context.Context, CustomerRef) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) RefreshKyc(context.Context, CustomerRef) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) GetRequirements(context.Context, CustomerRef) (*KycRequirements, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) RequestEndorsements(context.Context, CustomerRef, []string) (*KycState, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) CreateHostedKycLink(context.Context, CustomerRef, HostedLinkOpts) (*HostedKycLink, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) EstimateOnramp(context.Context, CustomerRef, OnrampQuoteRequest) (*Estimate, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) CreateOnramp(context.Context, CustomerRef, OnrampCreateRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) EstimateOfframp(context.Context, CustomerRef, OfframpQuoteRequest) (*Estimate, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) CreateOfframp(context.Context, CustomerRef, OfframpCreateRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) GetOrder(context.Context, CustomerRef, string) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) ParseWebhook(context.Context, map[string]string, []byte) (*WebhookResult, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) DecodeWebhook(context.Context, []byte) (*WebhookResult, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) StartIntegration(context.Context, CustomerRef) (*Integration, error) {
	return nil, ErrProviderNotImplemented
}

func (NotImplemented) RefreshIntegration(context.Context, CustomerRef) (*Integration, error) {
	return nil, ErrProviderNotImplemented
}
