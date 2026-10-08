package webhook

import (
	"context"
	"time"

	"github.com/macrowallets/waas/pkg/httpclient"
)

// SignedDelivery is one outbound POST. Signature is already the hex HMAC-SHA256
// of Body. The signing secret is not part of this value and is not sent.
type SignedDelivery struct {
	URL        string
	Body       []byte
	Signature  string
	EventType  string
	DeliveryID string
	Timeout    time.Duration
}

// DeliveryClient posts a signed webhook. The delivery adapter registers the live client.
type DeliveryClient interface {
	Post(ctx context.Context, call SignedDelivery) (httpclient.Response, error)
}

// deliveryClient is the live outbound HTTP post. The delivery adapter registers it.
var deliveryClient func() DeliveryClient

// SetDeliveryClient registers the outbound HTTP post. The delivery adapter calls it
// from init. Callers keep DeliveryClient.
func SetDeliveryClient(factory func() DeliveryClient) {
	deliveryClient = factory
}

// NewDeliveryClient returns the registered HTTP post. A process that does not
// link the adapter gets nil.
func NewDeliveryClient() DeliveryClient {
	if deliveryClient == nil {
		return nil
	}
	return deliveryClient()
}
