package webhooks

import (
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
)

// optionalString accepts any present string and skips an absent one.
const optionalString = "string"

// UpdateRequest changes the subscribed events or the active flag of an
// account-level webhook. Secret is only needed to claim a legacy unowned webhook.
// Rules type-check those fields. A blank secret is not required and keeps the
// stored value. Empty updates and unknown events stay in the webhook service
// so its 400 messages stay put.
type UpdateRequest struct {
	requests.Open
	Events   []string `form:"events"    json:"events,omitempty"    example:"deposit.confirmed,withdrawal.broadcast,withdrawal.confirmed,withdrawal.failed"`
	IsActive *bool    `form:"is_active" json:"is_active,omitempty" example:"true"`
	Secret   string   `form:"secret"    json:"secret,omitempty"    example:"my-webhook-secret"`
}

func (r *UpdateRequest) Rules(http.Context) map[string]string {
	return map[string]string{
		"events":    "array",
		"is_active": "bool",
		"secret":    optionalString,
	}
}
