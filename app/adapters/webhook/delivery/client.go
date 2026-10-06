package delivery

import (
	"context"
	"fmt"
	"time"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// Client posts one already-signed webhook. It does not see the signing secret.
type Client struct{}

// NewClient returns the outbound HTTP post. Each call uses the timeout on that delivery.
func NewClient() *Client {
	return &Client{}
}

// Post sends the signed body. Headers, URL, and body are the values the service computed.
// A missing context or a non-positive timeout fails before any network use.
func (c *Client) Post(ctx context.Context, call webhook.SignedDelivery) (httpclient.Response, error) {
	if ctx == nil {
		return httpclient.Response{}, fmt.Errorf("webhook delivery: context is required")
	}
	if call.Timeout <= 0 {
		return httpclient.Response{}, fmt.Errorf("webhook delivery: timeout is required")
	}
	return httpclient.NewClient(call.Timeout).Do(ctx, httpclient.Request{
		Method: httpclient.MethodPost,
		URL:    call.URL,
		Header: map[string]string{
			"Content-Type":        "application/json",
			"X-Vault-Signature":   call.Signature,
			"X-Vault-Event":       call.EventType,
			"X-Vault-Delivery-Id": call.DeliveryID,
			"X-Vault-Timestamp":   fmt.Sprintf("%d", time.Now().Unix()),
		},
		Body:    call.Body,
		HasBody: true,
	})
}

var _ webhook.DeliveryClient = (*Client)(nil)
