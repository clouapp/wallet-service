package webhooks

import (
	"github.com/goravel/framework/contracts/http"
)

// StoreRequest is the body of POST /api/v1/webhooks.
type StoreRequest struct {
	URL    string   `form:"url"    json:"url"    example:"https://example.com/webhook"`
	Secret string   `form:"secret" json:"secret" example:"my-webhook-secret"`
	Events []string `form:"events" json:"events" example:"deposit.confirmed,withdrawal.broadcast,withdrawal.confirmed,withdrawal.failed"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"url":    "required|full_url",
		"secret": "required",
		"events": "required|array",
	}
}
