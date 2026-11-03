package rules

import (
	"context"

	"github.com/goravel/framework/contracts/validation"
)

// TranslationsRule accepts a translated CMS field: an object whose every value
// is text. The binder casts whatever a value holds to a string, so without it
// an object or a number in one locale arrived as "" or "42" and, under PUT,
// quietly replaced that locale's text. Which locales may appear is the CMS's
// to judge; this checks the shape.
type TranslationsRule struct{}

func (r *TranslationsRule) Signature() string { return "translations" }

func (r *TranslationsRule) Passes(_ context.Context, _ validation.Data, val any, _ ...any) bool {
	texts, ok := val.(map[string]any)
	if !ok {
		return false
	}
	for _, text := range texts {
		if _, ok := text.(string); !ok {
			return false
		}
	}
	return true
}

func (r *TranslationsRule) Message(_ context.Context) string {
	return "every translation must be text"
}
