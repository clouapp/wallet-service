package webhook

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/pkg/egress"
	"github.com/macrowallets/waas/pkg/types"
)

// destinationGuard refuses a webhook URL whose destination is not public: its
// scheme, its host, and what a host name resolves to now. *egress.Guard is one.
type destinationGuard interface {
	CheckDestination(ctx context.Context, rawURL string) error
}

// refusedPrefix starts the last_error of an event whose destination was refused.
const refusedPrefix = "refused: "

// The scope of a webhook config in an audit.
const (
	ScopeWallet  = "wallet"
	ScopeAccount = "account"
	ScopeLegacy  = "legacy"
)

// CheckURL is the one rule for a webhook URL, applied wherever one is registered or
// kept active (the dashboard wallet webhooks, the account webhooks of the external
// API, a legacy webhook claimed): https to a public host whose name resolves only
// to public addresses. A refusal wraps ErrURLNotAllowed and the guard's reason.
// The delivery client applies the same guard again to every connection.
func (s *Service) CheckURL(ctx context.Context, rawURL string) error {
	if err := s.destinations.CheckDestination(ctx, rawURL); err != nil {
		return fmt.Errorf("%w: %w", ErrURLNotAllowed, err)
	}
	return nil
}

// RefusalReason is the rule a refused webhook URL broke, a fixed sentence that never
// carries the URL; ErrURLNotAllowed's text when err names none.
func RefusalReason(err error) string {
	var refusal *egress.Refusal
	if errors.As(err, &refusal) {
		return refusal.Reason
	}
	return ErrURLNotAllowed.Error()
}

// refuseDelivery settles an event the delivery client refused to post because its
// destination is not allowed (a config stored before the guard, or a name that now
// resolves inside): marked failed with the reason, never posted, never retried, and
// logged with the host only.
func (s *Service) refuseDelivery(ctx context.Context, msg types.WebhookMessage, err error) error {
	reason := RefusalReason(err)
	if markErr := s.webhookEventRepo.MarkFailed(ctx, msg.EventID, refusedPrefix+reason); markErr != nil {
		return markErr
	}
	slog.Warn("webhook not sent: its destination is refused", "event_id", msg.EventID, "config_id", msg.ConfigID,
		"host", egress.Host(msg.DeliveryURL), "reason", reason)
	return nil
}

// DestinationAudit counts the active webhook configs and lists those whose URL
// CheckURL refuses now: their deliveries are refused and never sent.
type DestinationAudit struct {
	Active  int
	Refused []RefusedDestination
}

// RefusedDestination is one active config whose URL is refused, named by its host
// only.
type RefusedDestination struct {
	ConfigID uuid.UUID
	Scope    string
	Host     string
	Reason   string
}

// AuditDestinations checks the URL of every active config. It reads and writes
// nothing else.
func (s *Service) AuditDestinations(ctx context.Context) (DestinationAudit, error) {
	configs, err := s.webhookConfigRepo.FindActive(ctx)
	if err != nil {
		return DestinationAudit{}, fmt.Errorf("list active webhook configs: %w", err)
	}
	audit := DestinationAudit{Active: len(configs)}
	for _, cfg := range configs {
		checkErr := s.CheckURL(ctx, cfg.URL)
		if checkErr == nil {
			continue
		}
		scope := ScopeLegacy
		switch {
		case cfg.WalletID != nil:
			scope = ScopeWallet
		case cfg.AccountID != nil:
			scope = ScopeAccount
		}
		audit.Refused = append(audit.Refused, RefusedDestination{
			ConfigID: cfg.ID, Scope: scope, Host: egress.Host(cfg.URL), Reason: RefusalReason(checkErr),
		})
	}
	return audit, nil
}
