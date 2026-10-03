package wallets

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/app/models"
	wallet "github.com/macrowallets/waas/app/services/wallet"
)

func TestCreateWalletResponseKeepsTheModelWire(t *testing.T) {
	t.Parallel()

	id := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	created := carbon.NewDateTime(carbon.Parse("2024-05-06 07:08:09"))
	const share = "share-secret"
	record := &models.Wallet{
		ID: id, Chain: "eth", Label: "hot", Status: "active", RequiredApprovals: 1,
		MPCCustomerShare: share, MPCShareIV: "iv-secret", MPCShareSalt: "salt-secret",
		ActivationCode: stringPointer("123456"),
	}
	record.CreatedAt = created

	raw, err := json.Marshal(newCreateWalletResponse(&wallet.CreateWalletResult{
		Wallet:           record,
		EncryptedUserKey: "ek",
		ServicePublicKey: "pk",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{share, "iv-secret", "salt-secret", "123456"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("wallet key material is on the wire")
		}
	}
	const want = `{"created_at":"2024-05-06 07:08:09","updated_at":null,"id":"11111111-1111-4111-8111-111111111111","chain":"eth","label":"hot","address_index":0,"status":"active","required_approvals":1,"read_model_status":"","gas_status":"","sweep_policy_version":0,"encrypted_user_key":"ek","service_public_key":"pk"}`
	if string(raw) != want {
		t.Fatalf("wire changed\n got %s\nwant %s", raw, want)
	}
}

func stringPointer(value string) *string { return &value }
