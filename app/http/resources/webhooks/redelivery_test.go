package webhooks_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/http/resources/webhooks"
)

func TestRedelivery_Wire_NamesTheWebhookAndTheRequeuedCount(t *testing.T) {
	t.Parallel()
	id := uuid.MustParse("3f2b8c1e-0d4a-4f6b-9e2a-7c5d1b8e9f00")

	raw, err := json.Marshal(webhooks.NewRedelivery(id, 3))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"webhook_id":"3f2b8c1e-0d4a-4f6b-9e2a-7c5d1b8e9f00","requeued":3}` {
		t.Fatalf("wire = %s", raw)
	}
}
