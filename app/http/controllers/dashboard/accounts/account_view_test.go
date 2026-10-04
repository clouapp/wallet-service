package accounts

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestAccountViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	limits := `{"max":1}`
	empty := ""

	full := models.Account{
		ID: id, Name: "Acme", Status: "active", ViewAllWallets: true,
		Environment: "prod", LinkedAccountID: &other,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated
	fullView := NewAccountView(full)
	fullView.SweepLimits = &limits
	emptyView := NewAccountView(models.Account{LinkedAccountID: &uuid.UUID{}})
	emptyView.SweepLimits = &empty

	cases := []struct {
		view AccountView
		want string
	}{
		{
			view: NewAccountView(models.Account{}),
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","status":"","view_all_wallets":false,"environment":""}`,
		},
		{
			view: emptyView,
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","status":"","view_all_wallets":false,"environment":"","linked_account_id":"00000000-0000-0000-0000-000000000000","sweep_limits":""}`,
		},
		{
			view: fullView,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","name":"Acme","status":"active","view_all_wallets":true,"environment":"prod","linked_account_id":"22222222-2222-4222-8222-222222222222","sweep_limits":"{\"max\":1}"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(tc.view)
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestAccountViewPtrKeepsNil(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(AccountViewPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "null" {
		t.Fatalf("nil account = %s", raw)
	}
}
