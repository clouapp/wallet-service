package rules

import (
	"context"
	"testing"
)

// The holder document of an email, phone or random key is a valid CPF or a
// valid CNPJ — numeric or, since 31/07/2026, alphanumeric (spec §19.1) — with
// its check digits right, as one word. Any other word is no document: the
// installed iOS builds strip the letters of an alphanumeric CNPJ and send
// what is left, which named another holder upstream.
func TestRecipientDocumentRule_Passes(t *testing.T) {
	r := &RecipientDocumentRule{}
	for value, want := range map[string]bool{
		"39053344705":     true,
		"11222333000181":  true,
		"12ABC34501DE35":  true,
		"12abc34501de35":  true,
		"123450135":       false,
		"12345678901":     false,
		"11111111111":     false,
		"11222333000180":  false,
		"12ABC34501DE36":  false,
		"ABCDEFGHIJK":     false,
		"12ABC34501DEXY":  false,
		"12ABC34501DE3":   false,
		"A1234567890123B": false,
		"390.533.447-05":  false,
		"":                false,
	} {
		if got := r.Passes(context.Background(), mapData{}, value); got != want {
			t.Errorf("Passes(%q) = %v, want %v", value, got, want)
		}
	}
	if r.Passes(context.Background(), mapData{}, 39053344705) {
		t.Error("expected a number to be refused")
	}
}
