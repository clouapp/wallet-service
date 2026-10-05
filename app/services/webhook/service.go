package webhook

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/queue"
	"github.com/macrowallets/waas/pkg/httpclient"
	"github.com/macrowallets/waas/pkg/types"
)

// configStore is the webhook endpoint persistence this service uses.
type configStore interface {
	FindActive(ctx context.Context) ([]models.WebhookConfig, error)
	Create(ctx context.Context, cfg *models.WebhookConfig) error
	FindAll(ctx context.Context) ([]models.WebhookConfig, error)
	FindVisibleToAccount(ctx context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error)
	FindByID(ctx context.Context, id uuid.UUID) (*models.WebhookConfig, error)
	AssignAccount(ctx context.Context, id, accountID uuid.UUID, events *string, isActive *bool) error
	DeleteByID(ctx context.Context, id uuid.UUID) error
}

// eventStore is the delivery-queue persistence this service uses.
type eventStore interface {
	Create(ctx context.Context, event *models.WebhookEvent) error
	MarkDelivered(ctx context.Context, eventID string) error
	IncrementAttempt(ctx context.Context, eventID, errMsg string) error
	ExistsForSubject(ctx context.Context, configID uuid.UUID, eventType, subjectID string) (bool, error)
	FindDueForDelivery(ctx context.Context, limit int, baseBackoff, maxBackoff time.Duration) ([]models.WebhookEvent, error)
	MarkFailed(ctx context.Context, eventID, errMsg string) error
}

// ---------------------------------------------------------------------------
// Service — webhook management.
// EnqueueEvent sends to SQS, Deliver is called by the Lambda worker.
// ---------------------------------------------------------------------------

type Service struct {
	sqs               queue.Sender
	webhookConfigRepo configStore
	webhookEventRepo  eventStore
	deliverySettings  deliverySettingsSource
}

// Deps is everything the webhook service needs. A nil field means that
// dependency is absent.
type Deps struct {
	SQS     queue.Sender
	Configs configStore
	Events  eventStore
}

// NewService wires the webhook service from Deps.
func NewService(deps Deps) *Service {
	return &Service{
		sqs:               deps.SQS,
		webhookConfigRepo: deps.Configs,
		webhookEventRepo:  deps.Events,
	}
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

	allConfigs, err := s.webhookConfigRepo.FindActive(ctx)
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
			MaxAttempts:     s.resolveDeliverySettings(ctx).MaxAttempts,
		}
		if err := s.webhookEventRepo.Create(ctx, webhookEvent); err != nil {
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

// StageWithdrawalBroadcasting inserts withdrawal.broadcasting webhook rows using
// ctx, so they join the caller's transaction. The returned send delivers those
// rows and runs only after that transaction commits. A nil send means no config
// matched. The signing secret stays inside the send closure and is not logged.
func (s *Service) StageWithdrawalBroadcasting(ctx context.Context, tx *models.Transaction) (func(context.Context), error) {
	if s == nil {
		return nil, fmt.Errorf("stage withdrawal broadcasting: webhook service is required")
	}
	if tx == nil {
		return nil, fmt.Errorf("stage withdrawal broadcasting: transaction is required")
	}
	if s.webhookConfigRepo == nil || s.webhookEventRepo == nil {
		return nil, fmt.Errorf("stage withdrawal broadcasting: webhook store is required")
	}
	msgs, err := s.stageLegacyEvent(ctx, &tx.ID, types.EventWithdrawalBroadcasting, tx)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	staged := append([]types.WebhookMessage(nil), msgs...)
	return func(sendCtx context.Context) {
		s.dispatchWebhooks(sendCtx, staged)
	}, nil
}

// StageSweepBroadcast inserts sweep.broadcast webhook rows using ctx, so they join
// the caller's transaction. The returned send delivers those rows and runs only
// after that transaction commits. A nil send means no config matched. The signing
// secret stays inside the send closure and is not logged.
func (s *Service) StageSweepBroadcast(ctx context.Context, tx *models.Transaction) (func(context.Context), error) {
	if s == nil {
		return nil, fmt.Errorf("stage sweep broadcast: webhook service is required")
	}
	if tx == nil {
		return nil, fmt.Errorf("stage sweep broadcast: transaction is required")
	}
	if s.webhookConfigRepo == nil || s.webhookEventRepo == nil {
		return nil, fmt.Errorf("stage sweep broadcast: webhook store is required")
	}
	msgs, err := s.stageLegacyEvent(ctx, &tx.ID, types.EventSweepBroadcast, tx)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	staged := append([]types.WebhookMessage(nil), msgs...)
	return func(sendCtx context.Context) {
		s.dispatchWebhooks(sendCtx, staged)
	}, nil
}

// StageSweepConfirmed inserts sweep.confirmed webhook rows using ctx, so they
// join the caller's transaction. The returned send delivers those rows and runs
// only after that transaction commits. A nil send means no config matched. The
// signing secret stays inside the send closure and is not logged.
func (s *Service) StageSweepConfirmed(ctx context.Context, tx *models.Transaction) (func(context.Context), error) {
	if s == nil {
		return nil, fmt.Errorf("stage sweep confirmed: webhook service is required")
	}
	if tx == nil {
		return nil, fmt.Errorf("stage sweep confirmed: transaction is required")
	}
	if s.webhookConfigRepo == nil || s.webhookEventRepo == nil {
		return nil, fmt.Errorf("stage sweep confirmed: webhook store is required")
	}
	msgs, err := s.stageLegacyEvent(ctx, &tx.ID, types.EventSweepConfirmed, tx)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	staged := append([]types.WebhookMessage(nil), msgs...)
	return func(sendCtx context.Context) {
		s.dispatchWebhooks(sendCtx, staged)
	}, nil
}

// StageWalletGasStatusChanged inserts wallet.gas_status.changed webhook rows
// using ctx, so they join the caller's transaction. The returned send delivers
// those rows and runs only after that transaction commits. A nil send means no
// config matched. This event has no transaction row, so transaction_id stays
// empty. The signing secret stays inside the send closure and is not logged.
func (s *Service) StageWalletGasStatusChanged(ctx context.Context, walletID uuid.UUID, data interface{}) (func(context.Context), error) {
	if s == nil {
		return nil, fmt.Errorf("stage wallet gas status: webhook service is required")
	}
	if walletID == uuid.Nil {
		return nil, fmt.Errorf("stage wallet gas status: wallet is required")
	}
	if s.webhookConfigRepo == nil || s.webhookEventRepo == nil {
		return nil, fmt.Errorf("stage wallet gas status: webhook store is required")
	}
	msgs, err := s.stageLegacyEvent(ctx, nil, types.EventWalletGasStatusChanged, data)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	staged := append([]types.WebhookMessage(nil), msgs...)
	return func(sendCtx context.Context) {
		s.dispatchWebhooks(sendCtx, staged)
	}, nil
}

func (s *Service) stageLegacyEvent(ctx context.Context, txID *uuid.UUID, eventType types.EventType, data interface{}) ([]types.WebhookMessage, error) {
	payload, err := json.Marshal(map[string]interface{}{
		"id":         uuid.New().String(),
		"type":       string(eventType),
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data":       data,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal webhook payload: %w", err)
	}

	allConfigs, err := s.webhookConfigRepo.FindActive(ctx)
	if err != nil {
		return nil, fmt.Errorf("query webhook configs: %w", err)
	}

	var configs []models.WebhookConfig
	eventTypeStr := string(eventType)
	subjectWallet := legacyEventWallet(data)
	for _, cfg := range allConfigs {
		if containsEvent(cfg.Events, eventTypeStr) && legacyConfigCanSee(cfg, subjectWallet) {
			configs = append(configs, cfg)
		}
	}

	msgs := make([]types.WebhookMessage, 0, len(configs))
	for _, cfg := range configs {
		eventID := uuid.New().String()
		configID := cfg.ID
		messageTxID := ""
		if txID != nil {
			messageTxID = txID.String()
		}
		webhookEvent := &models.WebhookEvent{
			ID:              uuid.MustParse(eventID),
			TransactionID:   txID,
			WebhookConfigID: &configID,
			EventType:       string(eventType),
			Payload:         string(payload),
			DeliveryURL:     cfg.URL,
			DeliveryStatus:  "pending",
			Attempts:        0,
			MaxAttempts:     s.resolveDeliverySettings(ctx).MaxAttempts,
		}
		if err := s.webhookEventRepo.Create(ctx, webhookEvent); err != nil {
			return nil, fmt.Errorf("insert webhook event: %w", err)
		}
		msgs = append(msgs, types.WebhookMessage{
			EventID:       eventID,
			TransactionID: messageTxID,
			EventType:     eventType,
			Payload:       string(payload),
			DeliveryURL:   cfg.URL,
			Secret:        cfg.Secret,
			Attempt:       1,
		})
	}
	return msgs, nil
}

func (s *Service) dispatchWebhooks(ctx context.Context, msgs []types.WebhookMessage) {
	if s == nil || s.sqs == nil {
		return
	}
	for _, msg := range msgs {
		if err := s.sqs.SendWebhook(ctx, msg); err != nil {
			slog.Error("sqs send webhook", "error", err, "event_id", msg.EventID)
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

const (
	webhookDeliveryTimeout = 10 * time.Second
	// Test deliveries are synchronous dashboard calls, so they fail fast.
	webhookTestTimeout = 3 * time.Second
	webhookTestEvent   = "webhook.test"
)

// Deliver executes the HTTP delivery. Called by the SQS Lambda worker.
// Returns error to trigger SQS retry → eventually DLQ after 10 failures.
func (s *Service) Deliver(ctx context.Context, msg types.WebhookMessage) error {
	resp, err := postSignedWebhook(ctx, msg.DeliveryURL, msg.Secret, msg.Payload, string(msg.EventType), msg.EventID, s.resolveDeliverySettings(ctx).Timeout)
	if err != nil {
		if httpclient.IsBuild(err) {
			return fmt.Errorf("build request: %w", err)
		}
		s.markAttempt(ctx, msg.EventID, err.Error())
		return fmt.Errorf("http send: %w", err)
	}

	if resp.StatusCode >= httpclient.StatusBadRequest {
		errMsg := fmt.Sprintf("HTTP %d", resp.StatusCode)
		s.markAttempt(ctx, msg.EventID, errMsg)
		return fmt.Errorf("delivery failed: %s", errMsg)
	}

	s.webhookEventRepo.MarkDelivered(ctx, msg.EventID)

	slog.Info("webhook delivered", "event_id", msg.EventID, "url", msg.DeliveryURL)
	return nil
}

func (s *Service) markAttempt(ctx context.Context, eventID, errMsg string) {
	s.webhookEventRepo.IncrementAttempt(ctx, eventID, errMsg)
}

// SendTest posts one signed webhook.test body to the config URL and does not
// retry or persist a delivery. The signing secret is never written to logs.
func (s *Service) SendTest(ctx context.Context, cfg *models.WebhookConfig, walletID uuid.UUID) error {
	if cfg == nil || strings.TrimSpace(cfg.URL) == "" {
		return ErrWebhookConfigNotFound
	}
	eventID := uuid.New().String()
	payload, err := json.Marshal(map[string]any{
		"id":         eventID,
		"type":       webhookTestEvent,
		"created_at": time.Now().UTC().Format(time.RFC3339),
		"data": map[string]string{
			"wallet_id":  walletID.String(),
			"webhook_id": cfg.ID.String(),
		},
	})
	if err != nil {
		return fmt.Errorf("marshal webhook test: %w", err)
	}

	resp, err := postSignedWebhook(ctx, cfg.URL, cfg.Secret, string(payload), webhookTestEvent, eventID, webhookTestTimeout)
	if err != nil {
		slog.Info("webhook test delivery failed", "webhook_id", cfg.ID.String(), "url", cfg.URL)
		return fmt.Errorf("webhook test delivery failed: %w", err)
	}
	if resp.StatusCode >= httpclient.StatusBadRequest {
		slog.Info("webhook test delivery rejected", "webhook_id", cfg.ID.String(), "url", cfg.URL, "status", resp.StatusCode)
		return fmt.Errorf("webhook test delivery failed: HTTP %d", resp.StatusCode)
	}
	slog.Info("webhook test delivered", "webhook_id", cfg.ID.String(), "url", cfg.URL)
	return nil
}

func postSignedWebhook(ctx context.Context, deliveryURL, secret, payload, eventType, eventID string, timeout time.Duration) (httpclient.Response, error) {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	signature := hex.EncodeToString(mac.Sum(nil))
	client := NewDeliveryClient()
	if client == nil {
		return httpclient.Response{}, fmt.Errorf("webhook delivery client is not configured")
	}
	return client.Post(ctx, SignedDelivery{
		URL:        deliveryURL,
		Body:       []byte(payload),
		Signature:  signature,
		EventType:  eventType,
		DeliveryID: eventID,
		Timeout:    timeout,
	})
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
	if err := s.webhookConfigRepo.Create(ctx, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) ListConfigs(ctx context.Context) ([]models.WebhookConfig, error) {
	return s.webhookConfigRepo.FindAll(ctx)
}

// ListAccountConfigs returns the account-level configs an account may manage: its own
// plus legacy unowned ones it can claim.
func (s *Service) ListAccountConfigs(ctx context.Context, accountID uuid.UUID) ([]models.WebhookConfig, error) {
	if accountID == uuid.Nil {
		return nil, errors.New("account id is required")
	}
	return s.webhookConfigRepo.FindVisibleToAccount(ctx, accountID)
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

	cfg, err := s.webhookConfigRepo.FindByID(ctx, configID)
	if errors.Is(err, models.ErrRepositoryNotFound) {
		cfg, err = nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find webhook config: %w", err)
	}
	if cfg == nil || cfg.WalletID != nil || (cfg.AccountID != nil && *cfg.AccountID != accountID) {
		return nil, ErrWebhookConfigNotFound
	}
	if cfg.AccountID == nil && !hmac.Equal([]byte(update.Secret), []byte(cfg.Secret)) {
		return nil, ErrWebhookOwnershipNotProven
	}

	var events *string
	if update.Events != nil {
		normalized, err := normalizeSubscribableEvents(update.Events)
		if err != nil {
			return nil, err
		}
		encoded := pgArray(normalized)
		events = &encoded
		cfg.Events = encoded
	}
	if update.IsActive != nil {
		cfg.IsActive = *update.IsActive
	}
	if err := s.webhookConfigRepo.AssignAccount(ctx, cfg.ID, accountID, events, update.IsActive); err != nil {
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
	return s.webhookConfigRepo.DeleteByID(ctx, id)
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
