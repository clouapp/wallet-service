package addresses

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	ctltestutil "github.com/macrowallets/waas/tests/feature/support"
	"github.com/macrowallets/waas/tests/feature/support/testutil"
)

// AddressesControllerTestSuite exercises the external /api/v1 address routes
// (GenerateAddress, ListWalletAddresses, LookupAddress, ListUserAddresses).
// It seeds wallets directly via the ORM with real secp256k1 MPC material so
// the wallet service's BIP32 derivation path succeeds — the critical-path
// pattern. Wallet creation and mutation flows are covered elsewhere; this
// suite is exclusively about the read / derive paths.
type AddressesControllerTestSuite struct {
	ctltestutil.HTTPSuite
}

func TestAddresses_Controller_Suite(t *testing.T) {
	ctltestutil.RunSuite(t, new(AddressesControllerTestSuite))
}

// seedAPIWalletForAccount inserts a Wallet bound to accountID with real
// secp256k1 pubkey + chain code so controllers that invoke BIP32 derivation
// (GenerateAddress on secp256k1 chains) succeed. Returns the wallet ID as a
// string for URL interpolation.
func seedAPIWalletForAccount(t *testing.T, accountID uuid.UUID, chain, label string) string {
	t.Helper()

	priv, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("new private key: %v", err)
	}
	pubCompressed := priv.PubKey().SerializeCompressed()
	chainCode := sha256.Sum256([]byte("addresses-suite-chain-code-" + label))

	walletID := uuid.New()
	acct := accountID
	w := &models.Wallet{
		ID:               walletID,
		Chain:            chain,
		Label:            label,
		MPCCustomerShare: "deadbeef",
		MPCShareIV:       "cafebabe",
		MPCShareSalt:     "feedface",
		MPCSecretARN:     "arn:aws:secretsmanager:us-east-1:123456789012:secret:test",
		MPCPublicKey:     hex.EncodeToString(pubCompressed),
		MPCChainCode:     hex.EncodeToString(chainCode[:]),
		MPCCurve:         "secp256k1",
		AccountID:        &acct,
	}
	if err := facades.Orm().Query().Create(w); err != nil {
		t.Fatalf("insert wallet: %v", err)
	}
	return walletID.String()
}

// seedAddressForWallet inserts a deposit address row for walletID. Used by
// list / lookup tests that don't need to exercise the derivation path.
func seedAddressForWallet(t *testing.T, walletID uuid.UUID, chain, addressStr, externalUserID string, index int) {
	t.Helper()
	a := &models.Address{
		ID:              uuid.New(),
		WalletID:        walletID,
		Chain:           chain,
		Address:         addressStr,
		DerivationIndex: index,
		ExternalUserID:  externalUserID,
		IsActive:        true,
		DerivationType:  "bip32",
	}
	if err := facades.Orm().Query().Create(a); err != nil {
		t.Fatalf("insert address: %v", err)
	}
}

func (s *AddressesControllerTestSuite) TestGenerate_Address_Success() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "gen-addr-success")

	body := `{"external_user_id":"user_123","metadata":"{\"tier\":\"premium\"}"}`
	resp := s.External("/api/v1/wallets/"+walletID+"/addresses", ctltestutil.Token{Bearer: bearer}).Post(body)

	resp.AssertCreated().AssertJson(map[string]any{
		"external_user_id": "user_123",
		"chain":            "eth",
		"wallet_id":        walletID,
	})

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload map[string]any
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.NotEmpty(payload["address"])
	s.Equal(true, payload["is_active"])
}

func (s *AddressesControllerTestSuite) TestGenerate_Address_MultipleForSameUser() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "gen-addr-multi")

	body := `{"external_user_id":"user_multi"}`

	resp1 := s.External("/api/v1/wallets/"+walletID+"/addresses", ctltestutil.Token{Bearer: bearer}).Post(body)
	resp1.AssertCreated()
	j1, err := resp1.Json()
	s.Require().NoError(err)
	addr1, _ := j1["address"].(string)
	s.Require().NotEmpty(addr1)

	resp2 := s.External("/api/v1/wallets/"+walletID+"/addresses", ctltestutil.Token{Bearer: bearer}).Post(body)
	resp2.AssertCreated()
	j2, err := resp2.Json()
	s.Require().NoError(err)
	addr2, _ := j2["address"].(string)
	s.Require().NotEmpty(addr2)

	s.NotEqual(addr1, addr2, "successive derivations must yield distinct addresses")
}

func (s *AddressesControllerTestSuite) TestList_Wallet_Addresses() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "list-wallet-addrs")

	wUUID := uuid.MustParse(walletID)
	seedAddressForWallet(s.T(), wUUID, "eth", "0x"+uuid.NewString()[:16], "user1", 1)
	seedAddressForWallet(s.T(), wUUID, "eth", "0x"+uuid.NewString()[:16], "user2", 2)

	resp := s.External("/api/v1/wallets/"+walletID+"/addresses", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []struct {
			Address        string `json:"address"`
			ExternalUserID string `json:"external_user_id"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Len(payload.Data, 2)
	for _, addr := range payload.Data {
		s.NotEmpty(addr.Address)
		s.NotEmpty(addr.ExternalUserID)
	}
}

func (s *AddressesControllerTestSuite) TestLookup_Address_Success() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "lookup-success")

	address := "0x" + uuid.NewString()[:32]
	seedAddressForWallet(s.T(), uuid.MustParse(walletID), "eth", address, "lookup_user", 1)

	resp := s.External("/api/v1/addresses/"+address+"?chain=eth", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk().AssertJson(map[string]any{
		"address":          address,
		"external_user_id": "lookup_user",
		"chain":            "eth",
	})
}

func (s *AddressesControllerTestSuite) TestLookup_Address_NotFound() {
	testutil.SeededTestDB(s.T())
	_, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)

	s.External("/api/v1/addresses/0xnonexistent?chain=eth", ctltestutil.Token{Bearer: bearer}).Get().
		AssertNotFound()
}

func (s *AddressesControllerTestSuite) TestList_User_Addresses() {
	testutil.SeededTestDB(s.T())
	accountID, bearer, _ := ctltestutil.SetupAPIAuth(s.T(), false)
	walletID := seedAPIWalletForAccount(s.T(), accountID, "eth", "list-user-addrs")
	wUUID := uuid.MustParse(walletID)

	seedAddressForWallet(s.T(), wUUID, "eth", "0x"+uuid.NewString()[:16], "target_user", 1)
	seedAddressForWallet(s.T(), wUUID, "eth", "0x"+uuid.NewString()[:16], "target_user", 2)
	seedAddressForWallet(s.T(), wUUID, "eth", "0x"+uuid.NewString()[:16], "other_user", 3)

	resp := s.External("/api/v1/users/target_user/addresses", ctltestutil.Token{Bearer: bearer}).Get()
	resp.AssertOk()

	content, err := resp.Content()
	s.Require().NoError(err)
	var payload struct {
		Data []struct {
			ExternalUserID string `json:"external_user_id"`
		} `json:"data"`
	}
	s.Require().NoError(json.Unmarshal([]byte(content), &payload))
	s.Len(payload.Data, 2)
	for _, a := range payload.Data {
		s.Equal("target_user", a.ExternalUserID)
	}
}
