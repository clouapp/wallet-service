package solana

import (
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
