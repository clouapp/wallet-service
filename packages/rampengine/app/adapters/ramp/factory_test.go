package ramp

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// realAdapter stands in for an implemented provider: it does not embed
// NotImplemented, so it carries no IsStub marker.
type realAdapter struct {
	fakeAdapter
	code ProviderCode
}

func (a realAdapter) Code() ProviderCode { return a.code }

// stubbedAdapter is shaped exactly like the shipped stubs.
type stubbedAdapter struct {
	NotImplemented
	code ProviderCode
}

func (a stubbedAdapter) Code() ProviderCode         { return a.code }
func (stubbedAdapter) PreferredKycMode() KycMode    { return KycModeHostedVerification }
func (stubbedAdapter) RequiresTos() bool            { return false }
func (stubbedAdapter) Supports(cap Capability) bool { return cap.Rail == RailPixPayin }

func TestFactoryGetReturnsRegisteredAdapter(t *testing.T) {
	bridge := realAdapter{code: ProviderBridge}
	factory := NewFactory(bridge)

	got, err := factory.Get(ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, ProviderBridge, got.Code())
}

func TestFactoryGetUnknownCode(t *testing.T) {
	factory := NewFactory()

	_, err := factory.Get(ProviderBridge)
	require.ErrorIs(t, err, ErrProviderNotImplemented)
}

// A stub is a valid adapter: reaching it directly must produce the documented
// provider_not_implemented rather than a nil dereference.
func TestFactoryGetStubReturnsAdapterThatRefuses(t *testing.T) {
	factory := NewFactory(stubbedAdapter{code: ProviderOzav})

	adapter, err := factory.Get(ProviderOzav)
	require.NoError(t, err)

	_, err = adapter.EstimateOnramp(context.Background(), CustomerRef{}, OnrampQuoteRequest{})
	require.ErrorIs(t, err, ErrProviderNotImplemented)
}

func TestFactoryIsImplemented(t *testing.T) {
	factory := NewFactory(
		realAdapter{code: ProviderBridge},
		stubbedAdapter{code: ProviderOzav},
	)

	require.True(t, factory.IsImplemented(ProviderBridge))
	require.False(t, factory.IsImplemented(ProviderOzav), "a stub must never be routable")
	require.False(t, factory.IsImplemented(ProviderMutualPay), "an unregistered code is not implemented")
}

func TestFactoryIgnoresNilAdapters(t *testing.T) {
	factory := NewFactory(nil, realAdapter{code: ProviderBridge})

	require.True(t, factory.IsImplemented(ProviderBridge))
}

// Replace exists for feature tests that need to wrap a registered adapter in
// a decorator (a spy that records the ramp.Application a real adapter's own
// wire format cannot carry back out again) — it is never called from
// production code, which fixes its adapters at boot.
func TestFactoryReplace(t *testing.T) {
	factory := NewFactory(realAdapter{code: ProviderBridge})

	factory.Replace(ProviderBridge, realAdapter{code: ProviderOzav})

	got, err := factory.Get(ProviderBridge)
	require.NoError(t, err)
	require.Equal(t, ProviderOzav, got.Code(),
		"Replace overwrites the registration under the given code, not the adapter's own")
}

func TestValidateMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		appEnv  string
		wantErr bool
	}{
		{"sandbox in local", models.ProviderModeSandbox, "local", false},
		{"sandbox in testing", models.ProviderModeSandbox, "testing", false},
		{"production in production", models.ProviderModeProduction, "production", false},
		// Real money on a developer's machine.
		{"production outside production", models.ProviderModeProduction, "local", true},
		// Real users handed orders that will never settle.
		{"sandbox inside production", models.ProviderModeSandbox, "production", true},
		{"unknown mode", "staging", "local", true},
		{"empty mode", "", "local", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := ValidateMode(c.mode, c.appEnv)
			if c.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
		})
	}
}

// The shipped stubs must satisfy the contract and refuse everything, including
// the paths the router would otherwise reach.
func TestNotImplementedRefusesEveryOperation(t *testing.T) {
	adapter := stubbedAdapter{code: ProviderMutualPay}
	ctx := context.Background()
	ref := CustomerRef{CustomerID: 1}

	calls := []func() error{
		func() error { _, err := adapter.CreateTosSession(ctx, ref, TosOpts{}); return err },
		func() error { _, err := adapter.SubmitApplication(ctx, ref, Application{}); return err },
		func() error { _, err := adapter.SubmitDocuments(ctx, ref, Documents{}); return err },
		func() error { _, err := adapter.GetKyc(ctx, ref); return err },
		func() error { _, err := adapter.RefreshKyc(ctx, ref); return err },
		func() error { _, err := adapter.GetRequirements(ctx, ref); return err },
		func() error { _, err := adapter.RequestEndorsements(ctx, ref, nil); return err },
		func() error { _, err := adapter.CreateHostedKycLink(ctx, ref, HostedLinkOpts{}); return err },
		func() error { _, err := adapter.EstimateOnramp(ctx, ref, OnrampQuoteRequest{}); return err },
		func() error { _, err := adapter.CreateOnramp(ctx, ref, OnrampCreateRequest{}); return err },
		func() error { _, err := adapter.EstimateOfframp(ctx, ref, OfframpQuoteRequest{}); return err },
		func() error { _, err := adapter.CreateOfframp(ctx, ref, OfframpCreateRequest{}); return err },
		func() error { _, err := adapter.GetOrder(ctx, CustomerRef{}, ""); return err },
		func() error { _, err := adapter.ParseWebhook(ctx, nil, nil); return err },
		func() error { _, err := adapter.DecodeWebhook(ctx, nil); return err },
		func() error { _, err := adapter.StartIntegration(ctx, ref); return err },
		func() error { _, err := adapter.RefreshIntegration(ctx, ref); return err },
	}
	for i, call := range calls {
		if err := call(); !errors.Is(err, ErrProviderNotImplemented) {
			t.Fatalf("call %d returned %v, want ErrProviderNotImplemented", i, err)
		}
	}
}
