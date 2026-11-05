package rules

import (
	"context"
	"testing"
)

// cnpj takes a CNPJ however it is typed, numeric or alphanumeric, masked or
// bare, its letters in either case (spec §19.1).
func TestCnpjRule_Passes(t *testing.T) {
	r := &CnpjRule{}
	for value, want := range map[string]bool{
		"33000167000101":     true,
		"33.000.167/0001-01": true,
		"12ABC34501DE35":     true,
		"12.abc.345/01de-35": true,
		"12ABC34501DE36":     false,
		"3300016700010":      false,
		"33000167000101x":    false,
		"39053344705":        false,
		"":                   false,
	} {
		if got := r.Passes(context.Background(), mapData{}, value); got != want {
			t.Errorf("Passes(%q) = %v, want %v", value, got, want)
		}
	}
	if r.Passes(context.Background(), mapData{}, 33000167000101) {
		t.Error("expected a number to be refused")
	}
	if r.Signature() != "cnpj" {
		t.Errorf("signature = %q", r.Signature())
	}
}
