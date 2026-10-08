package requests

import "github.com/goravel/framework/contracts/http"

// optionalString accepts any present string and skips an absent one.
const optionalString = "string"

// UpdateWebhookRequest changes the subscribed events or the active flag of an
// account-level webhook. Secret is only needed to claim a legacy unowned webhook.
// Rules type-check those fields. A blank secret is not required and keeps the
// stored value. Empty updates and unknown events stay in the webhook service
// so its 400 messages stay put.
type UpdateWebhookRequest struct {
	Open
	Events   []string `form:"events" json:"events,omitempty"`
	IsActive *bool    `form:"is_active" json:"is_active,omitempty"`
	Secret   string   `form:"secret" json:"secret,omitempty"`
}

func (r *UpdateWebhookRequest) Rules(http.Context) map[string]string {
	return map[string]string{
		"events":    "array",
		"is_active": "bool",
		"secret":    optionalString,
	}
}
