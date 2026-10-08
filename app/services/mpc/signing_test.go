package mpc

import (
	"bytes"
	"context"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
)

func TestSigning_Reconstruct_Secp2561(t *testing.T) {
	svc := NewTSSService()
	result, err := svc.Keygen(context.Background(), CurveSecp256k1)
	if err != nil {
		t.Fatal(err)
	}
	privBytes, err := svc.ReconstructSecp256k1PrivateKey(result.ShareA, result.ShareB)
	if err != nil {
		t.Fatal(err)
	}
	priv, _ := btcec.PrivKeyFromBytes(privBytes)
	if !bytes.Equal(priv.PubKey().SerializeCompressed(), result.CombinedPubKey) {
		t.Fatal("reconstructed public key does not match keygen")
	}
}
