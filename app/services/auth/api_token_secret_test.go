package auth_test

import (
	"strings"
	"testing"

	authsvc "github.com/macrowallets/waas/app/services/auth"
)

func TestGenerate_API_TokenSecretIs32BytesHex(t *testing.T) {
	t.Parallel()

	svc := authsvc.NewService(nil)
	first, err := svc.GenerateAPITokenSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	second, err := svc.GenerateAPITokenSecret()
	if err != nil {
		t.Fatalf("generate second secret: %v", err)
	}
	if len(first) != 64 || strings.ToLower(first) != first {
		t.Fatal("secret must be 64 lowercase hex characters")
	}
	if first == second {
		t.Fatal("two secrets must differ")
	}
	hash := svc.HashAPITokenSecret(first)
	if hash == first || !authsvc.IsAPITokenSecretHash(hash) {
		t.Fatal("stored form must be the sha256 digest, not the secret")
	}
	if !authsvc.APITokenSecretMatches(first, hash) {
		t.Fatal("digest does not match the secret")
	}
	if authsvc.APITokenSecretMatches(second, hash) {
		t.Fatal("a different secret matched the digest")
	}
}

func TestAPI_Token_HashAcceptsLegacyStoredForm(t *testing.T) {
	t.Parallel()

	svc := authsvc.NewService(nil)
	legacyIDHash := svc.HashToken("11111111-1111-4111-8111-111111111111")
	placeholder := "test-hash-already-stored"

	if authsvc.IsAPITokenSecretHash(legacyIDHash) || authsvc.IsAPITokenSecretHash(placeholder) {
		t.Fatal("legacy stored form was classified as a secret digest")
	}
	if !authsvc.APITokenHashAccepts("", legacyIDHash) || !authsvc.APITokenHashAccepts("", placeholder) {
		t.Fatal("a JWT without a secret claim must still match the stored form")
	}
	if authsvc.APITokenHashAccepts("presented-secret", legacyIDHash) {
		t.Fatal("a secret claim must not authenticate against the legacy stored form")
	}
	if authsvc.APITokenHashAccepts("", "") {
		t.Fatal("an empty stored hash must be rejected")
	}
}

func TestAPI_Token_HashRejectsSecretDigestWithoutTheClaim(t *testing.T) {
	t.Parallel()

	svc := authsvc.NewService(nil)
	secret, err := svc.GenerateAPITokenSecret()
	if err != nil {
		t.Fatalf("generate secret: %v", err)
	}
	hash := svc.HashAPITokenSecret(secret)
	if authsvc.APITokenHashAccepts("", hash) {
		t.Fatal("a secret digest must not authenticate without the secret claim")
	}
	if !authsvc.APITokenHashAccepts(secret, strings.ToUpper(hash)) {
		t.Fatal("digest comparison must accept equivalent hex")
	}
}
