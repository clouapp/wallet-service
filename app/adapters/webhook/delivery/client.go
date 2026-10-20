package delivery

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/httpclient"
)

// deliveryResponseMaxBytes is how much of a receiver's answer is read: only its
// status decides a delivery, and its body is never kept or returned to anyone.
const deliveryResponseMaxBytes = 64 << 10

// Client posts one already-signed webhook. It does not see the signing secret. Every
// post goes through one destination guard: the URL is checked before the request is
// built, and every connection is opened by the guard, to an address it checked.
type Client struct {
	guard     *egress.Guard
	transport *httpclient.DirectTransport
}

// NewClient returns the outbound HTTP post over guard, the guard the webhook service
// registers URLs with. Each call uses the timeout on that delivery; connections are
// shared between calls.
func NewClient(guard *egress.Guard) *Client {
	if guard == nil {
		panic("webhook delivery: destination guard is required")
	}
	return &Client{guard: guard, transport: httpclient.NewDirectTransport(guard.DialContext)}
}

// Post sends the signed body. Headers, URL, and body are the values the service computed;
// X-Vault-Timestamp is the time the service signed at, the t of X-Vault-Signature-V2.
// A missing context, a non-positive timeout or a delivery without its timestamped
// signature fails before any network use, as a build error (httpclient.IsBuild): it
// was never sent, so it is not an attempt. So does a destination the guard refuses,
// whether by its URL or by the address its name resolves to at send time: the error
// matches webhook.ErrURLNotAllowed. A redirect is not followed: the 3xx is the
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
	if err := c.guard.CheckURL(call.URL); err != nil {
		return httpclient.Response{}, refused(err)
	}
	resp, err := c.transport.ClientWithoutRedirects(call.Timeout).Do(ctx, httpclient.Request{
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
		Body:     call.Body,
		HasBody:  true,
		MaxBytes: deliveryResponseMaxBytes,
	})
	if errors.Is(err, egress.ErrRefused) {
		return httpclient.Response{}, refused(err)
	}
	return resp, err
}

// refused is a delivery the guard stopped before anything was sent.
func refused(err error) error {
	return httpclient.BuildFailure(fmt.Errorf("%w: %w", webhook.ErrURLNotAllowed, err))
}

var _ webhook.DeliveryClient = (*Client)(nil)
