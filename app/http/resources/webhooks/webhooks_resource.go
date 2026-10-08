package webhooks

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WebhookConfig is the webhook row HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps. The signing secret stays
// off the wire. A nil page stays nil; an empty page stays empty. A nil config
// stays null.
type WebhookConfig struct {
	CreatedAt *carbon.DateTime `json:"created_at"`
	UpdatedAt *carbon.DateTime `json:"updated_at"`
	ID        uuid.UUID        `json:"id"`
	URL       string           `json:"url"`
	Events    string           `json:"events"`
	IsActive  bool             `json:"is_active"`
	WalletID  *uuid.UUID       `json:"wallet_id,omitempty"`
	AccountID *uuid.UUID       `json:"account_id,omitempty"`
	Type      string           `json:"type,omitempty"`
}

// WebhookConfigFrom projects one webhook config. The signing secret stays off the wire.
func WebhookConfigFrom(cfg models.WebhookConfig) WebhookConfig {
	return WebhookConfig{
		CreatedAt: cfg.CreatedAt,
		UpdatedAt: cfg.UpdatedAt,
		ID:        cfg.ID,
		URL:       cfg.URL,
		Events:    cfg.Events,
		IsActive:  cfg.IsActive,
		WalletID:  cfg.WalletID,
		AccountID: cfg.AccountID,
		Type:      cfg.Type,
	}
}

// WebhookConfigsFrom copies a page. A nil slice stays nil; an empty slice stays empty.
func WebhookConfigsFrom(configs []models.WebhookConfig) []WebhookConfig {
	if configs == nil {
		return nil
	}
	views := make([]WebhookConfig, len(configs))
	for i := range configs {
		views[i] = WebhookConfigFrom(configs[i])
	}
	return views
}

// WebhookConfigPtr keeps a nil config as JSON null.
func WebhookConfigPtr(cfg *models.WebhookConfig) *WebhookConfig {
	if cfg == nil {
		return nil
	}
	view := WebhookConfigFrom(*cfg)
	return &view
}
