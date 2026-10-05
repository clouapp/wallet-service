package wallets

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
)

func webhooksControllerDeps() WebhooksControllerDeps {
	return WebhooksControllerDeps{
		Configs:     &walletrecords.Webhooks{},
		Delivery:    &webhook.Service{},
		Memberships: &walletrecords.Memberships{},
	}
}

func TestNewWebhooksControllerKeepsItsDependencies(t *testing.T) {
	deps := webhooksControllerDeps()
	ctrl := NewWebhooksController(deps)
	if ctrl == nil {
		t.Fatal("NewWebhooksController returned nil")
	}
	if ctrl.configs != deps.Configs {
		t.Fatal("wallet webhooks controller did not keep the webhook configs service")
	}
	if ctrl.delivery != deps.Delivery {
		t.Fatal("wallet webhooks controller did not keep the webhook delivery service")
	}
	if ctrl.memberships != deps.Memberships {
		t.Fatal("wallet webhooks controller did not keep the wallet memberships")
	}
}

func TestNewWebhooksControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*WebhooksControllerDeps)
		panic string
	}{
		{
			name:  "webhook configs service",
			clear: func(deps *WebhooksControllerDeps) { deps.Configs = nil },
			panic: "dashboard wallet webhooks controller: webhook configs service is required",
		},
		{
			name:  "webhook delivery service",
			clear: func(deps *WebhooksControllerDeps) { deps.Delivery = nil },
			panic: "dashboard wallet webhooks controller: webhook delivery service is required",
		},
		{
			name:  "wallet memberships",
			clear: func(deps *WebhooksControllerDeps) { deps.Memberships = nil },
			panic: "dashboard wallet webhooks controller: wallet memberships are required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := webhooksControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWebhooksController(deps)
			t.Fatal("expected a panic")
		})
	}
}
