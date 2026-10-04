package settings

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestSecretPairsNameTheSMTPPasswordAndNotTheHost(t *testing.T) {
	t.Parallel()

	sawPassword := false
	for _, pair := range SecretPairs() {
		if len(pair) != 2 {
			t.Fatalf("pair %#v", pair)
		}
		if pair[0] == groupMailSMTP && pair[1] == keyMailPassword {
			sawPassword = true
		}
		if pair[1] == keyMailHost {
			t.Fatal("host was treated as a secret")
		}
	}
	if !sawPassword {
		t.Fatal("mail_smtp password was not a secret pair")
	}
}

func TestSettingsAuditImagesRecordValueSetForASecret(t *testing.T) {
	t.Parallel()

	const ciphertext = "enc:v1:ciphertext-that-must-not-be-copied"
	definition := Definition{Key: keyMailPassword, Secret: true}
	oldImage, newImage := settingsAuditImages(nil, groupMailSMTP, definition, map[string]string{
		keyMailPassword: ciphertext,
	}, ciphertext)

	if oldImage["valueSet"] != true || newImage["valueSet"] != true {
		t.Fatalf("valueSet old=%v new=%v", oldImage["valueSet"], newImage["valueSet"])
	}
	if _, recorded := oldImage["value"]; recorded {
		t.Fatal("old value was recorded")
	}
	if _, recorded := newImage["value"]; recorded {
		t.Fatal("new value was recorded")
	}
	encoded, err := json.Marshal([]any{oldImage, newImage})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "enc:v1:") || strings.Contains(string(encoded), "ciphertext") {
		t.Fatal("the secret text was copied into the audit image")
	}
	if oldImage["key"] != keyMailPassword || newImage["group"] != groupMailSMTP {
		t.Fatalf("identity old=%v new=%v", oldImage, newImage)
	}
}

func TestSettingsAuditImagesKeepANonSecretValue(t *testing.T) {
	t.Parallel()

	accountID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	definition := Definition{Key: keyMailHost}
	oldImage, newImage := settingsAuditImages(&accountID, groupMailSMTP, definition, map[string]string{
		keyMailHost: "old.example",
	}, "new.example")

	if oldImage["value"] != "old.example" || newImage["value"] != "new.example" {
		t.Fatalf("values old=%v new=%v", oldImage["value"], newImage["value"])
	}
	if oldImage["account_id"] != accountID.String() {
		t.Fatalf("account = %#v", oldImage["account_id"])
	}
	if _, extra := newImage["valueSet"]; extra {
		t.Fatal("a non-secret was recorded as valueSet")
	}
}
