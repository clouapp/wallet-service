package settings

import "testing"

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

func TestSealTagsCiphertextAndOpenRefusesPlaintext(t *testing.T) {
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
	if _, err := Open(stubCipher{prefix: "cipher:"}, plaintext); err == nil {
		t.Fatal("Open accepted an unsealed value")
	}
}
