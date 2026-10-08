package settings

import (
	"errors"
	"testing"
)

type stubCipher struct {
	prefix string
}

func (c stubCipher) EncryptString(value string) (string, error) {
	return c.prefix + value, nil
}

func (c stubCipher) DecryptString(value string) (string, error) {
	if len(value) < len(c.prefix) || value[:len(c.prefix)] != c.prefix {
		return "", errStubDecrypt
	}
	return value[len(c.prefix):], nil
}

type stubDecryptError struct{}

func (stubDecryptError) Error() string { return "stub decrypt" }

var errStubDecrypt = stubDecryptError{}

func TestPrefix_Seal_TagsStoredCiphertextOnce(t *testing.T) {
	t.Parallel()

	const stored = "cipher-blob"
	tagged := PrefixSeal(stored)
	if !IsSealed(tagged) || tagged == stored || PrefixSeal(tagged) != tagged {
		t.Fatal("prefix seal did not tag the stored ciphertext once")
	}
	if PrefixSeal("") != "" {
		t.Fatal("prefix seal changed an empty value")
	}
}

func TestSeal_Tags_CiphertextAndOpenRefusesPlaintext(t *testing.T) {
	t.Parallel()

	const plaintext = "super-secret"
	sealed, err := Seal(stubCipher{prefix: "cipher:"}, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !IsSealed(sealed) {
		t.Fatalf("sealed value %q has no marker", sealed)
	}
	if sealed == plaintext {
		t.Fatal("plaintext was stored without encryption")
	}
	want := sealedPrefix + "cipher:" + plaintext
	if sealed != want {
		t.Fatalf("Seal = %q, want %q", sealed, want)
	}

	opened, err := Open(stubCipher{prefix: "cipher:"}, sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != plaintext {
		t.Fatalf("Open = %q", opened)
	}
	openedAgain, err := Open(stubCipher{prefix: "cipher:"}, plaintext)
	if !errors.Is(err, ErrNotSealed) {
		t.Fatalf("Open(unsealed) error = %v", err)
	}
	if openedAgain != "" {
		t.Fatalf("Open(unsealed) = %q", openedAgain)
	}
}

func TestOpen_With_TheWrongKeyFailsClosed(t *testing.T) {
	t.Parallel()

	sealed, err := Seal(stubCipher{prefix: "one:"}, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	got, err := Open(stubCipher{prefix: "two:"}, sealed)
	if err == nil {
		t.Fatal("decrypt with the wrong key succeeded")
	}
	if got != "" {
		t.Fatalf("Open(wrong key) = %q", got)
	}
}

func TestSeal_Open_RoundTrip(t *testing.T) {
	t.Parallel()

	const secret = "JBSWY3DPEHPK3PXP"
	cipher := stubCipher{prefix: "aes:"}
	sealed, err := Seal(cipher, secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if sealed == secret || !IsSealed(sealed) {
		t.Fatalf("Seal = %q", sealed)
	}
	opened, err := Open(cipher, sealed)
	if err != nil || opened != secret {
		t.Fatalf("Open = %q, %v", opened, err)
	}
}

func TestSeal_Is_IdempotentAndEmptySafe(t *testing.T) {
	t.Parallel()

	cipher := stubCipher{prefix: "aes:"}
	if got, err := Seal(cipher, ""); err != nil || got != "" {
		t.Fatalf("Seal empty = %q, %v", got, err)
	}
	if got, err := Open(cipher, ""); err != nil || got != "" {
		t.Fatalf("Open empty = %q, %v", got, err)
	}
	sealed, err := Seal(cipher, "JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	again, err := Seal(cipher, sealed)
	if err != nil || again != sealed {
		t.Fatalf("Seal(sealed) = %q, %v", again, err)
	}
}
