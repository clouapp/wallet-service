package webhook_test

import (
	"context"
	"testing"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/httpclient"
)

type stubDelivery struct{}

func (stubDelivery) Post(context.Context, webhook.SignedDelivery) (httpclient.Response, error) {
	return httpclient.Response{}, nil
}

func TestNew_Delivery_ClientIsTheRegisteredAdapter(t *testing.T) {
	webhook.SetDeliveryClient(func() webhook.DeliveryClient { return stubDelivery{} })
	if webhook.NewDeliveryClient() == nil {
		t.Fatal("webhook delivery client was not registered")
	}
}
