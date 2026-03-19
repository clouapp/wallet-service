package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateWebhookRequest struct {
	URL    string   `form:"url"    json:"url"`
	Secret string   `form:"secret" json:"secret"`
	Events []string `form:"events" json:"events"`
}

func (r *CreateWebhookRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWebhookRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"url":    "required|full_url",
		"secret": "required",
		"events": "required|array",
	}
}
