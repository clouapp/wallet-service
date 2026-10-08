package addressing

import (
	"encoding/hex"
	"testing"

	"github.com/macrowallets/waas/app/models"
)

func TestDeriveXRPAddressVectors(t *testing.T) {
	t.Parallel()

	// ripple-keypairs master account: compressed secp256k1 public key of the
	// genesis seed, classic address rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTh.
	pub, err := hex.DecodeString("0330E7FC9D56BB25D6893BA3F317AE5BCF33B3291BD63DB32654A313222F7FD020")
	if err != nil {
		t.Fatal(err)
	}
	address, err := DeriveXRPAddress(pub)
	if err != nil {
		t.Fatal(err)
	}
	const want = "rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTh"
	if address != want {
		t.Fatalf("address = %s, want %s", address, want)
	}
	accountID, err := DecodeXRPClassicAddress(address)
	if err != nil || len(accountID) != 20 {
		t.Fatalf("account id %x err %v", accountID, err)
	}
	for _, chainID := range []string{models.ChainXRP, models.ChainTXRP} {
		for _, testnet := range []bool{false, true} {
			routed, err := DeriveAddressOnNetwork(chainID, testnet, pub)
			if err != nil || routed != want {
				t.Fatalf("DeriveAddressOnNetwork(%s, testnet=%v) = %s, %v", chainID, testnet, routed, err)
			}
		}
	}
}

func TestXRPClassicAddressEncodingVector(t *testing.T) {
	t.Parallel()

	// ripple-address-codec: account id BA8E78626EE42C41B46D46C3048DF3A1C3C87072.
	payload, err := hex.DecodeString("00BA8E78626EE42C41B46D46C3048DF3A1C3C87072")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := encodeXRPClassicAddress(payload)
	if err != nil {
		t.Fatal(err)
	}
	const want = "rJrRMgiRgrU6hDF4pgu5DXQdWyPbY35ErN"
	if encoded != want {
		t.Fatalf("encoded = %s, want %s", encoded, want)
	}
	if !IsXRPClassicAddress(want) {
		t.Fatal("known classic address rejected")
	}
}

func TestXRPClassicAddressRejectsOtherShapes(t *testing.T) {
	t.Parallel()

	for _, address := range []string{
		"",
		"rHb9CJAWyB4rj91VRWn96DkukG4bwdtyTi",
		"X7AcgcsBL6XDcUb289X4mJ8djcdyKaB5hJDWMArnXr61cqZ",
		"TJRabPrwbZy45sbavfcjinPJC18kjpRTv8",
		"0x7e5f4552091a69125d5dfcb7b8c2659029395bdf",
	} {
		if IsXRPClassicAddress(address) {
			t.Errorf("accepted %q", address)
		}
	}
}
