package chain

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"testing"

	"filippo.io/edwards25519"
	bin "github.com/gagliardetto/binary"
	"github.com/gagliardetto/solana-go"
	"github.com/gagliardetto/solana-go/programs/system"

	"github.com/macrowallets/waas/pkg/types"
)

const testBlockhash = "4sGjMW1sUnHzSxGspuhpqLDx6wiyjNtZAMdL4VZHirAn"

// scalarKeyPair returns an ed25519 public key and its scalar in big-endian form,
// the same shape MPC reconstruction yields.
func scalarKeyPair(t *testing.T) (publicKey, scalarBigEndian []byte) {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		t.Fatal(err)
	}
	digest := sha512.Sum512(seed)
	secret, err := edwards25519.NewScalar().SetBytesWithClamping(digest[:32])
	if err != nil {
		t.Fatal(err)
	}
	little := secret.Bytes()
	scalarBigEndian = make([]byte, len(little))
	for i := range little {
		scalarBigEndian[i] = little[len(little)-1-i]
	}
	publicKey = ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	return publicKey, scalarBigEndian
}

func TestSignEd25519WithScalar_VerifiesAndIsDeterministic(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	message := []byte("solana message bytes")

	first, err := signEd25519WithScalar(scalar, publicKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if !ed25519.Verify(publicKey, message, first) {
		t.Fatal("signature does not verify")
	}
	second, err := signEd25519WithScalar(scalar, publicKey, message)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Fatal("same key and message must give the same signature")
	}
	other, err := signEd25519WithScalar(scalar, publicKey, []byte("another message"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(first[:32], other[:32]) {
		t.Fatal("different messages must use different nonces")
	}
}

func TestSignEd25519WithScalar_RejectsBadInput(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	otherPublicKey, _ := scalarKeyPair(t)
	nonCanonical := bytes.Repeat([]byte{0xff}, ed25519ScalarSize)
	zero := make([]byte, ed25519ScalarSize)

	cases := []struct {
		name      string
		scalar    []byte
		publicKey []byte
	}{
		{"short scalar", scalar[:31], publicKey},
		{"short public key", scalar, publicKey[:31]},
		{"non-canonical scalar", nonCanonical, publicKey},
		{"zero scalar", zero, publicKey},
		{"scalar for another key", scalar, otherPublicKey},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := signEd25519WithScalar(tc.scalar, tc.publicKey, []byte("m")); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestSignTransactionWithScalar_SignsSolanaTransfer(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	from := solana.PublicKeyFromBytes(publicKey)
	to := solana.NewWallet().PublicKey()
	message, err := buildSolanaNativeTx(from, to, 20_000_000, testBlockhash)
	if err != nil {
		t.Fatal(err)
	}

	signed, err := (&SolanaLive{}).SignTransactionWithScalar(context.Background(),
		&types.UnsignedTx{ChainID: "tsol", RawBytes: message}, scalar, publicKey)
	if err != nil {
		t.Fatal(err)
	}

	tx, err := solana.TransactionFromDecoder(bin.NewBinDecoder(signed.RawBytes))
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Signatures) != 1 || tx.Signatures[0].String() != signed.TxHash {
		t.Fatalf("unexpected signatures %v / hash %s", tx.Signatures, signed.TxHash)
	}
	if !ed25519.Verify(publicKey, message, tx.Signatures[0][:]) {
		t.Fatal("signature does not verify against the sender address")
	}
	if err := tx.VerifySignatures(); err != nil {
		t.Fatal(err)
	}
}

func TestSignTransactionWithScalar_RejectsForeignFeePayer(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	someoneElse := solana.NewWallet().PublicKey()
	message, err := buildSolanaNativeTx(someoneElse, solana.NewWallet().PublicKey(), 1, testBlockhash)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signSolanaTxWithScalar(&types.UnsignedTx{RawBytes: message}, scalar, publicKey); err == nil {
		t.Fatal("expected fee payer mismatch")
	}
}

func TestSignTransactionWithScalar_RejectsMultiSignerMessage(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	from := solana.PublicKeyFromBytes(publicKey)
	cosigner := solana.NewWallet().PublicKey()
	hash := solana.MustHashFromBase58(testBlockhash)
	ix := system.NewTransferInstruction(1, cosigner, solana.NewWallet().PublicKey()).Build()
	tx, err := solana.NewTransaction([]solana.Instruction{ix}, hash, solana.TransactionPayer(from))
	if err != nil {
		t.Fatal(err)
	}
	message, err := tx.Message.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := signSolanaTxWithScalar(&types.UnsignedTx{RawBytes: message}, scalar, publicKey); err == nil {
		t.Fatal("expected multi-signer rejection")
	}
}

func TestSignTransactionWithScalar_RejectsEmpty(t *testing.T) {
	publicKey, scalar := scalarKeyPair(t)
	if _, err := signSolanaTxWithScalar(nil, scalar, publicKey); err == nil {
		t.Fatal("expected error for nil tx")
	}
	if _, err := signSolanaTxWithScalar(&types.UnsignedTx{}, scalar, publicKey); err == nil {
		t.Fatal("expected error for empty tx")
	}
}
