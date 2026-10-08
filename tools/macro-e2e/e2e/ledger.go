package e2e

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/tools/internal/pyjson"
)

const (
	ledgerEntriesKey    = "entries"
	ledgerRecordedAtKey = "recordedAt"
	ledgerTagKey        = "tag"
	jsonIndent          = 2
	pythonNone          = "None"
	LedgerLockWait      = 30 * time.Second
	ledgerBackupInfix   = ".pre-reconcile-"
)

// FundingLedger is <state dir>/funding/ledger.json, shared with the Markets e2e. Its
// key order and layout (json.dumps(indent=2) + "\n") are preserved on every write.
// With LockPath set, every write holds that flock (<locks>/funding-ledger.lock).
type FundingLedger struct {
	Path     string
	LockPath string
	Now      func() time.Time
}

// Lock takes the ledger flock (a no-op lock without LockPath).
func (ledger FundingLedger) Lock(ctx context.Context) (*FileLock, error) {
	if ledger.LockPath == "" {
		return nil, nil
	}
	return AcquireFileLock(ctx, ledger.LockPath, LedgerLockWait)
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
	lock, err := ledger.Lock(context.Background())
	if err != nil {
		return err
	}
	defer lock.Release()
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

// ReplaceEntries rewrites the entries whose tag is a key of replacements in place (order
// kept) and stamps the ledger recordedAt. The caller holds Lock; every tag must exist.
func (ledger FundingLedger) ReplaceEntries(replacements map[string]pyjson.Object) error {
	document, entries, err := ledger.Load()
	if err != nil {
		return err
	}
	replaced := map[string]bool{}
	updated := make([]any, len(entries))
	for index, existing := range entries {
		updated[index] = existing
		object, isObject := existing.(pyjson.Object)
		if !isObject {
			continue
		}
		if replacement, found := replacements[object.String(ledgerTagKey)]; found {
			updated[index] = replacement
			replaced[object.String(ledgerTagKey)] = true
		}
	}
	for _, tag := range sortedKeys(replacements) {
		if !replaced[tag] {
			return fmt.Errorf("funding ledger has no entry %s", tag)
		}
	}
	return writeIndentedJSON(ledger.Path, document.Set(ledgerEntriesKey, updated).Set(ledgerRecordedAtKey, NowISO(ledger.Now())))
}

// Backup copies the ledger byte for byte to <ledger>.pre-reconcile-<UTC stamp> (0600)
// and returns that path; an existing backup is never overwritten.
func (ledger FundingLedger) Backup(now time.Time) (string, error) {
	raw, err := os.ReadFile(ledger.Path)
	if err != nil {
		return "", fmt.Errorf("missing funding ledger %s", ledger.Path)
	}
	backup := ledger.Path + ledgerBackupInfix + now.UTC().Format(environBackupStamp)
	handle, err := os.OpenFile(backup, os.O_WRONLY|os.O_CREATE|os.O_EXCL, PrivateFileMode)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", backup, err)
	}
	_, writeErr := handle.Write(raw)
	if err := errors.Join(writeErr, handle.Close()); err != nil {
		return "", fmt.Errorf("write %s: %w", backup, err)
	}
	return backup, nil
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
