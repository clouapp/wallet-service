package webhook

import (
	"context"
	"log/slog"
	"time"
)

// DeliverySettings is the attempt limit and the HTTP timeout for one delivery.
// MaxAttempts and Timeout are the values delivery uses. A non-positive field
// is missing and the code default stands in its place.
type DeliverySettings struct {
	MaxAttempts int
	Timeout     time.Duration
}

// deliverySettingsSource reads webhook_delivery at the moment of use.
// A non-nil error means the read failed. Delivery keeps the code defaults
// so a settings outage does not stop the send.
type deliverySettingsSource func(ctx context.Context) (DeliverySettings, error)

// SetDeliverySettingsSource installs the per-delivery reader. Nil keeps the
// code defaults (10 attempts, 10 seconds).
func (s *Service) SetDeliverySettingsSource(source deliverySettingsSource) {
	if s == nil {
		return
	}
	s.deliverySettings = source
}

// DefaultDeliverySettings is the webhook retry constant S1.4.4 moves into
// settings: 10 attempts and a 10 second timeout.
func DefaultDeliverySettings() DeliverySettings {
	return DeliverySettings{MaxAttempts: defaultMaxAttempts, Timeout: webhookDeliveryTimeout}
}

// DeliverySettingsFromStored overlays a webhook_delivery row on the code
// defaults. A non-positive field is missing or invalid and leaves that part
// of the default in place.
func DeliverySettingsFromStored(maxAttempts, timeoutSeconds int) DeliverySettings {
	out := DefaultDeliverySettings()
	if maxAttempts > 0 {
		out.MaxAttempts = maxAttempts
	}
	if timeoutSeconds > 0 {
		out.Timeout = time.Duration(timeoutSeconds) * time.Second
	}
	return out
}

// resolveDeliverySettings reads the platform group for this send. A missing
// source, a failed read, or a non-positive field keeps the code default.
// The log names the failure, never a URL, a secret, or a stored value.
func (s *Service) resolveDeliverySettings(ctx context.Context) DeliverySettings {
	fallback := DefaultDeliverySettings()
	if s == nil || s.deliverySettings == nil {
		return fallback
	}
	got, err := s.deliverySettings(ctx)
	if err != nil {
		slog.Warn("webhook delivery settings unread; keeping the defaults", "error", err)
		return fallback
	}
	if got.MaxAttempts <= 0 {
		got.MaxAttempts = fallback.MaxAttempts
	}
	if got.Timeout <= 0 {
		got.Timeout = fallback.Timeout
	}
	return got
}
