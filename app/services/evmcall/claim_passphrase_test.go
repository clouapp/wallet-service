package evmcall

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileClaimer_ClaimsATagOnlyOnce(t *testing.T) {
	claimer := FileClaimer{Dir: filepath.Join(t.TempDir(), "locks")}
	path, err := claimer.Claim(testTag, []byte(`{"n":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := claimer.Claim(testTag, []byte(`{"n":2}`)); !errors.Is(err, ErrAlreadyClaimed) {
		t.Fatalf("second claim: %v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != `{"n":1}` {
		t.Fatalf("claim was overwritten: %s", content)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != claimFileMode {
		t.Fatalf("claim mode %v", info.Mode().Perm())
	}
	resultPath, err := claimer.Record(testTag, []byte(`{"outcome":"success"}`))
	if err != nil || !strings.HasSuffix(resultPath, resultSuffix) {
		t.Fatalf("record %s %v", resultPath, err)
	}
	if _, err := (FileClaimer{}).Claim(testTag, nil); err == nil {
		t.Fatal("claimer without a directory: expected an error")
	}
}

func TestReadPassphraseLine(t *testing.T) {
	if got, err := ReadPassphraseLine(strings.NewReader(testPassphrase + "\r\nrest")); err != nil || got != testPassphrase {
		t.Fatalf("got %q err %v", got, err)
	}
	if got, err := ReadPassphraseLine(strings.NewReader(testPassphrase)); err != nil || got != testPassphrase {
		t.Fatalf("no newline: got %q err %v", got, err)
	}
	if _, err := ReadPassphraseLine(strings.NewReader("short\n")); err == nil {
		t.Fatal("short passphrase: expected an error")
	}
}
