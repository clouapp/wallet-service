// Package swapproviders implements the external swap APIs behind swap.Provider.
package swapproviders

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/services/swap"
	"github.com/macrowallets/waas/pkg/httpclient"
)

const maxResponseBytes = 4 << 20

// HTTPClient is the bounded HTTP transport used by the adapters.
type HTTPClient interface {
	Do(context.Context, httpclient.Request) (httpclient.Response, error)
}

// Deps supplies the HTTP client and an optional endpoint for private deployments.
// The composition root uses the official endpoint; it is not administrator input.
type Deps struct {
	Client  HTTPClient
	BaseURL string
}

type client struct {
	transport HTTPClient
	base      string
	header    string
}

func newClient(deps Deps, base, header string) client {
	if deps.Client == nil {
		deps.Client = httpclient.NewClientWithoutRedirects(12 * time.Second)
	}
	if deps.BaseURL != "" {
		base = deps.BaseURL
	}
	return client{transport: deps.Client, base: strings.TrimRight(base, "/"), header: header}
}

func (c client) request(ctx context.Context, method, path string, body any, credential swap.ProviderConfig, result any) error {
	var payload []byte
	var err error
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			return swap.ErrInvalidRequest
		}
	}
	headers := map[string]string{"Accept": "application/json", "Content-Type": "application/json", "User-Agent": "MacroWallets-Swap/1.0"}
	if credential.APIKey != "" {
		headers[c.header] = credential.APIKey
	}
	response, err := c.transport.Do(ctx, httpclient.Request{Method: method, URL: c.base + path, Header: headers, Body: payload, HasBody: body != nil, MaxBytes: maxResponseBytes + 1})
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return swap.ErrProviderUnavailable
	}
	switch {
	case response.StatusCode == 404:
		return swap.ErrNoRoute
	case response.StatusCode == 429 || response.StatusCode >= 500:
		return swap.ErrProviderUnavailable
	case response.StatusCode < 200 || response.StatusCode >= 300:
		return swap.ErrProviderRejected
	}
	if len(response.Body) > maxResponseBytes {
		return swap.ErrInvalidQuote
	}
	if err := json.Unmarshal(response.Body, result); err != nil {
		return swap.ErrInvalidQuote
	}
	return nil
}

func jsonPayload(value any) (json.RawMessage, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode swap route: %w", err)
	}
	return encoded, nil
}
