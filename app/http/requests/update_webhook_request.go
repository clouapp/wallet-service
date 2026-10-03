package requests

import "github.com/goravel/framework/contracts/http"

// UpdateWebhookRequest changes the subscribed events or the active flag of an
// account-level webhook. Secret is only needed to claim a legacy unowned webhook.
// Field checks stay in the webhook service so its 400 messages stay put.
type UpdateWebhookRequest struct {
	Open
	Events   []string `form:"events" json:"events,omitempty"`
	IsActive *bool    `form:"is_active" json:"is_active,omitempty"`
	Secret   string   `form:"secret" json:"secret,omitempty"`
}

func (r *UpdateWebhookRequest) Rules(http.Context) map[string]string {
	return map[string]string{}
}
