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
type ScopedEvent struct {
	EventType     types.EventType
	SubjectID     string
	WalletID      uuid.UUID
	AccountID     *uuid.UUID
	TransactionID *uuid.UUID
	Data          any
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

// EnqueueScoped records and queues one delivery per subscribed config that can see the
// wallet. A config that already has an event of this type for the subject is skipped,
// so repeated tracker runs and backfills never deliver the same event twice.
func (s *Service) EnqueueScoped(ctx context.Context, event ScopedEvent) (int, error) {
	if err := event.validate(); err != nil {
		return 0, fmt.Errorf("enqueue scoped event: %w", err)
	}

	configs, err := s.webhookConfigRepo.FindActive(ctx)
	if err != nil {
		return 0, fmt.Errorf("query webhook configs: %w", err)
	}

	enqueued := 0
	for _, cfg := range configs {
		if !containsEvent(cfg.Events, string(event.EventType)) || !configCanSee(cfg, event.WalletID, event.AccountID) {
			continue
		}
		created, err := s.enqueueForConfig(ctx, cfg, event)
		if err != nil {
			slog.Error("enqueue scoped webhook", "error", err, "config_id", cfg.ID, "event_type", event.EventType, "subject_id", event.SubjectID)
			continue
		}
		if created {
			enqueued++
		}
	}
	return enqueued, nil
}

func (s *Service) enqueueForConfig(ctx context.Context, cfg models.WebhookConfig, event ScopedEvent) (bool, error) {
	exists, err := s.webhookEventRepo.ExistsForSubject(ctx, cfg.ID, string(event.EventType), event.SubjectID)
	if err != nil {
		return false, fmt.Errorf("check duplicate: %w", err)
	}
	if exists {
		slog.Info("webhook already enqueued for subject", "config_id", cfg.ID, "event_type", event.EventType, "subject_id", event.SubjectID)
		return false, nil
	}

	eventID := uuid.New()
	payload, err := json.Marshal(map[string]any{
		"id":         eventID.String(),
		"type":       string(event.EventType),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       event.Data,
	})
	if err != nil {
		return false, fmt.Errorf("marshal payload: %w", err)
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
		MaxAttempts:     defaultMaxAttempts,
	}
	if err := s.webhookEventRepo.Create(ctx, webhookEvent); err != nil {
		if isUniqueViolation(err) {
			return false, nil
		}
		return false, fmt.Errorf("insert webhook event: %w", err)
	}

	msg := types.WebhookMessage{
		EventID:     eventID.String(),
		EventType:   event.EventType,
		Payload:     string(payload),
		DeliveryURL: cfg.URL,
		Secret:      cfg.Secret,
		Attempt:     1,
	}
	if event.TransactionID != nil {
		msg.TransactionID = event.TransactionID.String()
	}
	if s.sqs != nil {
		if err := s.sqs.SendWebhook(ctx, msg); err != nil {
			slog.Error("sqs send webhook", "error", err, "event_id", eventID)
		}
	}
	return true, nil
}

func isUniqueViolation(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "duplicate key") || strings.Contains(message, "sqlstate 23505")
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

	msg := types.WebhookMessage{
		EventID:     event.ID.String(),
		EventType:   types.EventType(event.EventType),
		Payload:     event.Payload,
		DeliveryURL: event.DeliveryURL,
		Secret:      cfg.Secret,
		Attempt:     event.Attempts + 1,
	}
	if event.TransactionID != nil {
		msg.TransactionID = event.TransactionID.String()
	}

	deliverErr := s.Deliver(ctx, msg)
	if deliverErr == nil {
		return true
	}
	slog.Warn("local webhook delivery failed", "error", deliverErr, "event_id", event.ID, "attempt", msg.Attempt)
	if msg.Attempt >= event.MaxAttempts {
		if markErr := s.webhookEventRepo.MarkFailed(ctx, event.ID.String(), deliverErr.Error()); markErr != nil {
			slog.Error("mark webhook event failed", "error", markErr, "event_id", event.ID)
		}
	}
	return false
}
