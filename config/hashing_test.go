package config

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestHashing_Config_KeepsTheCostOfStoredHashes(t *testing.T) {
	doc := hashingDocument()
	if doc["driver"] != "bcrypt" {
		t.Fatalf("driver = %v, want bcrypt", doc["driver"])
	}
	bcryptDoc, _ := doc["bcrypt"].(map[string]any)
	if bcryptDoc["rounds"] != bcrypt.DefaultCost {
		t.Fatalf("rounds = %v, want %d: stored hashes would all need a rehash", bcryptDoc["rounds"], bcrypt.DefaultCost)
	}
}
