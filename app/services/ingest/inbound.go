package ingest

import (
	"context"
	"errors"
	"log/slog"

	"github.com/macrowallets/waas/app/services/ingest/providers"
)

var (
	// ErrUnknownProvider is a callback from a provider name that has no adapter.
	ErrUnknownProvider = errors.New("unknown provider")
	// ErrInvalidPayload is a verified callback whose body the provider adapter
	// cannot parse. The parse error is not returned: it can quote the body.
	ErrInvalidPayload = errors.New("invalid payload")
)

// Webhook is one provider callback whose signature was already verified.
type Webhook struct {
	Provider string
	ChainID  string
	Body     []byte
}

// eventIngester records a typed event.
type eventIngester interface {
	Ingest(ctx context.Context, event InboundEvent) error
}

// Inbound turns a verified callback into an ingested event: it picks the
// provider's adapter, parses the body, and hands the typed event on.
type Inbound struct {
	ingest eventIngester
	lookup func() map[string]providers.WebhookProvider
}

// NewInbound wires the callback handling. lookup may be nil: the adapter
// registered for the provider is used then.
func NewInbound(ingest eventIngester, lookup func() map[string]providers.WebhookProvider) *Inbound {
	return &Inbound{ingest: ingest, lookup: lookup}
}

// Receive ingests one verified callback. The body is parsed only here, after
// the signature check, and is never logged.
func (i *Inbound) Receive(ctx context.Context, hook Webhook) error {
	provider, found := providers.Resolve(hook.Provider, i.lookup)
	if !found {
		return ErrUnknownProvider
	}
	transfers, err := provider.ParsePayload(hook.Body)
	if err != nil {
		slog.Warn("ingest parse rejected", "provider", hook.Provider)
		return ErrInvalidPayload
	}
	return i.ingest.Ingest(ctx, InboundEvent{ChainID: hook.ChainID, Transfers: transfers})
}
