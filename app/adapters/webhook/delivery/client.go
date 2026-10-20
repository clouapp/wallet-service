package delivery

import (
	"context"
	"fmt"
	"strconv"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// Client posts one already-signed webhook. It does not see the signing secret.
type Client struct{}

// NewClient returns the outbound HTTP post. Each call uses the timeout on that delivery.
func NewClient() *Client {
	return &Client{}
}

// Post sends the signed body. Headers, URL, and body are the values the service computed;
// X-Vault-Timestamp is the time the service signed at, the t of X-Vault-Signature-V2.
// A missing context, a non-positive timeout or a delivery without its timestamped
// signature fails before any network use, as a build error (httpclient.IsBuild): it
// was never sent, so it is not an attempt. A redirect is not followed: the 3xx is the
// response, and the signed headers never reach the Location it names.
func (c *Client) Post(ctx context.Context, call webhook.SignedDelivery) (httpclient.Response, error) {
	if ctx == nil {
		return httpclient.Response{}, httpclient.BuildFailure(fmt.Errorf("webhook delivery: context is required"))
	}
	if call.Timeout <= 0 {
		return httpclient.Response{}, httpclient.BuildFailure(fmt.Errorf("webhook delivery: timeout is required"))
	}
	if call.Timestamp <= 0 || call.SignatureV2 == "" {
		return httpclient.Response{}, httpclient.BuildFailure(fmt.Errorf("webhook delivery: the timestamped signature is required"))
	}
	return httpclient.NewClientWithoutRedirects(call.Timeout).Do(ctx, httpclient.Request{
		Method: httpclient.MethodPost,
		URL:    call.URL,
		Header: map[string]string{
			"Content-Type":         "application/json",
			"X-Vault-Signature":    call.Signature,
			"X-Vault-Signature-V2": call.SignatureV2,
			"X-Vault-Event":        call.EventType,
			"X-Vault-Delivery-Id":  call.DeliveryID,
			"X-Vault-Timestamp":    strconv.FormatInt(call.Timestamp, 10),
		},
		Body:    call.Body,
		HasBody: true,
	})
}

var _ webhook.DeliveryClient = (*Client)(nil)
