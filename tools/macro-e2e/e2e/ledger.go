package e2e

import (
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/pkg/pyjson"
)

const (
	ledgerEntriesKey    = "entries"
	ledgerRecordedAtKey = "recordedAt"
	ledgerTagKey        = "tag"
	jsonIndent          = 2
	pythonNone          = "None"
)

// FundingLedger is <state dir>/funding/ledger.json, shared with the Markets e2e. Its
// key order and layout (json.dumps(indent=2) + "\n") are preserved on every write.
type FundingLedger struct {
	Path string
	Now  func() time.Time
}

// Load reads the ledger; it must exist and hold an "entries" list.
func (ledger FundingLedger) Load() (pyjson.Object, []any, error) {
	raw, err := os.ReadFile(ledger.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("missing funding ledger %s", ledger.Path)
	}
	document, err := decodeObject(raw)
	if err != nil {
		return nil, nil, fmt.Errorf("%s is not a JSON object: %w", ledger.Path, err)
	}
	entriesValue, _ := document.Get(ledgerEntriesKey)
	entries, isList := entriesValue.([]any)
	if !isList {
		return nil, nil, fmt.Errorf("%s has no entries list", ledger.Path)
	}
	return document, entries, nil
}

// HasTag reports whether an entry already uses tag.
func (ledger FundingLedger) HasTag(tag string) (bool, error) {
	_, entries, err := ledger.Load()
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if object, isObject := entry.(pyjson.Object); isObject && object.String(ledgerTagKey) == tag {
			return true, nil
		}
	}
	return false, nil
}

// Upsert drops the entries with tag, appends entry, and stamps the ledger recordedAt.
func (ledger FundingLedger) Upsert(tag string, entry pyjson.Object) error {
	document, entries, err := ledger.Load()
	if err != nil {
		return err
	}
	kept := make([]any, 0, len(entries)+1)
	for _, existing := range entries {
		if object, isObject := existing.(pyjson.Object); isObject && object.String(ledgerTagKey) == tag {
			continue
		}
		kept = append(kept, existing)
	}
	kept = append(kept, entry)
	updated := document.Set(ledgerEntriesKey, kept).Set(ledgerRecordedAtKey, NowISO(ledger.Now()))
	return writeIndentedJSON(ledger.Path, updated)
}

func writeIndentedJSON(path string, value any) error {
	content, err := pyjson.DumpsIndent(value, jsonIndent)
	if err != nil {
		return err
	}
	return WritePrivateFile(path, []byte(content+"\n"))
}

func decodeObject(raw []byte) (pyjson.Object, error) {
	decoded, err := pyjson.Decode(raw)
	if err != nil {
		return nil, err
	}
	object, isObject := decoded.(pyjson.Object)
	if !isObject {
		return nil, fmt.Errorf("not a JSON object")
	}
	return object, nil
}

// pythonValueRepr renders a decoded JSON value like Python's repr in error messages.
func pythonValueRepr(value any) string {
	switch typed := value.(type) {
	case nil:
		return pythonNone
	case string:
		return pythonRepr(typed)
	default:
		encoded, err := pyjson.Dumps(typed, pyjson.Default)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return encoded
	}
}

func truncateRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit])
}

func lastRunes(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[len(runes)-limit:])
}
