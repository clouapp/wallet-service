package commands

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/console"
	"github.com/goravel/framework/contracts/console/command"

	"github.com/macrowallets/waas/app/services/webhook"
)

// destinationAuditor checks the URL of every active webhook config;
// *webhook.Service is the one implementation.
type destinationAuditor interface {
	AuditDestinations(ctx context.Context) (webhook.DestinationAudit, error)
}

// WebhooksAuditURLs lists the active webhook configs whose URL the outbound guard
// refuses (stored before it existed, or a name that now resolves inside): their
// deliveries are refused and never sent. Read-only; a config is named by its id,
// scope and host, never its URL. It exits 1 when one is refused.
type WebhooksAuditURLs struct {
	webhooks destinationAuditor
}

// NewWebhooksAuditURLs wires the command; the service is required.
func NewWebhooksAuditURLs(webhooks destinationAuditor) *WebhooksAuditURLs {
	if webhooks == nil {
		panic("webhooks:audit-urls: webhook service is required")
	}
	return &WebhooksAuditURLs{webhooks: webhooks}
}

func (c *WebhooksAuditURLs) Signature() string {
	return "webhooks:audit-urls"
}

func (c *WebhooksAuditURLs) Description() string {
	return "Read-only: list the active webhook configs whose URL the outbound guard refuses (their deliveries are never sent), by id, scope and host"
}

func (c *WebhooksAuditURLs) Extend() command.Extend {
	return command.Extend{Category: "webhook"}
}

func (c *WebhooksAuditURLs) Handle(ctx console.Context) error {
	audit, err := c.webhooks.AuditDestinations(context.Background())
	if err != nil {
		return fail(ctx, err)
	}
	for _, refused := range audit.Refused {
		ctx.Warning(fmt.Sprintf("refused %s webhook %s host %s: %s", refused.Scope, refused.ConfigID, refused.Host, refused.Reason))
	}
	summary := fmt.Sprintf("%d of %d active webhook configs name a refused destination", len(audit.Refused), audit.Active)
	if len(audit.Refused) == 0 {
		ctx.Info(summary)
		return nil
	}
	return fail(ctx, exitStatus{
		err:  fmt.Errorf("%s: their deliveries are never sent; deactivate them and register a public https URL instead", summary),
		code: exitMismatch,
	})
}
