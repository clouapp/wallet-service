package webhooks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/webhooks"
	"github.com/macrowallets/waas/app/models"
)

func TestWebhookConfigKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	full := models.WebhookConfig{
		ID: id, URL: "https://example.test/hook", Secret: "super-secret",
		Events: "deposit.confirmed", IsActive: true, WalletID: &other, AccountID: &id, Type: "wallet",
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	cases := []struct {
		cfg  models.WebhookConfig
		want string
	}{
		{
			cfg:  models.WebhookConfig{},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","url":"","events":"","is_active":false}`,
		},
		{
			cfg:  models.WebhookConfig{IsActive: false, Secret: "nope", Type: ""},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","url":"","events":"","is_active":false}`,
		},
		{
			cfg:  full,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","url":"https://example.test/hook","events":"deposit.confirmed","is_active":true,"wallet_id":"22222222-2222-4222-8222-222222222222","account_id":"11111111-1111-4111-8111-111111111111","type":"wallet"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(webhooks.WebhookConfigFrom(tc.cfg))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
		if tc.cfg.Secret != "" && strings.Contains(string(raw), tc.cfg.Secret) {
			t.Fatal("signing secret is on the wire")
		}
	}

	nilRaw, err := json.Marshal(webhooks.WebhookConfigPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil config = %s", nilRaw)
	}
}

func TestWebhookConfigsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if webhooks.WebhookConfigsFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := webhooks.WebhookConfigsFrom([]models.WebhookConfig{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": webhooks.WebhookConfigsFrom(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": webhooks.WebhookConfigsFrom([]models.WebhookConfig{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
