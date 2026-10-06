package security

import (
	"errors"
	"strings"
	"testing"
)

func TestRedact_Text_HidesURLCredentialsRPCPathsAndAssignments(t *testing.T) {
	ConfigureRedaction(
		[]string{"rpc.example"},
		[]string{"fake-api-key-value"},
	)
	t.Cleanup(func() { ConfigureRedaction(nil, nil) })

	in := strings.Join([]string{
		"dial postgres://vault:fake-rpc-password@db.internal:5432/vault",
		"rpc https://user:fake-rpc-password@rpc.example/v2/fake-path-key-value?apikey=fake-query-key",
		"passphrase=fake-passphrase-value",
		"webhook_secret=fake-webhook-secret",
		"totp=fake-totp-secret",
		"private_key=fake-private-key-material",
		"share_a=fake-share-material",
		"api fake-api-key-value",
		"wallet 9f0e3c1a still visible",
	}, " ")
	got := RedactText(in)

	for _, secret := range []string{
		"fake-rpc-password",
		"fake-path-key-value",
		"fake-query-key",
		"fake-passphrase-value",
		"fake-webhook-secret",
		"fake-totp-secret",
		"fake-private-key-material",
		"fake-share-material",
		"fake-api-key-value",
	} {
		if strings.Contains(got, secret) {
			t.Fatalf("RedactText left %q in %q", secret, got)
		}
	}
	for _, keep := range []string{
		"postgres://db.internal:5432/vault?[redacted]",
		"https://rpc.example/[redacted]?[redacted]",
		"passphrase=[redacted]",
		"webhook_secret=[redacted]",
		"totp=[redacted]",
		"private_key=[redacted]",
		"share_a=[redacted]",
		"wallet 9f0e3c1a still visible",
	} {
		if !strings.Contains(got, keep) {
			t.Fatalf("RedactText dropped %q from %q", keep, got)
		}
	}
}

func TestRedact_Text_LeavesPlainTextAndRedactErrorNil(t *testing.T) {
	if got := RedactText("redis ping failed: timeout"); got != "redis ping failed: timeout" {
		t.Fatalf("RedactText mangled plain text: %q", got)
	}
	if RedactError(nil) != "" {
		t.Fatal("RedactError(nil) must be empty")
	}
	if got := RedactError(errors.New("passphrase=fake-passphrase-value")); strings.Contains(got, "fake-passphrase-value") {
		t.Fatalf("RedactError leaked the passphrase: %q", got)
	}
}

func TestRedact_Text_StripsPEMPrivateKey(t *testing.T) {
	in := "key -----BEGIN PRIVATE KEY-----\nFAKE-PRIVATE-KEY-BODY\n-----END PRIVATE KEY----- tail"
	got := RedactText(in)
	if strings.Contains(got, "FAKE-PRIVATE-KEY-BODY") {
		t.Fatalf("PEM body leaked: %q", got)
	}
	if !strings.Contains(got, "key [redacted] tail") {
		t.Fatalf("PEM was not replaced in place: %q", got)
	}
}
