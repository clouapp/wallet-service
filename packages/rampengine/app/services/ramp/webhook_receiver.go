package ramp

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/webhooks"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrWebhookRejected means the delivery could not be authenticated: no adapter
// is registered for the provider, or the adapter refused its signature. The two
// are one error on purpose — telling a caller which would map the endpoint.
var ErrWebhookRejected = errors.New("ramp webhook: rejected")

// WebhookReceiver is the ingest half of a provider webhook: it authenticates a
// delivery, stores it once, and queues it for the WebhookProcessor.
type WebhookReceiver struct {
	factory    *ramp.Factory
	events     repositories.WebhookEventRepository
	dispatcher EventDispatcher
}

func NewWebhookReceiver(
	factory *ramp.Factory,
	events repositories.WebhookEventRepository,
	dispatcher EventDispatcher,
) *WebhookReceiver {
	return &WebhookReceiver{factory: factory, events: events, dispatcher: dispatcher}
}

// Receive verifies and normalizes the delivery before anything is persisted, so
// a delivery that cannot be authenticated never becomes a row. A duplicate is
// accepted without being queued again.
func (r *WebhookReceiver) Receive(ctx context.Context, in dto.ReceiveWebhookDTO) error {
	adapter, err := r.factory.Get(in.Provider)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWebhookRejected, err)
	}
	result, err := adapter.ParseWebhook(ctx, in.Headers, in.Body)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWebhookRejected, err)
	}

	row := &models.WebhookEvent{
		Provider:   in.Provider,
		EventID:    dedupKey(result.EventID, in.Body),
		EventType:  result.EventType,
		RawPayload: string(in.Body),
		ReceivedAt: time.Now(),
	}
	if ref := firstNonEmpty(result.ProviderOrderID, result.ExternalCustomerID); ref != "" {
		row.ExternalRef = &ref
	}

	// CreateIfNew collapses the benign unique violation to (false, nil), so an
	// error here is genuinely transient: nothing was written and nothing
	// dispatched.
	inserted, err := r.events.CreateIfNew(row)
	if err != nil {
		return fmt.Errorf("ramp webhook: persist %s event: %w", in.Provider, err)
	}
	if !inserted {
		return nil
	}

	if err := r.dispatcher.Dispatch(row.ID); err != nil {
		// The row is stored and unprocessed, which is exactly what the
		// reconciliation sweep looks for. Failing the delivery would make the
		// provider redeliver an event we already have.
		facades.Log().WithContext(ctx).Errorf("ramp webhook: dispatch %s event: %v", in.Provider, err)
	}
	return nil
}

// dedupKey is what `(provider, event_id)` deduplicates on.
//
// A provider that supplies its own event id gets to define delivery identity.
// When it does not, hashing the already-verified body makes any verbatim
// replay collapse onto one row — which an unsigned header could not do, since
// varying it would turn one captured payload into unlimited distinct events.
func dedupKey(eventID string, raw []byte) string {
	if eventID != "" {
		return eventID
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
