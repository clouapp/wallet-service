package activity

import (
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/policies"
)

func TestSettings_Change_KeepsFieldNamesAndDropsValues(t *testing.T) {
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

func TestChain_Thresholds_ChangeRecordsTheChainAndFieldNames(t *testing.T) {
	t.Parallel()

	const amount = "999000000000000000"
	meta, err := ChainThresholdsChange("eth", []string{"gas_readiness_threshold_raw"})
	if err != nil {
		t.Fatalf("chain thresholds: %v", err)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(encoded, amount) {
		t.Fatalf("metadata stored an amount: %s", encoded)
	}
	if !strings.Contains(encoded, `"key":"eth"`) || !strings.Contains(encoded, `"gas_readiness_threshold_raw"`) {
		t.Fatalf("metadata = %s", encoded)
	}
}

func TestChain_RPC_ChangeRecordsTheFieldNameOnly(t *testing.T) {
	t.Parallel()

	const endpoint = "https://dial.example/v2/secret-path"
	meta, err := ChainRPCChange("eth")
	if err != nil {
		t.Fatal("chain rpc change failed")
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatal("encode failed")
	}
	if strings.Contains(encoded, endpoint) || strings.Contains(encoded, "http") || strings.Contains(encoded, "secret-path") {
		t.Fatal("metadata stored an endpoint")
	}
	if !strings.Contains(encoded, `"key":"eth"`) || !strings.Contains(encoded, `"rpc_url"`) {
		t.Fatal("metadata missing the chain or the field")
	}
	if _, err := ChainRPCChange(endpoint); err == nil {
		t.Fatal("a url was accepted as a chain id")
	}
}

func TestFeature_Audit_KeepsChangedBooleansAndDropsAnUnchangedFlag(t *testing.T) {
	t.Parallel()

	const secret = "do-not-store-secret"
	meta, changed, err := FeatureAudit(
		map[string]bool{"sweep-enabled": true, "withdrawals-enabled": true},
		map[string]bool{"sweep-enabled": false, "withdrawals-enabled": true},
	)
	if err != nil || !changed {
		t.Fatalf("audit changed=%v err=%v", changed, err)
	}
	encoded, err := meta.Encode()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if encoded != `{"after":{"sweep-enabled":false},"before":{"sweep-enabled":true}}` {
		t.Fatalf("metadata = %s", encoded)
	}
	if strings.Contains(encoded, secret) || strings.Contains(encoded, "withdrawals-enabled") {
		t.Fatalf("metadata recorded an unchanged flag or a secret: %s", encoded)
	}
	target, err := FeatureAuditTarget(meta)
	if err != nil || target != "sweep-enabled" {
		t.Fatalf("target = %q err=%v", target, err)
	}

	several, changed, err := FeatureAudit(
		map[string]bool{"sweep-enabled": true, "wallet-creation-enabled": true},
		map[string]bool{"sweep-enabled": false, "wallet-creation-enabled": false},
	)
	if err != nil || !changed {
		t.Fatalf("several changed=%v err=%v", changed, err)
	}
	target, err = FeatureAuditTarget(several)
	if err != nil || target != "features" {
		t.Fatalf("several target = %q err=%v", target, err)
	}

	if _, changed, err := FeatureAudit(
		map[string]bool{"sweep-enabled": false},
		map[string]bool{"sweep-enabled": false},
	); err != nil || changed {
		t.Fatalf("unchanged changed=%v err=%v", changed, err)
	}

	for _, bad := range []models.ActivityMetadata{
		{"before": map[string]any{"withdrawals-enabled": secret}, "after": map[string]bool{"withdrawals-enabled": false}},
		{"before": map[string]any{"withdrawals-enabled": 1.0}, "after": map[string]bool{"withdrawals-enabled": false}},
		{"before": map[string]any{"withdrawals-enabled": "false"}, "after": map[string]bool{"withdrawals-enabled": true}},
		{"before": map[string]bool{"signing-secret": true}, "after": map[string]bool{"signing-secret": false}},
		{"before": map[string]bool{"withdrawals-enabled": true}, "after": map[string]bool{"withdrawals-enabled": true}},
	} {
		if _, err := bad.Encode(); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}

	if ActionUserFeaturesUpdated != "user.features_updated" || ActionChainFeaturesUpdated != "chain.features_updated" {
		t.Fatal("user and chain audit names are not declared")
	}
}

func TestFeature_Audit_AcceptsEveryCatalogFlag(t *testing.T) {
	t.Parallel()

	for key := range map[string]struct{}{
		"api-request-signature-required": {},
		"deposit-scan-enabled":           {},
		"sweep-enabled":                  {},
		"user-2fa-required":              {},
		"wallet-creation-enabled":        {},
		"webhook-delivery-enabled":       {},
		"withdrawals-enabled":            {},
	} {
		meta, changed, err := FeatureAudit(map[string]bool{key: false}, map[string]bool{key: true})
		if err != nil || !changed {
			t.Fatalf("%s changed=%v err=%v", key, changed, err)
		}
		encoded, err := meta.Encode()
		if err != nil {
			t.Fatalf("%s encode: %v", key, err)
		}
		if !strings.Contains(encoded, `"`+key+`":false`) || !strings.Contains(encoded, `"`+key+`":true`) {
			t.Fatalf("%s metadata = %s", key, encoded)
		}
	}
}

func TestMetadata_Rejects_SecretKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"value", "secret", "token", "password", "signing_secret"} {
		_, err := models.ActivityMetadata{key: "hidden"}.Encode()
		if err == nil {
			t.Fatalf("key %q was accepted", key)
		}
	}
}

func TestToken_Created_KeepsNameAndPermissionsAndDropsTheSecret(t *testing.T) {
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

func TestToken_Created_RejectsAPermissionOutsideTheCatalog(t *testing.T) {
	t.Parallel()

	if _, err := TokenCreated("ci", `["wallets:read"]`); err == nil {
		t.Fatal("a permission outside the catalog was accepted")
	}
	if _, err := TokenCreated("  ", `["wallets.read"]`); err == nil {
		t.Fatal("a blank token name was accepted")
	}
}

func TestToken_Revoked_OmitsALegacyPermissionValue(t *testing.T) {
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

func TestToken_Permission_AllowlistMatchesTheAPICatalog(t *testing.T) {
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

func TestMember_Change_NamesTheStatusAndKeepsTheNewRole(t *testing.T) {
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

func TestNamed_Events_OmitSecretsHashesLimitsAndAmounts(t *testing.T) {
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
	sessions, err := SessionsRevoked()
	if err != nil {
		t.Fatalf("sessions revoked: %v", err)
	}
	invited, err := MemberInvited("auditor")
	if err != nil {
		t.Fatalf("member invited: %v", err)
	}
	accepted, err := InviteAccepted("user")
	if err != nil {
		t.Fatalf("invite accepted: %v", err)
	}
	for _, meta := range []models.ActivityMetadata{removed, reset, cancelled, sessions, invited, accepted} {
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
	if sessions["key"] != "sessions" || invited["role"] != "auditor" || accepted["role"] != "user" {
		t.Fatalf("sessions=%v invited=%v accepted=%v", sessions, invited, accepted)
	}
	if _, err := MemberInvited("token_hash"); err == nil {
		t.Fatal("a role that is not an account role was accepted")
	}
}
