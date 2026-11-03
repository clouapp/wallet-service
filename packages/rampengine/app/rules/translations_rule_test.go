package rules

import (
	"context"
	"testing"
)

// The binder casts whatever a translated field holds to text, so an object or
// a number in one locale would arrive as "" or "42"; the rule refuses them
// before that happens.
func TestTranslationsRule_Passes(t *testing.T) {
	r := &TranslationsRule{}
	for _, tc := range []struct {
		name  string
		val   any
		wants bool
	}{
		{"text in every locale", map[string]any{"pt-BR": "Pix", "en": "Pix"}, true},
		{"blank text is still text", map[string]any{"pt-BR": "Pix", "en": ""}, true},
		{"no locale", map[string]any{}, true},
		{"an object in one locale", map[string]any{"pt-BR": "Pix", "en": map[string]any{"x": 1}}, false},
		{"a number", map[string]any{"pt-BR": float64(42)}, false},
		{"a boolean", map[string]any{"pt-BR": "Pix", "en": true}, false},
		{"a list", map[string]any{"pt-BR": []any{"Pix"}}, false},
		{"null", map[string]any{"pt-BR": "Pix", "en": nil}, false},
		{"not an object", "Pix", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := r.Passes(context.Background(), mapData{}, tc.val); got != tc.wants {
				t.Errorf("Passes(%v) = %v, want %v", tc.val, got, tc.wants)
			}
		})
	}
}

func TestTranslationsRule_Signature(t *testing.T) {
	if got := (&TranslationsRule{}).Signature(); got != "translations" {
		t.Errorf("Signature() = %q, want %q", got, "translations")
	}
}
