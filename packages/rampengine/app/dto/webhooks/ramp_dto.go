// Package webhooks contains DTOs for the provider webhook endpoints, which sit
// outside both API audiences (app/http/controllers/webhooks).
package webhooks

// ReceiveWebhookDTO is one inbound delivery exactly as it arrived. Body is
// the raw bytes the provider signed, which is why it is never decoded first.
type ReceiveWebhookDTO struct {
	Provider string
	Headers  map[string]string
	Body     []byte
} //	@name	RampReceiveWebhookDTO
