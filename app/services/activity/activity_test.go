package activity

import (
	"strings"
	"testing"

	"github.com/macrowallets/waas/app/models"
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
