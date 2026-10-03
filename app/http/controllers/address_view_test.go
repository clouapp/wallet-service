package controllers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
)

func TestAddressViewKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	other := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))
	createdBy := other
	full := models.Address{
		ID: id, WalletID: other, Chain: "eth", Address: "0xabc", DerivationIndex: 0,
		ExternalUserID: "system", Metadata: "", IsActive: true, Label: "Deposit Address",
		CreatedBy: &createdBy, DerivationType: "genesis",
		EncryptedPrivateKey: "cipher-secret", EncryptionIV: "iv-secret", EncryptionSalt: "salt-secret",
	}
	full.CreatedAt = created
	full.UpdatedAt = updated

	stamped := models.Address{Metadata: "", IsActive: false}
	stamped.CreatedAt = created

	cases := []struct {
		addr models.Address
		want string
	}{
		{
			addr: models.Address{},
			want: `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain":"","address":"","derivation_index":0,"external_user_id":"","metadata":"","is_active":false,"derivation_type":""}`,
		},
		{
			addr: stamped,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","chain":"","address":"","derivation_index":0,"external_user_id":"","metadata":"","is_active":false,"derivation_type":""}`,
		},
		{
			addr: full,
			want: `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","chain":"eth","address":"0xabc","derivation_index":0,"external_user_id":"system","metadata":"","is_active":true,"label":"Deposit Address","created_by":"22222222-2222-4222-8222-222222222222","derivation_type":"genesis"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(newAddressView(tc.addr))
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{tc.addr.EncryptedPrivateKey, tc.addr.EncryptionIV, tc.addr.EncryptionSalt} {
			if secret != "" && strings.Contains(string(raw), secret) {
				t.Fatal("address key material is on the wire")
			}
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}

	nilRaw, err := json.Marshal(AddressViewPtr(nil))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilRaw) != "null" {
		t.Fatalf("nil address = %s", nilRaw)
	}
}

func TestAddressViewKeepsARelatedWalletOffTheKeyMaterial(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	const share = "share-secret"
	addr := models.Address{
		ID: id, Chain: "eth", DerivationType: "genesis",
		EncryptedPrivateKey: "cipher-secret", EncryptionIV: "iv-secret", EncryptionSalt: "salt-secret",
		Wallet: &models.Wallet{ID: id, Chain: "eth", Label: "hot", MPCCustomerShare: share},
	}
	raw, err := json.Marshal(newAddressView(addr))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{addr.EncryptedPrivateKey, addr.EncryptionIV, addr.EncryptionSalt, share} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("key material is on the wire")
		}
	}
	if !strings.Contains(string(raw), `"wallet":{"created_at":null,"updated_at":null,"id":"11111111-1111-4111-8111-111111111111","chain":"eth","label":"hot","address_index":0,"status":"","required_approvals":0,"read_model_status":"","gas_status":"","sweep_policy_version":0}`) {
		t.Fatal("related wallet changed")
	}
}

func TestAddressViewsPreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if AddressViews(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := AddressViews([]models.Address{})
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty slice = %#v", empty)
	}

	nilPage, err := json.Marshal(map[string]any{"data": AddressViews(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(map[string]any{"data": AddressViews([]models.Address{})})
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[]}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
