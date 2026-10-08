package accounts

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestAccount_From_KeepsThePlatformListFields(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	linked := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	raw, err := json.Marshal(AccountFrom(models.Account{
		ID:              id,
		Name:            "Acme",
		Status:          "active",
		ViewAllWallets:  true,
		Environment:     "test",
		LinkedAccountID: &linked,
	}))
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"id":"11111111-1111-4111-8111-111111111111","name":"Acme","status":"active"}`
	if string(raw) != want {
		t.Fatalf("account json = %s, want %s", raw, want)
	}
}

func TestAccounts_From_NilIsAnEmptyList(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(AccountsFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "[]" {
		t.Fatalf("nil page = %s, want []", raw)
	}
}
