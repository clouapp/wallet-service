package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	// MaxRedeliveryEvents bounds the events one redelivery request requeues.
	MaxRedeliveryEvents = 500
	// RedeliveryWindow is how far back a redelivery reaches: only events created in
	// the last seven days are delivered again.
	RedeliveryWindow = 7 * 24 * time.Hour
	// redeliveryCooldown leaves alone an event that is already pending and was queued
	// or attempted this recently, so a repeated request does not queue it twice.
	redeliveryCooldown = time.Minute
)

var (
	ErrWebhookRedeliveryUnclaimed = errors.New("claim the webhook with its secret before asking for a redelivery")
	ErrWebhookInactive            = errors.New("webhook is inactive")
	ErrRedeliverySelection        = errors.New("send event_ids, or since with optional until and types")
	ErrRedeliveryTooManyEvents    = errors.New("at most 500 events per redelivery; narrow the selection")
	ErrRedeliveryWindow           = errors.New("since must be within the last 7 days and before until")
)

// RedeliverySelection picks the events of one webhook to deliver again: EventIDs,
// or every event created in [Since, Until), Until zero meaning now, of Types when
// any are given. Either way only events created in the last RedeliveryWindow count.
type RedeliverySelection struct {
	EventIDs []uuid.UUID
	Since    time.Time
	Until    time.Time
	Types    []string
}

func (s RedeliverySelection) byID() bool { return len(s.EventIDs) > 0 }

func (s RedeliverySelection) validate(now time.Time) error {
	bySince := !s.Since.IsZero()
	switch {
	case s.byID() == bySince:
		return ErrRedeliverySelection
	case s.byID() && (!s.Until.IsZero() || len(s.Types) > 0):
		return ErrRedeliverySelection
	case len(s.EventIDs) > MaxRedeliveryEvents:
		return ErrRedeliveryTooManyEvents
	case s.byID():
		return nil
	}
	if s.Since.Before(now.Add(-RedeliveryWindow)) || !s.Since.Before(s.until(now)) {
		return ErrRedeliveryWindow
	}
	for _, eventType := range s.Types {
		if !types.IsSubscribableEvent(eventType) {
			return fmt.Errorf("%w: %q", ErrWebhookUnknownEvent, eventType)
		}
	}
	return nil
}

func (s RedeliverySelection) until(now time.Time) time.Time {
	if s.Until.IsZero() {
		return now
	}
	return s.Until
}

// Redeliver delivers again the selected events of one of the account's webhooks,
// whatever their delivery status: each goes back to pending with a fresh attempt
// budget and is sent to the webhook queue, or left to the local outbox when no queue
// is configured. A queue send that fails leaves the row pending for the outbox
// re-dispatch. Events of another webhook, older than RedeliveryWindow, or already
// pending and queued moments ago are left alone. So is an event that is not about a
// wallet of the account: a legacy webhook received the events of every account
// before it was claimed, and its new owner may only ask for its own again (an event
// whose wallet cannot be told is left alone too). The webhook must belong to the
// account (a legacy webhook is claimed first) and be active. Each delivery is signed
// when it is sent, so a redelivered event carries a fresh X-Vault-Timestamp. It
// returns how many events were requeued.
func (s *Service) Redeliver(ctx context.Context, accountID, configID uuid.UUID, selection RedeliverySelection) (int, error) {
	cfg, err := s.redeliverableConfig(ctx, accountID, configID)
	if err != nil {
		return 0, err
	}
	now := time.Now().UTC()
	if invalid := selection.validate(now); invalid != nil {
		return 0, invalid
	}

	ids := selection.EventIDs
	if !selection.byID() {
		ids, err = s.webhookEventRepo.FindForRedelivery(ctx, cfg.ID, accountID, selection.Since, selection.until(now), selection.Types, MaxRedeliveryEvents+1)
		if err != nil {
			return 0, fmt.Errorf("select webhook events to redeliver: %w", err)
		}
		if len(ids) > MaxRedeliveryEvents {
			return 0, ErrRedeliveryTooManyEvents
		}
	}
	if len(ids) == 0 {
		return 0, nil
	}

	events, err := s.webhookEventRepo.RequeueForRedelivery(ctx, cfg.ID, accountID, distinctIDs(ids), now.Add(-RedeliveryWindow), now.Add(-redeliveryCooldown))
	if err != nil {
		return 0, fmt.Errorf("requeue webhook events: %w", err)
	}
	if s.sqs != nil {
		for _, event := range events {
			if err := s.sqs.SendWebhook(ctx, queuedMessage(event, 1)); err != nil {
				slog.Error("redeliver webhook: sqs send failed, the outbox re-dispatch sends it later", "error", err, "event_id", event.ID)
			}
		}
	}
	slog.Info("webhook events requeued for redelivery", "webhook_id", cfg.ID, "requested", len(ids), "requeued", len(events))
	return len(events), nil
}

// redeliverableConfig loads a webhook the account owns and that is active. A
// webhook of another account, of a wallet, or unknown is not found.
func (s *Service) redeliverableConfig(ctx context.Context, accountID, configID uuid.UUID) (*models.WebhookConfig, error) {
	if accountID == uuid.Nil || configID == uuid.Nil {
		return nil, ErrWebhookConfigNotFound
	}
	cfg, err := s.webhookConfigRepo.FindByID(ctx, configID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, ErrWebhookConfigNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg == nil || cfg.WalletID != nil || (cfg.AccountID != nil && *cfg.AccountID != accountID) {
		return nil, ErrWebhookConfigNotFound
	}
	if cfg.AccountID == nil {
		return nil, ErrWebhookRedeliveryUnclaimed
	}
	if !cfg.IsActive {
		return nil, ErrWebhookInactive
	}
	return cfg, nil
}

func distinctIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
