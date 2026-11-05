// Package webhooks contains DTOs for the admin webhook-event explorer.
package webhooks

import "time"

// AdminListWebhookEventsDTO carries validated filter parameters. From and To
// are parsed in the controller from the query string before the DTO is built.
type AdminListWebhookEventsDTO struct {
	Page          int        `json:"page"`
	PerPage       int        `json:"per_page"`
	Provider      string     `json:"provider"`
	EventType     string     `json:"event_type"`
	Processed     string     `json:"processed"`
	TransactionID uint       `json:"transaction_id"`
	ExternalRef   string     `json:"external_ref"`
	From          *time.Time `json:"from,omitempty"`
	To            *time.Time `json:"to,omitempty"`
	Sort          string     `json:"sort"`
} //	@name	AdminListWebhookEventsDTO
