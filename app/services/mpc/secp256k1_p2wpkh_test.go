package mpc

import (
	"bytes"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

func TestSignSecp256k1P2WPKH_MatchesLowSDER(t *testing.T) {
	privateKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatal(err)
	}
	digest := bytes.Repeat([]byte{0x11}, 32)
	raw := privateKey.Serialize()

	signature, publicKey, err := SignSecp256k1P2WPKH(raw, digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) < 2 || signature[len(signature)-1] != 0x01 {
		t.Fatalf("signature is not SIGHASH_ALL DER, length %d", len(signature))
	}
	parsed, err := ecdsa.ParseDERSignature(signature[:len(signature)-1])
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Verify(digest, privateKey.PubKey()) {
		t.Fatal("signature does not verify")
	}
	if !bytes.Equal(publicKey, privateKey.PubKey().SerializeCompressed()) {
		t.Fatal("compressed public key mismatch")
	}
	want := append(ecdsa.Sign(privateKey, digest).Serialize(), 0x01)
	if !bytes.Equal(signature, want) {
		t.Fatal("encoding differs from the bitcoin witness signature")
	}
}
