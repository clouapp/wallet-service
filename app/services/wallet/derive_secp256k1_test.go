package wallet

import (
	"encoding/hex"
	"testing"
)

func TestDeriveSecp256k1ChildAddress_ETH_Index0(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(result.ChildPubKey) != 33 {
		t.Fatalf("expected 33-byte compressed pubkey, got %d", len(result.ChildPubKey))
	}
	if len(result.ILBytes) == 0 {
		t.Fatal("ILBytes should not be empty")
	}
	if result.Index != 0 {
		t.Fatalf("expected index 0, got %d", result.Index)
	}
}

func TestDeriveSecp256k1ChildAddress_DifferentIndices(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	r0, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("index 0: %v", err)
	}
	r1, err := deriveSecp256k1Child(pubKey, chainCode, 1)
	if err != nil {
		t.Fatalf("index 1: %v", err)
	}

	if hex.EncodeToString(r0.ChildPubKey) == hex.EncodeToString(r1.ChildPubKey) {
		t.Fatal("different indices must produce different child public keys")
	}
}

func TestDeriveSecp256k1ChildAddress_ETH(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := deriveEthAddress(result.ChildPubKey)
	if err != nil {
		t.Fatalf("deriveEthAddress: %v", err)
	}
	if len(addr) != 42 || addr[:2] != "0x" {
		t.Fatalf("invalid ETH address: %s", addr)
	}
}

func TestDeriveSecp256k1ChildAddress_BTC(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	result, err := deriveSecp256k1Child(pubKey, chainCode, 0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	addr, err := deriveBtcAddress("tb", result.ChildPubKey)
	if err != nil {
		t.Fatalf("deriveBtcAddress: %v", err)
	}
	if len(addr) < 10 || addr[:2] != "tb" {
		t.Fatalf("invalid BTC testnet address: %s", addr)
	}
}

func TestDeriveSecp256k1ChildAddress_RejectsHardenedIndex(t *testing.T) {
	pubKeyHex := "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
	chainCodeHex := "873dff81c02f525623fd1fe5167eac3a55a049de3d314bb42ee227ffed37d508"

	pubKey, _ := hex.DecodeString(pubKeyHex)
	chainCode, _ := hex.DecodeString(chainCodeHex)

	_, err := deriveSecp256k1Child(pubKey, chainCode, 0x80000000)
	if err == nil {
		t.Fatal("expected error for hardened index, got nil")
	}
}
