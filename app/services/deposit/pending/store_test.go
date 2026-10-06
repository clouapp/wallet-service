package pending

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testChain = "tpend"

func sampleEntry(block uint64, attempts int, at time.Time) Entry {
	return Entry{
		Chain: testChain, Block: block, TxHashes: []string{fmt.Sprintf("tx-%d", block)},
		ErrorClass: ClassDatabase, LastError: "insert tx: connection refused", Attempts: attempts,
		FirstFailedAt: at, LastFailedAt: at, NextRetryAt: at.Add(30 * time.Second),
	}
}

func TestStore_Entry_Validate(t *testing.T) {
	now := time.Now().UTC()
	valid := sampleEntry(10, 1, now)
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid entry rejected: %v", err)
	}
	cases := map[string]func(*Entry){
		"blank chain":         func(e *Entry) { e.Chain = "" },
		"padded chain":        func(e *Entry) { e.Chain = " sol" },
		"path in chain":       func(e *Entry) { e.Chain = "../sol" },
		"block zero":          func(e *Entry) { e.Block = 0 },
		"no attempt":          func(e *Entry) { e.Attempts = 0 },
		"missing retry time":  func(e *Entry) { e.NextRetryAt = time.Time{} },
		"missing failed time": func(e *Entry) { e.FirstFailedAt = time.Time{} },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			entry := valid
			mutate(&entry)
			if err := entry.Validate(); !errors.Is(err, ErrInvalidEntry) {
				t.Fatalf("expected ErrInvalidEntry, got %v", err)
			}
		})
	}
}

func TestDue_Sorts_ByNextRetryAndSkipsFutureEntries(t *testing.T) {
	now := time.Now().UTC()
	late := sampleEntry(5, 1, now.Add(-10*time.Second))
	early := sampleEntry(9, 1, now.Add(-time.Minute))
	future := sampleEntry(7, 1, now)
	due := Due([]Entry{late, future, early}, now.Add(25*time.Second))
	if len(due) != 2 || due[0].Block != 9 || due[1].Block != 5 {
		t.Fatalf("unexpected due entries %+v", due)
	}
}

func TestStore_Truncate_Error(t *testing.T) {
	if got := TruncateError(strings.Repeat("x", MaxErrorLength+20)); len(got) != MaxErrorLength {
		t.Fatalf("expected %d bytes, got %d", MaxErrorLength, len(got))
	}
	if got := TruncateError("short"); got != "short" {
		t.Fatalf("short messages must be kept, got %q", got)
	}
}

func TestFile_Store_PutReplaceDeleteSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().UTC().Truncate(time.Millisecond)
	store, err := NewFileStore(FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Entry{sampleEntry(20, 1, now), sampleEntry(10, 1, now), sampleEntry(20, 2, now.Add(time.Second))} {
		if err := store.Put(ctx, entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Delete(ctx, testChain, 10); err != nil {
		t.Fatal(err)
	}

	reopened, err := NewFileStore(FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := reopened.List(ctx, testChain)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Block != 20 || entries[0].Attempts != 2 || !entries[0].LastFailedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("expected only block 20 at attempt 2 after a reopen, got %+v", entries)
	}
	info, err := os.Stat(filepath.Join(dir, testChain+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != filePermissions {
		t.Fatalf("log must be private (%o), got %o", filePermissions, perm)
	}
}

func TestFile_Store_IgnoresATornLastRecordAndKeepsAppending(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().UTC()
	store, err := NewFileStore(FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(ctx, sampleEntry(30, 1, now)); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(dir, testChain+".jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := log.WriteString(`{"op":"put","block":31,"entry":{"chain":"tp`); err != nil {
		t.Fatal(err)
	}
	_ = log.Close()

	entries, err := store.List(ctx, testChain)
	if err != nil || len(entries) != 1 || entries[0].Block != 30 {
		t.Fatalf("a torn last record must be ignored, got %+v, %v", entries, err)
	}
	if err := store.Put(ctx, sampleEntry(32, 1, now)); err != nil {
		t.Fatal(err)
	}
	entries, err = store.List(ctx, testChain)
	if err != nil || len(entries) != 2 || entries[0].Block != 30 || entries[1].Block != 32 {
		t.Fatalf("the next append must not merge with the torn record, got %+v, %v", entries, err)
	}
}

func TestFile_Store_RejectsACorruptRecordInTheMiddle(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, testChain+".jsonl"), []byte("garbage\n{\"op\":\"delete\",\"block\":1}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewFileStore(FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.List(ctx, testChain); err == nil {
		t.Fatal("a corrupt record that is not the torn tail must fail the read, not be skipped")
	}
}

func TestFile_Store_CompactsResolvedEntries(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	now := time.Now().UTC()
	store, err := NewFileStore(FileStoreDeps{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	const compactAfter = 40
	store.compactAfter = compactAfter
	if err := store.Put(ctx, sampleEntry(1, 1, now)); err != nil {
		t.Fatal(err)
	}
	for block := uint64(2); block <= compactAfter; block += 2 {
		if err := store.Put(ctx, sampleEntry(block, 1, now)); err != nil {
			t.Fatal(err)
		}
		if err := store.Delete(ctx, testChain, block); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(filepath.Join(dir, testChain+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// 41 records were written; compaction at the 40th left the two live puts, then
	// one delete followed.
	if lines := strings.Count(string(content), "\n"); lines != 3 {
		t.Fatalf("expected the log compacted to 3 records, has %d", lines)
	}
	entries, err := store.List(ctx, testChain)
	if err != nil || len(entries) != 1 || entries[0].Block != 1 {
		t.Fatalf("compaction must keep the live entry, got %+v, %v", entries, err)
	}
}

func TestNew_DurableStore_NeedsABackend(t *testing.T) {
	if _, err := NewDurableStore(DurableStoreDeps{}); err == nil {
		t.Fatal("expected an error without any backend")
	}
}
