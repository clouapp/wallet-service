package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/pkg/types"
)

// ---------------------------------------------------------------------------
// Service — webhook management.
// EnqueueEvent sends to SQS, Deliver is called by the Lambda worker.
// ---------------------------------------------------------------------------

type Service struct {
	sqs               queue.Sender
	webhookConfigRepo repositories.WebhookConfigRepository
	webhookEventRepo  repositories.WebhookEventRepository
}

func NewService(sqs queue.Sender, webhookConfigRepo repositories.WebhookConfigRepository, webhookEventRepo repositories.WebhookEventRepository) *Service {
	return &Service{sqs: sqs, webhookConfigRepo: webhookConfigRepo, webhookEventRepo: webhookEventRepo}
}

// EnqueueEvent creates webhook events for the matching configs and sends them to SQS.
// It cannot tell which account owns the subject, so account-owned configs never
// receive these events (use EnqueueScoped); wallet configs only receive them when the
// data is a transaction of their wallet; legacy configs without an owner receive all.
func (s *Service) EnqueueEvent(ctx context.Context, txID uuid.UUID, eventType types.EventType, data interface{}) {
	payload, err := json.Marshal(map[string]interface{}{
		"id":         uuid.New().String(),
		"type":       string(eventType),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       data,
	})
	if err != nil {
		slog.Error("marshal webhook payload", "error", err)
		return
	}

	allConfigs, err := s.webhookConfigRepo.FindActive()
	if err != nil {
		slog.Error("query webhook configs", "error", err)
		return
	}

	var configs []models.WebhookConfig
	eventTypeStr := string(eventType)
	subjectWallet := legacyEventWallet(data)
	for _, cfg := range allConfigs {
		if containsEvent(cfg.Events, eventTypeStr) && legacyConfigCanSee(cfg, subjectWallet) {
			configs = append(configs, cfg)
		}
	}

	for _, cfg := range configs {
		eventID := uuid.New().String()

		configID := cfg.ID
		webhookEvent := &models.WebhookEvent{
			ID:              uuid.MustParse(eventID),
			TransactionID:   &txID,
			WebhookConfigID: &configID,
			EventType:       string(eventType),
			Payload:         string(payload),
			DeliveryURL:     cfg.URL,
			DeliveryStatus:  "pending",
			Attempts:        0,
			MaxAttempts:     10,
		}
		if err := s.webhookEventRepo.Create(webhookEvent); err != nil {
			slog.Error("insert webhook event", "error", err)
			continue
		}

		msg := types.WebhookMessage{
			EventID:       eventID,
			TransactionID: txID.String(),
			EventType:     eventType,
			Payload:       string(payload),
			DeliveryURL:   cfg.URL,
			Secret:        cfg.Secret,
			Attempt:       1,
		}
		if s.sqs == nil {
			continue
		}
		if err := s.sqs.SendWebhook(ctx, msg); err != nil {
			slog.Error("sqs send webhook", "error", err, "event_id", eventID)
		}
	}
}

func legacyConfigCanSee(cfg models.WebhookConfig, subjectWallet *uuid.UUID) bool {
	if cfg.AccountID != nil {
		return false
	}
	if cfg.WalletID == nil {
		return true
	}
	return subjectWallet != nil && *cfg.WalletID == *subjectWallet
}

func legacyEventWallet(data interface{}) *uuid.UUID {
	switch tx := data.(type) {
	case models.Transaction:
		return &tx.WalletID
	case *models.Transaction:
		if tx != nil {
			return &tx.WalletID
		}
	}
	return nil
}

// Deliver executes the HTTP delivery. Called by the SQS Lambda worker.
// Returns error to trigger SQS retry → eventually DLQ after 10 failures.
func (s *Service) Deliver(ctx context.Context, msg types.WebhookMessage) error {
	mac := hmac.New(sha256.New, []byte(msg.Secret))
	mac.Write([]byte(msg.Payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	req, err := http.NewRequestWithContext(ctx, "POST", msg.DeliveryURL, bytes.NewReader([]byte(msg.Payload)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Signature", signature)
	req.Header.Set("X-Vault-Event", string(msg.EventType))
	req.Header.Set("X-Vault-Delivery-Id", msg.EventID)
	req.Header.Set("X-Vault-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		s.markAttempt(ctx, msg.EventID, err.Error())
		return fmt.Errorf("http send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		s.markAttempt(ctx, msg.EventID, errMsg)
		return fmt.Errorf("delivery failed: %s", errMsg)
	}

	s.webhookEventRepo.MarkDelivered(msg.EventID)

	slog.Info("webhook delivered", "event_id", msg.EventID, "url", msg.DeliveryURL)
	return nil
}

func (s *Service) markAttempt(ctx context.Context, eventID, errMsg string) {
	s.webhookEventRepo.IncrementAttempt(eventID, errMsg)
}

// ---------------------------------------------------------------------------
// Webhook config CRUD
// ---------------------------------------------------------------------------

// CreateConfig registers an account-level webhook. A nil accountID creates a
// legacy unowned config.
func (s *Service) CreateConfig(ctx context.Context, url, secret string, events []string, accountID *uuid.UUID) (*models.WebhookConfig, error) {
	cfg := &models.WebhookConfig{
		ID:        uuid.New(),
		URL:       url,
		Secret:    secret,
		Events:    pgArray(events),
		IsActive:  true,
		AccountID: accountID,
	}
	if err := s.webhookConfigRepo.Create(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) ListConfigs(ctx context.Context) ([]models.WebhookConfig, error) {
	return s.webhookConfigRepo.FindAll()
}

// ListAccountConfigs returns the account-level configs an account may manage: its own
// plus legacy unowned ones it can claim.
func (s *Service) ListAccountConfigs(ctx context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	if accountID == uuid.Nil {
		return nil, errors.New("account id is required")
	}
	return s.webhookConfigRepo.FindVisibleToAccount(accountID)
}

var (
	ErrWebhookConfigNotFound     = errors.New("webhook not found")
	ErrWebhookUpdateEmpty        = errors.New("events or is_active is required")
	ErrWebhookEventsEmpty        = errors.New("events must not be empty")
	ErrWebhookUnknownEvent       = errors.New("unknown webhook event")
	ErrWebhookOwnershipNotProven = errors.New("secret must match to claim a webhook created before account ownership")
)

// ConfigUpdate changes an account-level config. Secret is only a proof of possession
// for claiming a legacy unowned config; it is never changed here.
type ConfigUpdate struct {
	Events   []string
	IsActive *bool
	Secret   string
}

// UpdateAccountConfig updates a config owned by the account. A legacy unowned config is
// claimed by the account when the caller presents its signing secret.
func (s *Service) UpdateAccountConfig(ctx context.Context, accountID, configID uuid.UUID, update ConfigUpdate) (*models.WebhookConfig, error) {
	if accountID == uuid.Nil || configID == uuid.Nil {
		return nil, ErrWebhookConfigNotFound
	}
	if update.Events == nil && update.IsActive == nil {
		return nil, ErrWebhookUpdateEmpty
	}

	cfg, err := s.webhookConfigRepo.FindByID(configID)
	if err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg == nil || cfg.WalletID != nil || (cfg.AccountID != nil && *cfg.AccountID != accountID) {
		return nil, ErrWebhookConfigNotFound
	}
	if cfg.AccountID == nil && !hmac.Equal([]byte(update.Secret), []byte(cfg.Secret)) {
		return nil, ErrWebhookOwnershipNotProven
	}

	fields := map[string]any{"account_id": accountID}
	if update.Events != nil {
		events, err := normalizeSubscribableEvents(update.Events)
		if err != nil {
			return nil, err
		}
		fields["events"] = pgArray(events)
		cfg.Events = pgArray(events)
	}
	if update.IsActive != nil {
		fields["is_active"] = *update.IsActive
		cfg.IsActive = *update.IsActive
	}
	if err := s.webhookConfigRepo.UpdateFields(cfg.ID, fields); err != nil {
		return nil, fmt.Errorf("update webhook config: %w", err)
	}
	owner := accountID
	cfg.AccountID = &owner
	return cfg, nil
}

func normalizeSubscribableEvents(events []string) ([]string, error) {
	if len(events) == 0 {
		return nil, ErrWebhookEventsEmpty
	}
	seen := make(map[string]bool, len(events))
	normalized := make([]string, 0, len(events))
	for _, raw := range events {
		event := strings.TrimSpace(raw)
		if !types.IsSubscribableEvent(event) {
			return nil, fmt.Errorf("%w: %q", ErrWebhookUnknownEvent, event)
		}
		if seen[event] {
			continue
		}
		seen[event] = true
		normalized = append(normalized, event)
	}
	return normalized, nil
}

func (s *Service) DeleteConfig(ctx context.Context, id uuid.UUID) error {
	return s.webhookConfigRepo.DeleteByID(id)
}

func pgArray(arr []string) string {
	s := "{"
	for i, v := range arr {
		if i > 0 {
			s += ","
		}
		s += `"` + v + `"`
	}
	return s + "}"
}

// containsEvent checks if an event type is in the postgres array string
// The format is: {"event1","event2","event3"}
func containsEvent(eventsStr, eventType string) bool {
	return strings.Contains(eventsStr, `"`+eventType+`"`)
}
