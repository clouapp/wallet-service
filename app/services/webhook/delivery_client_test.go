package webhook_test

import (
	"testing"

	_ "github.com/macrowallets/waas/app/adapters/webhook/delivery"
	"github.com/macrowallets/waas/app/services/webhook"
)

func TestNewDeliveryClientIsTheRegisteredAdapter(t *testing.T) {
	if webhook.NewDeliveryClient() == nil {
		t.Fatal("webhook delivery client was not registered")
	}
}
