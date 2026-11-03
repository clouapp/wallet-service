package i18n

import (
	"errors"
	"fmt"
	"maps"
	"slices"

	"golang.org/x/text/language"
)

// The configurations NewLocales refuses. The service provider answers them at
// boot, when the container first resolves the CMS.
var (
	ErrNoLocales           = errors.New("i18n: cms.locales is empty")
	ErrInvalidLocale       = errors.New("i18n: not a BCP 47 language tag")
	ErrDefaultNotSupported = errors.New("i18n: cms.default_locale is not one of cms.locales")
)

// The rules a write can break, as FieldError.Rule names them.
const (
	RuleRequired     = "required"
	RuleRequiredWith = "required_with"
	RuleLocale       = "locale"
)

// Field is one translatable field a write carries, named as the request names it.
type Field struct {
	Name  string
	Value Translations
}

// FieldError is the write rule a field broke. Field is "<name>.<locale>", the
// key a validation error map uses; Rule is one of the Rule* constants.
type FieldError struct {
	Field string
	Rule  string
}

func (e *FieldError) Error() string {
	return fmt.Sprintf("i18n: %s fails %s", e.Field, e.Rule)
}

// Locales is the configured set: the locales content may be written in, the
// one every entity must have, and the chain a read falls back along.
type Locales struct {
	supported []string
	matcher   language.Matcher
	def       string
	fallback  string
}

// NewLocales checks the configuration once. fallback is app.fallback_locale;
// outside supported it is allowed and simply never part of a chain.
func NewLocales(supported []string, def, fallback string) (*Locales, error) {
	if len(supported) == 0 {
		return nil, ErrNoLocales
	}
	tags := make([]language.Tag, 0, len(supported))
	for _, locale := range supported {
		tag, err := language.Parse(locale)
		if err != nil {
			return nil, fmt.Errorf("%w: %q", ErrInvalidLocale, locale)
		}
		tags = append(tags, tag)
	}
	if !slices.Contains(supported, def) {
		return nil, fmt.Errorf("%w: %q", ErrDefaultNotSupported, def)
	}
	return &Locales{
		supported: slices.Clone(supported),
		matcher:   language.NewMatcher(tags),
		def:       def,
		fallback:  fallback,
	}, nil
}

// Supported is the configured locales, in config order.
func (l *Locales) Supported() []string { return slices.Clone(l.supported) }

// Default is the locale every entity must have.
func (l *Locales) Default() string { return l.def }

// Match picks the configured locale an Accept-Language header asks for, the
// way BCP 47 matching does: "pt" and "pt-PT" find "pt-BR", "es-MX" finds "es".
// It reports false for a header that matches none, and for an empty or
// malformed one.
func (l *Locales) Match(acceptLanguage string) (string, bool) {
	tags, _, err := language.ParseAcceptLanguage(acceptLanguage)
	if err != nil || len(tags) == 0 {
		return "", false
	}
	_, index, confidence := l.matcher.Match(tags...)
	if confidence == language.No {
		return "", false
	}
	return l.supported[index], true
}

// Chain is the order a read for locale tries: locale, then the app's
// fallback, then the default, keeping only configured locales, each once.
func (l *Locales) Chain(locale string) []string {
	chain := make([]string, 0, 3)
	for _, candidate := range []string{locale, l.fallback, l.def} {
		if slices.Contains(l.supported, candidate) && !slices.Contains(chain, candidate) {
			chain = append(chain, candidate)
		}
	}
	return chain
}

// Missing lists, in config order, the configured locales in which not every
// field has text: the tabs an editor still has to fill.
func (l *Locales) Missing(fields ...Translations) []string {
	missing := make([]string, 0, len(l.supported))
	for _, locale := range l.supported {
		if !complete(locale, fields) {
			missing = append(missing, locale)
		}
	}
	return missing
}

// Validate applies the write rules and returns the first violation as a
// *FieldError, checking fields in the order given:
//  1. every locale with text is configured (RuleLocale);
//  2. every field has text in the default locale (RuleRequired);
//  3. a locale with text in one field has text in all of them (RuleRequiredWith).
func (l *Locales) Validate(fields ...Field) error {
	for _, field := range fields {
		for _, locale := range slices.Sorted(maps.Keys(field.Value)) {
			if field.Value.Has(locale) && !slices.Contains(l.supported, locale) {
				return &FieldError{Field: field.Name + "." + locale, Rule: RuleLocale}
			}
		}
	}
	for _, field := range fields {
		if !field.Value.Has(l.def) {
			return &FieldError{Field: field.Name + "." + l.def, Rule: RuleRequired}
		}
	}
	for _, locale := range l.supported {
		if !slices.ContainsFunc(fields, func(f Field) bool { return f.Value.Has(locale) }) {
			continue
		}
		for _, field := range fields {
			if !field.Value.Has(locale) {
				return &FieldError{Field: field.Name + "." + locale, Rule: RuleRequiredWith}
			}
		}
	}
	return nil
}
