package ingest

import (
	"testing"

	ingestsvc "github.com/macrowallets/waas/app/services/ingest"
	"github.com/macrowallets/waas/app/services/ingest/providers"
)

func ingestControllerDeps() IngestControllerDeps {
	return IngestControllerDeps{
		Subscriptions: &ingestsvc.Subscriptions{},
		Ingest:        &ingestsvc.Service{},
		Lookup: func() map[string]providers.WebhookProvider {
			return map[string]providers.WebhookProvider{
				"alchemy": providers.NewAlchemyProvider(""),
			}
		},
	}
}

func TestNewIngestControllerKeepsItsDependencies(t *testing.T) {
	deps := ingestControllerDeps()
	ctrl := NewIngestController(deps)
	if ctrl == nil {
		t.Fatal("NewIngestController returned nil")
	}
	if ctrl.subscriptions != deps.Subscriptions {
		t.Fatal("ingest controller did not keep the webhook subscriptions service")
	}
	if ctrl.ingest != deps.Ingest {
		t.Fatal("ingest controller did not keep the ingest service")
	}
	if ctrl.lookup == nil {
		t.Fatal("ingest controller did not keep the provider lookup")
	}
	found, ok := ctrl.lookup()["alchemy"]
	if !ok || found == nil {
		t.Fatal("ingest controller did not keep the provider lookup")
	}
}

func TestNewIngestControllerAllowsNilLookup(t *testing.T) {
	deps := ingestControllerDeps()
	deps.Lookup = nil
	ctrl := NewIngestController(deps)
	if ctrl == nil {
		t.Fatal("NewIngestController returned nil")
	}
	if ctrl.lookup != nil {
		t.Fatal("ingest controller did not keep a nil provider lookup")
	}
	if ctrl.subscriptions != deps.Subscriptions || ctrl.ingest != deps.Ingest {
		t.Fatal("ingest controller dropped a required dependency")
	}
}

func TestNewIngestControllerRequiresEveryDependency(t *testing.T) {
	cases := []struct {
		name  string
		clear func(*IngestControllerDeps)
		panic string
	}{
		{
			name:  "webhook subscriptions service",
			clear: func(deps *IngestControllerDeps) { deps.Subscriptions = nil },
			panic: "ingest controller: webhook subscriptions service is required",
		},
		{
			name:  "ingest service",
			clear: func(deps *IngestControllerDeps) { deps.Ingest = nil },
			panic: "ingest controller: ingest service is required",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			deps := ingestControllerDeps()
			tc.clear(&deps)
			defer func() {
				got := recover()
				if got != tc.panic {
					t.Fatalf("panic = %v", got)
				}
			}()
			NewIngestController(deps)
			t.Fatal("expected a panic")
		})
	}
}
