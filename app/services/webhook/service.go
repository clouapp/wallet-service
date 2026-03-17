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
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/macromarkets/vault/app/models"
	"github.com/macromarkets/vault/app/services/queue"
	"github.com/macromarkets/vault/pkg/types"
)

// ---------------------------------------------------------------------------
// Service — webhook management.
// EnqueueEvent sends to SQS, Deliver is called by the Lambda worker.
// ---------------------------------------------------------------------------

type Service struct {
	db  *sqlx.DB
	sqs queue.Sender
}

func NewService(db *sqlx.DB, sqs queue.Sender) *Service {
	return &Service{db: db, sqs: sqs}
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
	var configs []models.WebhookConfig
	if err := s.db.SelectContext(ctx, &configs, `
		SELECT * FROM webhook_configs WHERE is_active = true AND $1 = ANY(events)
	`, string(eventType)); err != nil {
		slog.Error("query webhook configs", "error", err)
		return
	}

	for _, cfg := range configs {
		eventID := uuid.New().String()

		// Persist webhook event for auditing
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO webhook_events (id, transaction_id, event_type, payload, delivery_url, delivery_status, attempts, max_attempts, created_at)
			VALUES ($1, $2, $3, $4, $5, 'pending', 0, 10, $6)
		`, eventID, txID, string(eventType), string(payload), cfg.URL, time.Now().UTC()); err != nil {
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
	s.db.ExecContext(ctx, `
		UPDATE webhook_events SET delivery_status = 'delivered', delivered_at = $1, attempts = attempts + 1
		WHERE id = $2`, time.Now().UTC(), msg.EventID)

	slog.Info("webhook delivered", "event_id", msg.EventID, "url", msg.DeliveryURL)
	return nil
}

func (s *Service) markAttempt(ctx context.Context, eventID, errMsg string) {
	s.db.ExecContext(ctx, `
		UPDATE webhook_events SET attempts = attempts + 1, last_error = $1 WHERE id = $2`, errMsg, eventID)
}

// ---------------------------------------------------------------------------
// Webhook config CRUD
// ---------------------------------------------------------------------------

func (s *Service) CreateConfig(ctx context.Context, url, secret string, events []string) (*models.WebhookConfig, error) {
	cfg := &models.WebhookConfig{
		ID: uuid.New(), URL: url, Secret: secret,
		Events: pgArray(events), IsActive: true, CreatedAt: time.Now().UTC(),
	}
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO webhook_configs (id, url, secret, events, is_active, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		cfg.ID, cfg.URL, cfg.Secret, cfg.Events, cfg.IsActive, cfg.CreatedAt); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (s *Service) ListConfigs(ctx context.Context) ([]models.WebhookConfig, error) {
	var configs []models.WebhookConfig
	return configs, s.db.SelectContext(ctx, &configs, "SELECT * FROM webhook_configs ORDER BY created_at")
}

func (s *Service) DeleteConfig(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM webhook_configs WHERE id = $1", id)
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
