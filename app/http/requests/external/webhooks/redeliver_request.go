package webhooks

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/http"

	"github.com/macrowallets/waas/app/http/requests"
	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/types"
)

// RedeliverRequest is the body of POST /api/v1/webhooks/{webhookId}/redeliver:
// event_ids, or since with an optional until and types. The rules check the
// shapes; After checks that exactly one selection is sent, its size and its event
// types, and turns it into the service's selection. The window (seven days, until
// after since) is the webhook service's rule.
type RedeliverRequest struct {
	requests.Open
	EventIDs []string `form:"event_ids" json:"event_ids,omitempty" example:"3f2b8c1e-0d4a-4f6b-9e2a-7c5d1b8e9f00"`
	Since    string   `form:"since"     json:"since,omitempty"     example:"2026-10-10T00:00:00Z"`
	Until    string   `form:"until"     json:"until,omitempty"     example:"2026-10-11T00:00:00Z"`
	Types    []string `form:"types"     json:"types,omitempty"     example:"deposit.confirmed"`

	selection webhook.RedeliverySelection
}

func (r *RedeliverRequest) Rules(http.Context) map[string]string {
	return map[string]string{
		"event_ids":   "array",
		"event_ids.*": "uuid",
		"since":       "rfc3339",
		"until":       "rfc3339",
		"types":       "array",
	}
}

// After runs once the rules passed, so the ids are UUIDs and the timestamps
// RFC 3339; the first selection problem is the only field answered.
func (r *RedeliverRequest) After(http.Context) map[string][]string {
	byID := len(r.EventIDs) > 0
	switch {
	case !byID && r.Since == "":
		return map[string][]string{"event_ids": {"event_ids or since is required"}}
	case byID && (r.Since != "" || r.Until != "" || len(r.Types) > 0):
		return map[string][]string{"event_ids": {"event_ids cannot be combined with since, until or types"}}
	case len(r.EventIDs) > webhook.MaxRedeliveryEvents:
		return map[string][]string{"event_ids": {fmt.Sprintf("at most %d event ids per redelivery", webhook.MaxRedeliveryEvents)}}
	}
	for _, eventType := range r.Types {
		if !types.IsSubscribableEvent(eventType) {
			return map[string][]string{"types": {fmt.Sprintf("unknown webhook event %q", eventType)}}
		}
	}
	return r.parse()
}

func (r *RedeliverRequest) parse() map[string][]string {
	selection := webhook.RedeliverySelection{Types: r.Types}
	for _, raw := range r.EventIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return map[string][]string{"event_ids": {"every event id must be a UUID"}}
		}
		selection.EventIDs = append(selection.EventIDs, id)
	}
	var err error
	if selection.Since, err = parseTimestamp(r.Since); err != nil {
		return map[string][]string{"since": {"must be an RFC 3339 timestamp"}}
	}
	if selection.Until, err = parseTimestamp(r.Until); err != nil {
		return map[string][]string{"until": {"must be an RFC 3339 timestamp"}}
	}
	r.selection = selection
	return nil
}

// parseTimestamp reads an optional RFC 3339 timestamp in UTC; blank is zero.
func parseTimestamp(raw string) (time.Time, error) {
	if raw == "" {
		return time.Time{}, nil
	}
	at, err := time.Parse(time.RFC3339, raw)
	return at.UTC(), err
}

// Selection is the request as the webhook service reads it; it is set by After.
func (r *RedeliverRequest) Selection() webhook.RedeliverySelection {
	return r.selection
}
