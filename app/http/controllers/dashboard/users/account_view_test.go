package users

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	dashboardaccounts "github.com/macrowallets/waas/app/http/controllers/dashboard/accounts"
	"github.com/macrowallets/waas/app/models"
)

func TestMyAccountKeepsTheAccountWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	limits := `{"max":1}`
	full := models.Account{
		ID: id, Name: "Acme", Status: "active", ViewAllWallets: true,
		Environment: "prod", LinkedAccountID: &other,
	}
	full.CreatedAt = created
	full.UpdatedAt = updated
	view := dashboardaccounts.NewAccountView(full)
	view.SweepLimits = &limits

	raw, err := json.Marshal(myAccount{AccountView: view, Role: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","name":"Acme","status":"active","view_all_wallets":true,"environment":"prod","linked_account_id":"22222222-2222-4222-8222-222222222222","sweep_limits":"{\"max\":1}","role":"owner"}`
	if string(raw) != want {
		t.Fatalf("wire changed\n got %s\nwant %s", raw, want)
	}

	zero, err := json.Marshal(myAccount{})
	if err != nil {
		t.Fatal(err)
	}
	wantZero := `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","status":"","view_all_wallets":false,"environment":"","role":""}`
	if string(zero) != wantZero {
		t.Fatalf("zero wire changed\n got %s\nwant %s", zero, wantZero)
	}
}
