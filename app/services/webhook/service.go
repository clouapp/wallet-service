package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/pkg/types"
)

// ---------------------------------------------------------------------------
// Service — webhook management.
// EnqueueEvent sends to SQS, Deliver is called by the Lambda worker.
// ---------------------------------------------------------------------------

type Service struct {
	sqs queue.Sender
}

func NewService(sqs queue.Sender) *Service {
	return &Service{sqs: sqs}
}

// EnqueueEvent creates webhook events for all matching configs and sends to SQS.
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

	// Find all active webhook configs subscribed to this event
	var allConfigs []models.WebhookConfig
	if err := facades.Orm().Query().Where("is_active", true).Find(&allConfigs); err != nil {
		slog.Error("query webhook configs", "error", err)
		return
	}

	// Filter configs that are subscribed to this event type
	var configs []models.WebhookConfig
	eventTypeStr := string(eventType)
	for _, cfg := range allConfigs {
		// Check if the event type is in the Events field (postgres array stored as string)
		// The pgArray function formats as {"event1","event2"}
		if containsEvent(cfg.Events, eventTypeStr) {
			configs = append(configs, cfg)
		}
	}

	for _, cfg := range configs {
		eventID := uuid.New().String()

		// Persist webhook event for auditing
		webhookEvent := &models.WebhookEvent{
			ID:             uuid.MustParse(eventID),
			TransactionID:  &txID,
			EventType:      string(eventType),
			Payload:        string(payload),
			DeliveryURL:    cfg.URL,
			DeliveryStatus: "pending",
			Attempts:       0,
			MaxAttempts:    10,
		}
		if err := facades.Orm().Query().Create(webhookEvent); err != nil {
			slog.Error("insert webhook event", "error", err)
			continue
		}

		// Send to SQS for async delivery
		msg := types.WebhookMessage{
			EventID:       eventID,
			TransactionID: txID.String(),
			EventType:     eventType,
			Payload:       string(payload),
			DeliveryURL:   cfg.URL,
			Secret:        cfg.Secret,
			Attempt:       1,
		}
		if err := s.sqs.SendWebhook(ctx, msg); err != nil {
			slog.Error("sqs send webhook", "error", err, "event_id", eventID)
		}
	}
}

// Deliver executes the HTTP delivery. Called by the SQS Lambda worker.
// Returns error to trigger SQS retry → eventually DLQ after 10 failures.
func (s *Service) Deliver(ctx context.Context, msg types.WebhookMessage) error {
	// Compute HMAC-SHA256 signature
	mac := hmac.New(sha256.New, []byte(msg.Secret))
	mac.Write([]byte(msg.Payload))
	signature := hex.EncodeToString(mac.Sum(nil))

	// Build HTTP request
	req, err := http.NewRequestWithContext(ctx, "POST", msg.DeliveryURL, bytes.NewReader([]byte(msg.Payload)))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Vault-Signature", signature)
	req.Header.Set("X-Vault-Event", string(msg.EventType))
	req.Header.Set("X-Vault-Delivery-Id", msg.EventID)
	req.Header.Set("X-Vault-Timestamp", fmt.Sprintf("%d", time.Now().Unix()))

	// Send with timeout
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

	// Success — mark delivered
	now := time.Now().UTC()
	facades.Orm().Query().Exec("UPDATE webhook_events SET delivery_status = 'delivered', delivered_at = ?, attempts = attempts + 1 WHERE id = ?", now, msg.EventID)

	slog.Info("webhook delivered", "event_id", msg.EventID, "url", msg.DeliveryURL)
	return nil
}

func (s *Service) markAttempt(ctx context.Context, eventID, errMsg string) {
	facades.Orm().Query().Exec("UPDATE webhook_events SET attempts = attempts + 1, last_error = ? WHERE id = ?", errMsg, eventID)
}

// ---------------------------------------------------------------------------
// Webhook config CRUD
// ---------------------------------------------------------------------------

func (s *Service) CreateConfig(ctx context.Context, url, secret string, events []string) (*models.WebhookConfig, error) {
	cfg := &models.WebhookConfig{
		ID:       uuid.New(),
		URL:      url,
		Secret:   secret,
		Events:   pgArray(events),
		IsActive: true,
		// CreatedAt handled by orm.Model
	}
	if err := facades.Orm().Query().Create(cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) ListConfigs(ctx context.Context) ([]models.WebhookConfig, error) {
	var configs []models.WebhookConfig
	if err := facades.Orm().Query().Order("created_at").Find(&configs); err != nil {
		return nil, err
	}
	return configs, nil
}

func (s *Service) DeleteConfig(ctx context.Context, id uuid.UUID) error {
	_, err := facades.Orm().Query().Delete(&models.WebhookConfig{}, id)
	return err
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
	// Simple string contains check for the quoted event type
	return strings.Contains(eventsStr, `"`+eventType+`"`)
}
