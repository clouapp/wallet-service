package ramp

import (
	"strings"
	"time"
)

// HeaderValue looks a delivery header up case-insensitively: the webhook
// controller hands over the request's headers in Go's canonical form, so a
// provider's "X-OZAV-Signature" arrives as "X-Ozav-Signature".
func HeaderValue(headers map[string]string, name string) string {
	if value, ok := headers[name]; ok {
		return value
	}
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

// ParseTime reads an RFC 3339 timestamp out of a provider payload, or nil when
// it is absent or unreadable: an event with no usable time is still an event,
// and nil keeps "not stated" apart from the epoch.
func ParseTime(value string) *time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil
	}
	return &parsed
}
