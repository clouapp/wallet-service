package webhook

import (
	"github.com/google/uuid"
	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/pkg/types"
	"testing"
)

func TestRampEvents_OnlyExplicitAccountSubscriptionCanSeeThem(t *testing.T) {
	a, b, w := uuid.New(), uuid.New(), uuid.New()
	for _, test := range []struct {
		name   string
		config models.WebhookConfig
		want   bool
	}{{"matching", models.WebhookConfig{AccountID: &a}, true}, {"other", models.WebhookConfig{AccountID: &b}, false}, {"legacy", models.WebhookConfig{}, false}, {"wallet", models.WebhookConfig{AccountID: &a, WalletID: &w}, false}} {
		t.Run(test.name, func(t *testing.T) {
			if got := configCanSee(test.config, uuid.Nil, &a); got != test.want {
				t.Fatalf("visibility %v", got)
			}
		})
	}
	if err := (ScopedEvent{EventType: types.EventType("customer.compliance.updated"), SubjectID: uuid.NewString(), AccountID: &a, Data: map[string]any{"status": "approved"}}).validate(); err != nil {
		t.Fatal(err)
	}
	if err := (ScopedEvent{EventType: types.EventType("wallet.created"), SubjectID: uuid.NewString(), AccountID: &a, Data: map[string]any{}}).validate(); err == nil {
		t.Fatal("ordinary wallet event lost its wallet scope")
	}
}
