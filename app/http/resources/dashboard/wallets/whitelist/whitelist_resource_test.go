package whitelist_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/http/pagination"
	"github.com/macrowallets/waas/app/http/resources/dashboard/wallets/whitelist"
	"github.com/macrowallets/waas/app/models"
)

func TestWhitelist_Entry_KeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	walletID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	updated := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:10"))

	labeled := models.WhitelistEntry{ID: id, WalletID: walletID, Label: "Cold Storage", Address: "0xabc"}
	labeled.CreatedAt = created
	labeled.UpdatedAt = updated
	unlabeled := models.WhitelistEntry{ID: id, WalletID: walletID, Address: "0xabc"}
	unlabeled.CreatedAt = created
	unlabeled.UpdatedAt = updated

	cases := []struct {
		entry models.WhitelistEntry
		want  string
	}{
		{
			entry: models.WhitelistEntry{},
			want:  `{"created_at":null,"updated_at":null,"id":"00000000-0000-0000-0000-000000000000","wallet_id":"00000000-0000-0000-0000-000000000000","address":""}`,
		},
		{
			entry: models.WhitelistEntry{ID: id, WalletID: walletID, Address: "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"},
			want:  `{"created_at":null,"updated_at":null,"id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","address":"bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq"}`,
		},
		{
			entry: labeled,
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","label":"Cold Storage","address":"0xabc"}`,
		},
		{
			entry: unlabeled,
			want:  `{"created_at":"2024-05-06 07:08:09","updated_at":"2024-05-06 07:08:10","id":"11111111-1111-4111-8111-111111111111","wallet_id":"22222222-2222-4222-8222-222222222222","address":"0xabc"}`,
		},
	}
	for _, tc := range cases {
		raw, err := json.Marshal(whitelist.WhitelistEntryFrom(tc.entry))
		if err != nil {
			t.Fatal(err)
		}
		if string(raw) != tc.want {
			t.Fatalf("wire changed\n got %s\nwant %s", raw, tc.want)
		}
	}
}

func TestWhitelist_Entries_PreserveSliceNilness(t *testing.T) {
	t.Parallel()

	if whitelist.WhitelistEntriesFrom(nil) != nil {
		t.Fatal("nil slice became an empty slice")
	}
	empty := whitelist.WhitelistEntriesFrom([]models.WhitelistEntry{})
	if empty == nil {
		t.Fatal("empty slice became nil")
	}
	if len(empty) != 0 {
		t.Fatalf("empty slice length = %d", len(empty))
	}

	nilPage, err := json.Marshal(pagination.Response(whitelist.WhitelistEntriesFrom(nil), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(nilPage) != `{"data":null,"limit":20,"offset":0,"total":0}` {
		t.Fatalf("nil page = %s", nilPage)
	}

	emptyPage, err := json.Marshal(pagination.Response(whitelist.WhitelistEntriesFrom([]models.WhitelistEntry{}), 0, 20, 0))
	if err != nil {
		t.Fatal(err)
	}
	if string(emptyPage) != `{"data":[],"limit":20,"offset":0,"total":0}` {
		t.Fatalf("empty page = %s", emptyPage)
	}
}
