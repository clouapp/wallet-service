package webhooks

import (
	"github.com/goravel/framework/contracts/http"
)

// StoreRequest is the body of a wallet webhook: the URL to call and, optionally,
// the signing secret and the events it subscribes to.
type StoreRequest struct {
	URL    string `form:"url"    json:"url"`
	Secret string `form:"secret" json:"secret,omitempty"`
	Events string `form:"events" json:"events,omitempty"`
}

func (r *StoreRequest) Authorize(ctx http.Context) error {
	return nil
}

func (r *StoreRequest) Rules(ctx http.Context) map[string]string {
	return map[string]string{
		"url": "required|full_url",
	}
}
