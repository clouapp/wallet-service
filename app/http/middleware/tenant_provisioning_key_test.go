package middleware

import "testing"

func TestProvisioningKeyRequiresAnIndependentLongExactSecret(t *testing.T) {
	key := "separate-provisioning-key-at-least-32-characters"
	if !ValidProvisionKey(key, key) {
		t.Fatal("matching key refused")
	}
	if ValidProvisionKey(key, key+"x") || ValidProvisionKey(key, "short") || ValidProvisionKey("", "") {
		t.Fatal("invalid provisioning key accepted")
	}
}
