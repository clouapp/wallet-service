package requests

import (
	"github.com/goravel/framework/contracts/http"
)

type CreateWalletWebhookRequest struct {
	URL    string `form:"url"    json:"url"`
	Secret string `form:"secret" json:"secret,omitempty"`
	Events string `form:"events" json:"events,omitempty"`
}

func (r *CreateWalletWebhookRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *CreateWalletWebhookRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"url": "required|full_url",
	}
}
