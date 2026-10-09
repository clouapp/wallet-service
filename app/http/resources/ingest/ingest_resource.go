// Package ingest shapes the answer of the inbound provider webhook route.
package ingest

// Accepted is the answer of an ingested webhook.
type Accepted struct {
	OK bool `json:"ok" example:"true"`
}

// NewAccepted marks the webhook as ingested.
func NewAccepted() Accepted {
	return Accepted{OK: true}
}
