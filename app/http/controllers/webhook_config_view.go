package controllers

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

// WebhookConfigView is the webhook row HTTP clients read. Field order and tags
// match the model wire, including embedded timestamps. The signing secret stays
// off the wire. A nil page stays nil; an empty page stays empty. A nil config
// stays null.
type WebhookConfigView struct {
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

func newWebhookConfigView(cfg models.WebhookConfig) WebhookConfigView {
	return WebhookConfigView{
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

// WebhookConfigViews copies a page. A nil slice stays nil; an empty slice stays empty.
func WebhookConfigViews(configs []models.WebhookConfig) []WebhookConfigView {
	if configs == nil {
		return nil
	}
	views := make([]WebhookConfigView, len(configs))
	for i := range configs {
		views[i] = newWebhookConfigView(configs[i])
	}
	return views
}

// WebhookConfigViewPtr keeps a nil config as JSON null.
func WebhookConfigViewPtr(cfg *models.WebhookConfig) *WebhookConfigView {
	if cfg == nil {
		return nil
	}
	view := newWebhookConfigView(*cfg)
	return &view
}
