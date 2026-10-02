package requests

// UpdateWebhookRequest changes the subscribed events or the active flag of an
// account-level webhook. Secret is only needed to claim a legacy unowned webhook.
type UpdateWebhookRequest struct {
	Events   []string `json:"events,omitempty"`
	IsActive *bool    `json:"is_active,omitempty"`
	Secret   string   `json:"secret,omitempty"`
}
