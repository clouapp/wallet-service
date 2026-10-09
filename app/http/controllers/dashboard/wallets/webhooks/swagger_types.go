package webhooks

// Swagger request and response types of the wallet webhook routes (doc-only).

type WebhookTestResponse struct {
	Delivered bool `json:"delivered" example:"true"`
}

type CreateWalletWebhookSwagger struct {
	URL    string `json:"url" example:"https://example.com/hook"`
	Secret string `json:"secret,omitempty" example:"wh_secret_123"`
	Events string `json:"events,omitempty" example:"deposit.confirmed,withdrawal.confirmed"`
}
