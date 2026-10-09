package webhooks

import (
	"testing"

	"github.com/macrowallets/waas/app/services/walletrecords"
	"github.com/macrowallets/waas/app/services/webhook"
)

func TestNew_WebhookController_RequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name     string
		configs  *walletrecords.Webhooks
		delivery *webhook.Service
		panic    string
	}{
		{"webhook configs", nil, &webhook.Service{}, "dashboard wallet webhooks controller: webhook configs service is required"},
		{"webhook delivery", &walletrecords.Webhooks{}, nil, "dashboard wallet webhooks controller: webhook delivery service is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if got := recover(); got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewWebhookController(tc.configs, tc.delivery)
			t.Fatal("expected a panic")
		})
	}
}

func TestNew_WebhookController_KeepsItsDependencies(t *testing.T) {
	configs, delivery := &walletrecords.Webhooks{}, &webhook.Service{}

	ctrl := NewWebhookController(configs, delivery)

	if ctrl.configs != configs || ctrl.delivery != delivery {
		t.Fatal("webhook controller did not keep its dependencies")
	}
}
