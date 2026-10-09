package webhooks

import "github.com/macrowallets/waas/app/models"

// ConfigList is the answer of the account webhook list: the configs under
// data. A nil page stays null; an empty page stays [].
type ConfigList struct {
	Data []WebhookConfig `json:"data"`
}

// NewConfigList projects the webhook configs of an account.
func NewConfigList(configs []models.WebhookConfig) ConfigList {
	return ConfigList{Data: WebhookConfigsFrom(configs)}
}
