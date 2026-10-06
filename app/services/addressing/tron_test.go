package addressing

import (
	"encoding/hex"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/models"
)

func compressedPubKeyOf(t *testing.T, privateKeyHex string) []byte {
	t.Helper()
	key, err := crypto.HexToECDSA(privateKeyHex)
	if err != nil {
		t.Fatal(err)
	}
	return crypto.CompressPubkey(&key.PublicKey)
}

func TestDeriveTronAddressVectors(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		privateKey string
		wantHex    string
		wantBase58 string
	}{
		{
			// Private key 1: the EVM address 0x7E5F…5Bdf under the 0x41 prefix.
			name:       "private key 1",
			privateKey: "0000000000000000000000000000000000000000000000000000000000000001",
			wantHex:    "417e5f4552091a69125d5dfcb7b8c2659029395bdf",
			wantBase58: "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC",
		},
		{
			// TIP-01 (https://github.com/tronprotocol/tips/blob/master/tip-01.md): this
			// key's address body is E11973395042BA3C0B52B4CDF4E15EA77818F275 (the TIP
			// shows it under the old testnet prefix 0xA0; mainnet uses 0x41).
			name:       "TIP-01",
			privateKey: "F43EBCC94E6C257EDBE559183D1A8778B2D5A08040902C0F0A77A3343A1D0EA5",
			wantHex:    "41e11973395042ba3c0b52b4cdf4e15ea77818f275",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pub := compressedPubKeyOf(t, tc.privateKey)
			address, err := DeriveTronAddress(pub)
			if err != nil {
				t.Fatal(err)
			}
			if tc.wantBase58 != "" && address != tc.wantBase58 {
				t.Fatalf("address = %s, want %s", address, tc.wantBase58)
			}
			hexAddress, err := TronAddressToHex(address)
			if err != nil || hexAddress != tc.wantHex {
				t.Fatalf("hex = %s, want %s (%v)", hexAddress, tc.wantHex, err)
			}
			for _, chainID := range []string{models.ChainTron, models.ChainTTron} {
				for _, testnet := range []bool{false, true} {
					routed, err := DeriveAddressOnNetwork(chainID, testnet, pub)
					if err != nil || routed != address {
						t.Fatalf("DeriveAddressOnNetwork(%s, testnet=%v) = %s, %v", chainID, testnet, routed, err)
					}
				}
			}
		})
	}
}

func TestTronAddressEncodingVectors(t *testing.T) {
	t.Parallel()
	cases := []struct{ base58, hex string }{
		// https://developers.tron.network/docs/encoding (Base58Check and hex forms
		// of the same address).
		{"TJRabPrwbZy45sbavfcjinPJC18kjpRTv8", "415cbdd86a2fa8dc4bddd8a8f69dba48572eec07fb"},
		// Mainnet USDT contract.
		{models.USDTContractTron, "41a614f803b6fd780986a42c78ec9c7f77e6ded13c"},
		// Nile USDT contract (its owner_address in the Nile transfer c4c6e63a…).
		{models.USDTContractTronNile, "41eca9bc828a3005b9a3b909f2cc5c2a54794de05f"},
	}
	for _, tc := range cases {
		gotHex, err := TronAddressToHex(tc.base58)
		if err != nil || gotHex != tc.hex {
			t.Fatalf("TronAddressToHex(%s) = %s, %v; want %s", tc.base58, gotHex, err, tc.hex)
		}
		gotBase58, err := TronAddressFromHex(strings.ToUpper(tc.hex))
		if err != nil || gotBase58 != tc.base58 {
			t.Fatalf("TronAddressFromHex(%s) = %s, %v", tc.hex, gotBase58, err)
		}
	}
}

func TestTronAddressRejections(t *testing.T) {
	t.Parallel()
	valid := "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2HC"
	badChecksum := valid[:len(valid)-1] + "D"
	raw, _ := hex.DecodeString("a07e5f4552091a69125d5dfcb7b8c2659029395bdf")
	for _, address := range []string{
		"", badChecksum, valid[:33], valid + "1", "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf",
		"1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", "TMVQGm1qAQYVdetCeGRRkTWYYrLXuHK2H0",
	} {
		if IsTronAddress(address) {
			t.Fatalf("%q accepted", address)
		}
	}
	if !IsTronAddress(valid) {
		t.Fatal("valid address rejected")
	}
	if _, err := EncodeTronAddress(raw); err == nil {
		t.Fatal("0xa0 prefix accepted")
	}
	if _, err := EncodeTronAddress(raw[1:]); err == nil {
		t.Fatal("20-byte address accepted")
	}
	for _, value := range []string{"417e5f", "zz7e5f4552091a69125d5dfcb7b8c2659029395bdf", "a07e5f4552091a69125d5dfcb7b8c2659029395bdf"} {
		if _, err := TronAddressFromHex(value); err == nil {
			t.Fatalf("hex %q accepted", value)
		}
	}
	if _, err := DeriveTronAddress(make([]byte, 33)); err == nil {
		t.Fatal("invalid public key accepted")
	}
}
