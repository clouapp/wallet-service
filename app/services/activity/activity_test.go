package activity

import (
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

func TestSettingsChangeKeepsFieldNamesAndDropsValues(t *testing.T) {
	t.Parallel()

	const secret = "activity-signing-secret-do-not-store"
	meta, err := SettingsChange("account_webhooks", []string{"signing_secret"})
	if err != nil {
		t.Fatalf("settings change: %v", err)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(encoded, secret) || strings.Contains(encoded, "enc:v1:") {
		t.Fatalf("metadata stored a secret: %s", encoded)
	}
	if !strings.Contains(encoded, `"group":"account_webhooks"`) || !strings.Contains(encoded, `"signing_secret"`) {
		t.Fatalf("metadata = %s", encoded)
	}
}

func TestMetadataRejectsSecretKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"value", "secret", "token", "password", "signing_secret"} {
		_, err := models.ActivityMetadata{key: "hidden"}.Encode()
		if err == nil {
			t.Fatalf("key %q was accepted", key)
		}
	}
}

func TestTokenCreatedKeepsNameAndPermissionsAndDropsTheSecret(t *testing.T) {
	t.Parallel()

	const secret = "activity-token-secret-do-not-store"
	const digest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	meta, err := TokenCreated("CI Token", `["webhooks.write","wallets.read","wallets.read"]`)
	if err != nil {
		t.Fatalf("token created: %v", err)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(encoded, secret) || strings.Contains(encoded, digest) || strings.Contains(encoded, "daily_usd") {
		t.Fatalf("metadata stored a secret or a limit: %s", encoded)
	}
	if !strings.Contains(encoded, `"name":"CI Token"`) || !strings.Contains(encoded, `"permissions":["wallets.read","webhooks.write"]`) {
		t.Fatalf("metadata = %s", encoded)
	}
}

func TestTokenCreatedRejectsAPermissionOutsideTheCatalog(t *testing.T) {
	t.Parallel()

	if _, err := TokenCreated("ci", `["wallets:read"]`); err == nil {
		t.Fatal("a permission outside the catalog was accepted")
	}
	if _, err := TokenCreated("  ", `["wallets.read"]`); err == nil {
		t.Fatal("a blank token name was accepted")
	}
}

func TestTokenRevokedOmitsALegacyPermissionValue(t *testing.T) {
	t.Parallel()

	meta, err := TokenRevoked("legacy", "read")
	if err != nil {
		t.Fatalf("token revoked: %v", err)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(encoded, "read") || strings.Contains(encoded, "permissions") {
		t.Fatalf("legacy permissions were stored: %s", encoded)
	}
	if !strings.Contains(encoded, `"name":"legacy"`) {
		t.Fatalf("metadata = %s", encoded)
	}
}

func TestTokenPermissionAllowlistMatchesTheAPICatalog(t *testing.T) {
	t.Parallel()

	for _, name := range policies.APITokenPermissionCatalog() {
		meta, err := TokenCreated("ci", `["`+name+`"]`)
		if err != nil {
			t.Fatalf("catalog permission %s: %v", name, err)
		}
		if _, err := meta.Encode(); err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}
	}
}

func TestMemberChangeNamesTheStatusAndKeepsTheNewRole(t *testing.T) {
	t.Parallel()

	role := "admin"
	status := models.MembershipStatusSuspended
	action, meta, err := MemberChange(&role, &status)
	if err != nil {
		t.Fatalf("member change: %v", err)
	}
	if action != ActionMemberSuspended {
		t.Fatalf("action = %s", action)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(encoded, `"role":"admin"`) || !strings.Contains(encoded, `"status":"suspended"`) {
		t.Fatalf("metadata = %s", encoded)
	}
}

func TestNamedEventsOmitSecretsHashesLimitsAndAmounts(t *testing.T) {
	t.Parallel()

	const secret = "do-not-store-secret"
	removed, err := MemberRemoved("user")
	if err != nil {
		t.Fatalf("member removed: %v", err)
	}
	reset, err := MFAReset()
	if err != nil {
		t.Fatalf("mfa reset: %v", err)
	}
	cancelled, err := WithdrawalCancelled()
	if err != nil {
		t.Fatalf("withdrawal cancelled: %v", err)
	}
	for _, meta := range []models.ActivityMetadata{removed, reset, cancelled} {
		encoded, err := meta.Encode()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		for _, forbidden := range []string{secret, "token_hash", "spending_limit", "amount", "-1"} {
			if strings.Contains(encoded, forbidden) {
				t.Fatalf("metadata stored %q: %s", forbidden, encoded)
			}
		}
	}
	if removed["role"] != "user" || reset["enabled"] != false || cancelled["key"] != "cancelled" {
		t.Fatalf("removed=%v reset=%v cancelled=%v", removed, reset, cancelled)
	}
}
