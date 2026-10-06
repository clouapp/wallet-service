package tron

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/crypto"

	"github.com/macrowallets/waas/app/services/addressing"
	"github.com/macrowallets/waas/pkg/types"
)

const (
	tronTestSenderKeyHex = "1f2b77e3a4b50120fa9c8f6d0c6e2f3a4b5c6d7e8f90a1b2c3d4e5f60718293a"
	tronTestOtherKeyHex  = "0b6e18cafb6ed99687ec547bd28139cafdd2bffe70e6b688025de6b445aa5c5b"
)

func tronTestKey(t *testing.T, keyHex string) (*ecdsa.PrivateKey, string) {
	t.Helper()
	key, err := crypto.HexToECDSA(keyHex)
	if err != nil {
		t.Fatal(err)
	}
	address, err := addressing.EncodeTronAddress(tronRawAddressOf(&key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	return key, address
}

// buildTestTRXTransfer builds a TRX transfer from the sender key's address to an
// activated recipient on a fake Nile node.
func buildTestTRXTransfer(t *testing.T) (*TronLive, *types.UnsignedTx, *ecdsa.PrivateKey, string) {
	t.Helper()
	key, from := tronTestKey(t, tronTestSenderKeyHex)
	_, to := tronTestKey(t, tronTestOtherKeyHex)
	node := newFakeTronNode(t)
	node.fund(t, from, 50_000_000)
	node.fund(t, to, 1)
	adapter := newTronTestAdapter(t, node)
	unsigned, err := adapter.BuildTransfer(context.Background(), types.TransferRequest{From: from, To: to, Amount: big.NewInt(1_234_567), Asset: "TRX"})
	if err != nil {
		t.Fatal(err)
	}
	return adapter, unsigned, key, from
}

func mpcRS(t *testing.T, digest []byte, key *ecdsa.PrivateKey) []byte {
	t.Helper()
	signature, err := crypto.Sign(digest, key)
	if err != nil {
		t.Fatal(err)
	}
	return signature[:tronRSSize]
}

func TestTronFinalizeMPCSignatureAndVerify(t *testing.T) {
	adapter, unsigned, key, from := buildTestTRXTransfer(t)
	compressed := crypto.CompressPubkey(&key.PublicKey)
	rs := mpcRS(t, unsigned.RawBytes, key)

	for name, publicKey := range map[string][]byte{"compressed": compressed, "uncompressed": crypto.FromECDSAPub(&key.PublicKey)} {
		t.Run(name, func(t *testing.T) {
			signed, err := adapter.FinalizeMPCSignature(unsigned, rs, publicKey)
			if err != nil {
				t.Fatal(err)
			}
			if signed.TxHash != hex.EncodeToString(unsigned.RawBytes) || signed.TxHash != unsigned.Metadata["tx_id"] {
				t.Fatalf("tx hash %s is not the txID", signed.TxHash)
			}
			raw, sigs, err := decodeTronTransaction(signed.RawBytes)
			if err != nil || len(sigs) != 1 || len(sigs[0]) != tronSignatureSize || sigs[0][64] > 1 {
				t.Fatalf("signed envelope: %v %x", err, sigs)
			}
			if hex.EncodeToString(raw) != unsigned.Metadata["raw_data_hex"] {
				t.Fatal("signed raw data differs from the built one")
			}
			if err := adapter.VerifySignedTransaction(unsigned, signed, from); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestTronFinalizeMPCSignatureNormalizesHighS(t *testing.T) {
	adapter, unsigned, key, from := buildTestTRXTransfer(t)
	rs := mpcRS(t, unsigned.RawBytes, key)
	highS := append([]byte(nil), rs...)
	s := new(big.Int).SetBytes(rs[32:])
	new(big.Int).Sub(secp256k1Order, s).FillBytes(highS[32:])

	signed, err := adapter.FinalizeMPCSignature(unsigned, highS, crypto.CompressPubkey(&key.PublicKey))
	if err != nil {
		t.Fatal(err)
	}
	_, sigs, _ := decodeTronTransaction(signed.RawBytes)
	if !bytes.Equal(sigs[0][:tronRSSize], rs) {
		t.Fatalf("high S not normalized: %x", sigs[0])
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, from); err != nil {
		t.Fatal(err)
	}
}

func TestTronFinalizeMPCSignatureRejections(t *testing.T) {
	adapter, unsigned, key, _ := buildTestTRXTransfer(t)
	other, _ := tronTestKey(t, tronTestOtherKeyHex)
	rs := mpcRS(t, unsigned.RawBytes, key)
	compressed := crypto.CompressPubkey(&key.PublicKey)

	tampered := *unsigned
	tampered.RawBytes = append([]byte(nil), unsigned.RawBytes...)
	tampered.RawBytes[0] ^= 1
	zeroR := append(make([]byte, 32), rs[32:]...)

	cases := []struct {
		name      string
		unsigned  *types.UnsignedTx
		signature []byte
		publicKey []byte
	}{
		{"wrong public key", unsigned, rs, crypto.CompressPubkey(&other.PublicKey)},
		{"signature by another key", unsigned, mpcRS(t, unsigned.RawBytes, other), compressed},
		{"65-byte signature", unsigned, append(rs, 0), compressed},
		{"zero R", unsigned, zeroR, compressed},
		{"short public key", unsigned, rs, compressed[:32]},
		{"txID not of raw data", &tampered, rs, compressed},
		{"no metadata", &types.UnsignedTx{RawBytes: unsigned.RawBytes}, rs, compressed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := adapter.FinalizeMPCSignature(tc.unsigned, tc.signature, tc.publicKey); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestTronSignTransactionLocal(t *testing.T) {
	adapter, unsigned, key, from := buildTestTRXTransfer(t)
	signed, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(key))
	if err != nil {
		t.Fatal(err)
	}
	if err := adapter.VerifySignedTransaction(unsigned, signed, from); err != nil {
		t.Fatal(err)
	}

	other, _ := tronTestKey(t, tronTestOtherKeyHex)
	if _, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(other)); err == nil {
		t.Fatal("signed with a key that does not own owner_address")
	}
	if _, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(key)[:31]); err == nil {
		t.Fatal("31-byte key accepted")
	}
}

func TestTronVerifySignedTransactionRejections(t *testing.T) {
	adapter, unsigned, key, from := buildTestTRXTransfer(t)
	signed, err := adapter.SignTransaction(context.Background(), unsigned, crypto.FromECDSA(key))
	if err != nil {
		t.Fatal(err)
	}
	other, otherAddress := tronTestKey(t, tronTestOtherKeyHex)
	rawBytes := mustHex(t, unsigned.Metadata["raw_data_hex"].(string))

	otherSig, _ := crypto.Sign(unsigned.RawBytes, other)
	bySomeoneElse := &types.SignedTx{TxHash: signed.TxHash, RawBytes: encodeTronTransaction(rawBytes, otherSig)}

	changedRaw := append([]byte(nil), rawBytes...)
	changedRaw[len(changedRaw)-1] ^= 1
	_, sigs, _ := decodeTronTransaction(signed.RawBytes)
	differentRaw := &types.SignedTx{RawBytes: encodeTronTransaction(changedRaw, sigs[0])}

	twoSigs := &types.SignedTx{RawBytes: encodeTronTransaction(rawBytes, sigs[0], sigs[0])}
	badV := append([]byte(nil), sigs[0]...)
	badV[64] = 2
	wrongHash := &types.SignedTx{TxHash: strings.Repeat("ab", 32), RawBytes: signed.RawBytes}

	cases := []struct {
		name   string
		signed *types.SignedTx
		from   string
	}{
		{"signed by another key", bySomeoneElse, from},
		{"raw data changed", differentRaw, from},
		{"two signatures", twoSigs, from},
		{"recovery id 2", &types.SignedTx{RawBytes: encodeTronTransaction(rawBytes, badV)}, from},
		{"tx hash not the txID", wrongHash, from},
		{"from is not the owner", signed, otherAddress},
		{"invalid from", signed, "0x7E5F4552091A69125d5DfCb7b8C2659029395Bdf"},
		{"empty", &types.SignedTx{}, from},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := adapter.VerifySignedTransaction(unsigned, tc.signed, tc.from); err == nil {
				t.Fatal("accepted")
			}
		})
	}

	// TronWeb-style v (27/28) verifies like 0/1.
	tronWebV := append([]byte(nil), sigs[0]...)
	tronWebV[64] += tronRecoveryIDOffset
	if err := adapter.VerifySignedTransaction(unsigned, &types.SignedTx{RawBytes: encodeTronTransaction(rawBytes, tronWebV)}, from); err != nil {
		t.Fatalf("v=27/28: %v", err)
	}
}
