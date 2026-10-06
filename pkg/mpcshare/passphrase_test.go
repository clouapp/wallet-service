package mpcshare

import "testing"

func TestDiscard_Passphrase_DropsTheValue(t *testing.T) {
	value := "not-a-real-passphrase"
	DiscardPassphrase(&value)
	if value != "" {
		t.Fatal("passphrase was kept")
	}
	DiscardPassphrase(nil)
	DiscardPassphrase(&value)
}
