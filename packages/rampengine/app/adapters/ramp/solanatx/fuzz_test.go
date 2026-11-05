package solanatx

import (
	"bytes"
	"errors"
	"testing"

	"github.com/gagliardetto/solana-go"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
)

// seeds are the transactions the unit tests generate, valid and broken.
func seeds(f *testing.F) (raws [][]byte, fingerprints []string) {
	f.Helper()
	fx := newFixture(f, solana.MessageVersionV0)
	pair := newFixture(f, solana.MessageVersionV0, solana.NewWallet().PrivateKey, solana.NewWallet().PrivateKey)
	legacy := newFixture(f, solana.MessageVersionLegacy)
	signed := fx.signed(f)
	raws = [][]byte{
		fx.unsigned,
		signed,
		pair.unsigned,
		pair.signed(f),
		flipMessageByte(signed),
		fx.signedBy(f, solana.NewWallet().PrivateKey),
		append(bytes.Clone(signed), 0),
		legacy.unsigned,
		legacy.signed(f),
		withSignatureSlots(f, fx, 2),
		fx.message(f),
		withMessagePrefix(signed, 1, 0x81),
		withMessagePrefix(signed, 1, 0x7f),
		withAddressTableLookup(f, fx),
		append(bytes.Clone(legacy.signed(f)), 0),
		flipMessageByte(legacy.signed(f)),
		withMessagePrefix(legacy.signed(f), 1, 0x7f),
		{},
	}
	return raws, []string{Fingerprint(fx.message(f)), Fingerprint(legacy.message(f))}
}

func FuzzUnsigned(f *testing.F) {
	raws, _ := seeds(f)
	for _, raw := range raws {
		f.Add(b64(raw))
	}
	f.Fuzz(func(t *testing.T, input string) {
		tx, message, err := Unsigned(input)
		if err != nil {
			return
		}
		encoded, err := tx.MarshalBinary()
		if err != nil {
			t.Fatalf("accepted transaction does not re-encode: %v", err)
		}
		if b64(encoded) != input {
			t.Fatal("accepted transaction does not round-trip")
		}
		if !bytes.HasSuffix(encoded, message) {
			t.Fatal("answered message is not the transaction's message")
		}
		legacy := tx.Message.GetVersion() == solana.MessageVersionLegacy && message[0] < v0Prefix
		v0 := tx.Message.GetVersion() == solana.MessageVersionV0 && message[0] == v0Prefix
		if !legacy && !v0 {
			t.Fatalf("accepted message version byte 0x%02x", message[0])
		}
		for _, sig := range tx.Signatures {
			if !sig.IsZero() {
				t.Fatal("accepted an unsigned transaction with a signature set")
			}
		}
	})
}

func FuzzVerifySigned(f *testing.F) {
	raws, fingerprints := seeds(f)
	for _, raw := range raws {
		for _, fingerprint := range fingerprints {
			f.Add(b64(raw), fingerprint)
		}
	}
	f.Fuzz(func(t *testing.T, input, wantFingerprint string) {
		tx, err := VerifySigned(input, wantFingerprint)
		if err != nil {
			if !errors.Is(err, ramp.ErrInvalidSignedActionPayload) {
				t.Fatalf("error does not wrap ErrInvalidSignedActionPayload: %v", err)
			}
			return
		}
		if err := tx.VerifySignatures(); err != nil {
			t.Fatalf("accepted a transaction whose signatures do not verify: %v", err)
		}
		message, err := tx.Message.MarshalBinary()
		if err != nil || Fingerprint(message) != wantFingerprint {
			t.Fatal("accepted a transaction whose message does not match the fingerprint")
		}
	})
}
