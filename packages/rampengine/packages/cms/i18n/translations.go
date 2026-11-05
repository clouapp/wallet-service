// Package i18n is the CMS's shared base for translated content: the text a
// translatable field holds in each locale, the configured locales, how a read
// resolves an entity to one of them, and the rules a write must pass.
package i18n

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
)

// Translations is one translatable field: its text in each locale, keyed by
// the locale as the cms config spells it ("pt-BR"). It is stored as a jsonb
// object. A blank text is the same as no text, everywhere in this package.
type Translations map[string]string

// Has reports whether t holds non-blank text in locale.
func (t Translations) Has(locale string) bool {
	return strings.TrimSpace(t[locale]) != ""
}

// Only returns the non-blank entries of t whose locale is in locales.
func (t Translations) Only(locales []string) Translations {
	only := make(Translations, len(locales))
	for _, locale := range locales {
		if t.Has(locale) {
			only[locale] = t[locale]
		}
	}
	return only
}

// GormDataType names the column type, so gorm reads the map as one value and
// never tries to make a relation of it.
func (Translations) GormDataType() string { return "jsonb" }

// Value writes the non-blank entries as a jsonb object; with none it writes
// "{}", which the NOT NULL columns accept.
func (t Translations) Value() (driver.Value, error) {
	kept := make(map[string]string, len(t))
	for locale, text := range t {
		if strings.TrimSpace(text) != "" {
			kept[locale] = text
		}
	}
	encoded, err := json.Marshal(kept)
	if err != nil {
		return nil, fmt.Errorf("i18n: encoding translations: %w", err)
	}
	return string(encoded), nil
}

// Scan reads a jsonb object, which the driver hands over as bytes or as text.
// NULL reads as no translations.
func (t *Translations) Scan(src any) error {
	var raw []byte
	switch value := src.(type) {
	case nil:
		*t = nil
		return nil
	case []byte:
		raw = value
	case string:
		raw = []byte(value)
	default:
		return fmt.Errorf("i18n: cannot scan %T into Translations", src)
	}
	var decoded map[string]string
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return fmt.Errorf("i18n: decoding translations: %w", err)
	}
	*t = decoded
	return nil
}

// Resolve returns the first locale of chain in which every field has text.
// Fallback is per entity, never per field: a question and its answer always
// come back in the same language.
func Resolve(chain []string, fields ...Translations) (string, bool) {
	for _, locale := range chain {
		if complete(locale, fields) {
			return locale, true
		}
	}
	return "", false
}

// complete reports whether every field has text in locale.
func complete(locale string, fields []Translations) bool {
	for _, field := range fields {
		if !field.Has(locale) {
			return false
		}
	}
	return true
}
