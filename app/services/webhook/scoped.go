package webhook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/pgerr"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	defaultMaxAttempts = 10

	// Local outbox delivery: first retry after LocalRetryBaseBackoff, doubling per
	// failed attempt up to LocalRetryMaxBackoff.
	LocalRetryBaseBackoff = 10 * time.Second
	LocalRetryMaxBackoff  = 10 * time.Minute

	errConfigInactive = "webhook config inactive or deleted"
)

// ScopedEvent is an event about one wallet that must reach only the configs allowed to
// see that wallet, and at most once per config for a given subject.
//
// OccurredAt is set when the event is published after the fact (a backfill): a config
// created after that instant does not receive it, so an endpoint registered later
// is not sent history. Zero means the event happens now and reaches every config.
type ScopedEvent struct {
	EventType     types.EventType
	SubjectID     string
	WalletID      uuid.UUID
	AccountID     *uuid.UUID
	TransactionID *uuid.UUID
	Data          any
	OccurredAt    time.Time
}

func (e ScopedEvent) validate() error {
	if strings.TrimSpace(string(e.EventType)) == "" {
		return errors.New("event type is required")
	}
	if strings.TrimSpace(e.SubjectID) == "" {
		return errors.New("subject id is required")
	}
	if e.WalletID == uuid.Nil {
		return errors.New("wallet id is required")
	}
	if e.Data == nil {
		return errors.New("event data is required")
	}
	return nil
}

// configCanSee reports whether a config may receive events about the wallet. Wallet
// configs only see their wallet; account configs only see their account's wallets;
// legacy configs without an owner keep receiving every event.
func configCanSee(cfg models.WebhookConfig, walletID uuid.UUID, accountID *uuid.UUID) bool {
	if cfg.WalletID != nil && *cfg.WalletID != walletID {
		return false
	}
	if cfg.AccountID != nil && (accountID == nil || *cfg.AccountID != *accountID) {
		return false
	}
	return true
}

// configExistedAt reports whether a config was created no later than at. A zero at
// (an event happening now) and a config without a creation time always qualify.
func configExistedAt(cfg models.WebhookConfig, at time.Time) bool {
	if at.IsZero() || cfg.CreatedAt == nil || cfg.CreatedAt.Carbon == nil || cfg.CreatedAt.IsZero() {
		return true
	}
	return !cfg.CreatedAt.StdTime().After(at)
}

// EnqueueScoped records and queues one delivery per subscribed config that can see the
// wallet (and, for an event with OccurredAt, existed when it occurred). A config that
// already has an event of this type for the subject is skipped, so repeated tracker
// runs and backfills never deliver the same event twice.
func (s *Service) EnqueueScoped(ctx context.Context, event ScopedEvent) (int, error) {
	send, enqueued, err := s.StageScoped(ctx, event)
	if err != nil {
		return 0, err
	}
	send(ctx)
	return enqueued, nil
}

// StageScoped records the deliveries EnqueueScoped would, with ctx, so they join
// the caller's database transaction, and returns the send that queues them, to run
// only after that transaction commits (never nil): a delivery whose row is rolled
// back is never queued.
func (s *Service) StageScoped(ctx context.Context, event ScopedEvent) (func(context.Context), int, error) {
	if err := event.validate(); err != nil {
		return nil, 0, fmt.Errorf("enqueue scoped event: %w", err)
	}

	configs, err := s.webhookConfigRepo.FindActive(ctx)
	if err != nil {
		return nil, 0, fmt.Errorf("query webhook configs: %w", err)
	}

	var staged []types.WebhookMessage
	for _, cfg := range configs {
		if !containsEvent(cfg.Events, string(event.EventType)) || !configCanSee(cfg, event.WalletID, event.AccountID) || !configExistedAt(cfg, event.OccurredAt) {
			continue
		}
		msg, err := s.stageForConfig(ctx, cfg, event)
		if err != nil {
			slog.Error("enqueue scoped webhook", "error", err, "config_id", cfg.ID, "event_type", event.EventType, "subject_id", event.SubjectID)
			continue
		}
		if msg != nil {
			staged = append(staged, *msg)
		}
	}
	return func(sendCtx context.Context) { s.dispatchWebhooks(sendCtx, staged) }, len(staged), nil
}

// stageForConfig records the delivery of event to cfg and returns its queue message,
// nil when cfg already has one for the subject.
func (s *Service) stageForConfig(ctx context.Context, cfg models.WebhookConfig, event ScopedEvent) (*types.WebhookMessage, error) {
	exists, err := s.webhookEventRepo.ExistsForSubject(ctx, cfg.ID, string(event.EventType), event.SubjectID)
	if err != nil {
		return nil, fmt.Errorf("check duplicate: %w", err)
	}
	if exists {
		slog.Info("webhook already enqueued for subject", "config_id", cfg.ID, "event_type", event.EventType, "subject_id", event.SubjectID)
		return nil, nil
	}

	eventID := uuid.New()
	payload, err := json.Marshal(map[string]any{
		"id":         eventID.String(),
		"type":       string(event.EventType),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       event.Data,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal payload: %w", err)
	}

	configID := cfg.ID
	subjectID := event.SubjectID
	webhookEvent := &models.WebhookEvent{
		ID:              eventID,
		TransactionID:   event.TransactionID,
		WebhookConfigID: &configID,
		SubjectID:       &subjectID,
		EventType:       string(event.EventType),
		Payload:         string(payload),
		DeliveryURL:     cfg.URL,
		DeliveryStatus:  models.WebhookDeliveryPending,
		MaxAttempts:     s.resolveDeliverySettings(ctx).MaxAttempts,
	}
	if err := s.webhookEventRepo.Create(ctx, webhookEvent); err != nil {
		if pgerr.IsUniqueViolation(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("insert webhook event: %w", err)
	}

	msg := types.WebhookMessage{
		EventID:     eventID.String(),
		EventType:   event.EventType,
		Payload:     string(payload),
		DeliveryURL: cfg.URL,
		ConfigID:    cfg.ID.String(),
		Attempt:     1,
	}
	if event.TransactionID != nil {
		msg.TransactionID = event.TransactionID.String()
	}
	return &msg, nil
}

// DeliverPending is the local stand-in for the SQS webhook worker: it delivers stored
// events whose retry backoff has elapsed and gives up after their max attempts.
func (s *Service) DeliverPending(ctx context.Context, limit int) (delivered int, err error) {
	events, err := s.webhookEventRepo.FindDueForDelivery(ctx, limit, LocalRetryBaseBackoff, LocalRetryMaxBackoff)
	if err != nil {
		return 0, fmt.Errorf("find due webhook events: %w", err)
	}

	for _, event := range events {
		if ctx.Err() != nil {
			return delivered, ctx.Err()
		}
		if s.deliverStored(ctx, event) {
			delivered++
		}
	}
	return delivered, nil
}

func (s *Service) deliverStored(ctx context.Context, event models.WebhookEvent) bool {
	if event.WebhookConfigID == nil {
		return false
	}
	cfg, err := s.webhookConfigRepo.FindByID(ctx, *event.WebhookConfigID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		cfg, err = nil, nil
	}
	if err != nil {
		slog.Error("load webhook config for delivery", "error", err, "event_id", event.ID)
		return false
	}
	if cfg == nil || !cfg.IsActive {
		if markErr := s.webhookEventRepo.MarkFailed(ctx, event.ID.String(), errConfigInactive); markErr != nil {
			slog.Error("mark webhook event failed", "error", markErr, "event_id", event.ID)
		}
		return false
	}

	msg := queuedMessage(event, event.Attempts+1)
	posted, deliverErr := s.deliver(ctx, msg)
	if deliverErr == nil {
		return posted
	}
	slog.Warn("local webhook delivery failed", "error", deliverErr, "event_id", event.ID, "attempt", msg.Attempt)
	if msg.Attempt >= s.resolveDeliverySettings(ctx).MaxAttempts {
		if markErr := s.webhookEventRepo.MarkFailed(ctx, event.ID.String(), deliverErr.Error()); markErr != nil {
			slog.Error("mark webhook event failed", "error", markErr, "event_id", event.ID)
		}
	}
	return false
}
