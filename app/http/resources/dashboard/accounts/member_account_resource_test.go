package accounts_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/resources/dashboard/accounts"
	"github.com/macrowallets/waas/app/models"
	accountsvc "github.com/macrowallets/waas/app/services/account"
)

func TestMember_Account_KeepsTheAccountWire(t *testing.T) {
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

	raw, err := json.Marshal(accounts.NewMemberAccounts([]accountsvc.MemberAccount{
		{View: accountsvc.View{Account: full, SweepLimits: &limits}, Role: "owner"},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","name":"Acme","status":"active","view_all_wallets":true,"environment":"prod","linked_account_id":"22222222-2222-4222-8222-222222222222","sweep_limits":"{\"max\":1}","role":"owner"}]`
	if string(raw) != want {
		t.Fatalf("wire changed\n got %s\nwant %s", raw, want)
	}

	zero, err := json.Marshal(accounts.MemberAccount{})
	if err != nil {
		t.Fatal(err)
	}
	wantZero := `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","name":"","status":"","view_all_wallets":false,"environment":"","role":""}`
	if string(zero) != wantZero {
		t.Fatalf("zero wire changed\n got %s\nwant %s", zero, wantZero)
	}

	empty, err := json.Marshal(accounts.NewMemberAccounts(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(empty) != `[]` {
		t.Fatalf("an empty page is %s, want []", empty)
	}
}

func TestDefault_Account_KeepsTheEnvelope(t *testing.T) {
	t.Parallel()

	none, err := json.Marshal(accounts.NewDefaultAccount(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(none) != `{"account":null}` {
		t.Fatalf("missing account = %s", none)
	}

	view := accountsvc.View{Account: models.Account{ID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Name: "Acme"}}
	got, err := json.Marshal(accounts.NewDefaultAccount(&view))
	if err != nil {
		t.Fatal(err)
	}
	account := accounts.NewAccount(view)
	want, err := json.Marshal(map[string]any{"account": &account})
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("wire changed\n got %s\nwant %s", got, want)
	}
}
