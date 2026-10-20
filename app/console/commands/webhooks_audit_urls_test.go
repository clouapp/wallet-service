package commands

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	mocksconsole "github.com/goravel/framework/mocks/console"
	"github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/app/services/webhook"
	"github.com/macrowallets/waas/pkg/egress"
)

type fakeDestinationAuditor struct {
	calls int
	audit webhook.DestinationAudit
	err   error
}

func (f *fakeDestinationAuditor) AuditDestinations(context.Context) (webhook.DestinationAudit, error) {
	f.calls++
	return f.audit, f.err
}

func TestWebhooks_AuditURLs_SignatureAndCategory(t *testing.T) {
	cmd := NewWebhooksAuditURLs(&fakeDestinationAuditor{})
	ext := cmd.Extend()
	if cmd.Signature() != "webhooks:audit-urls" || ext.Category != "webhook" || len(ext.Arguments) != 0 || len(ext.Flags) != 0 {
		t.Fatalf("signature %q category %q arguments %d flags %d", cmd.Signature(), ext.Category, len(ext.Arguments), len(ext.Flags))
	}
}

func TestWebhooks_AuditURLs_ConstructorRequiresTheService(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("constructor accepted a missing service")
		}
	}()
	NewWebhooksAuditURLs(nil)
}

func TestWebhooks_AuditURLs_ListsTheRefusedConfigsByHostAndExitsOne(t *testing.T) {
	auditor := &fakeDestinationAuditor{audit: webhook.DestinationAudit{Active: 46, Refused: []webhook.RefusedDestination{
		{ConfigID: uuid.MustParse("0c6f3e1a-2b4d-4f5a-8c7e-9d1b2a3c4e5f"), Scope: webhook.ScopeWallet, Host: "169.254.169.254", Reason: egress.ReasonLinkLocal},
		{ConfigID: uuid.MustParse("7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"), Scope: webhook.ScopeLegacy, Host: "openbao", Reason: egress.ReasonHostName},
	}}}
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Warning("refused wallet webhook 0c6f3e1a-2b4d-4f5a-8c7e-9d1b2a3c4e5f host 169.254.169.254: " + egress.ReasonLinkLocal).Return()
	ctx.EXPECT().Warning("refused legacy webhook 7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d host openbao: " + egress.ReasonHostName).Return()
	ctx.EXPECT().Error(mock.MatchedBy(func(line string) bool {
		return strings.Contains(line, "2 of 46 active webhook configs") && strings.Contains(line, "never sent")
	})).Return()

	err := NewWebhooksAuditURLs(auditor).Handle(ctx)
	var status interface{ ExitCode() int }
	if !errors.As(err, &status) || status.ExitCode() != 1 {
		t.Fatalf("err = %v, want exit status 1", err)
	}
	if auditor.calls != 1 {
		t.Fatalf("calls = %d", auditor.calls)
	}
}

func TestWebhooks_AuditURLs_ReportsAClearRun(t *testing.T) {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Info("0 of 12 active webhook configs name a refused destination").Return()

	if err := NewWebhooksAuditURLs(&fakeDestinationAuditor{audit: webhook.DestinationAudit{Active: 12}}).Handle(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestWebhooks_AuditURLs_FailsWhenTheConfigsCannotBeRead(t *testing.T) {
	ctx := mocksconsole.NewContext(t)
	ctx.EXPECT().Error(mock.MatchedBy(func(line string) bool { return strings.Contains(line, "list active webhook configs") })).Return()

	err := NewWebhooksAuditURLs(&fakeDestinationAuditor{err: errors.New("list active webhook configs: connection refused")}).Handle(ctx)
	if err == nil {
		t.Fatal("a failed read exited 0")
	}
}
