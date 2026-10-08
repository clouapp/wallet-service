package keyexport

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate_Archive_Password(t *testing.T) {
	strong := "Correct-Horse-Battery-7"
	cases := []struct {
		name          string
		first, second string
		want          error
	}{
		{"accepts a strong password", strong, strong, nil},
		{"refuses a mismatch", strong, strong + "!", ErrArchivePasswordMismatch},
		{"refuses one character short", "Abcdefgh-123456", "Abcdefgh-123456", ErrArchivePasswordShort},
		{"refuses few distinct characters", strings.Repeat("ab12", 6), strings.Repeat("ab12", 6), ErrArchivePasswordWeak},
		{"refuses a run of one character", strings.Repeat("a", 40), strings.Repeat("a", 40), ErrArchivePasswordWeak},
		{"refuses control characters", "Abcdefgh-1234567\t", "Abcdefgh-1234567\t", ErrArchivePasswordEncoding},
		{"refuses invalid utf-8", "Abcdefgh-1234567\xff", "Abcdefgh-1234567\xff", ErrArchivePasswordEncoding},
		{"refuses an absurdly long password", strings.Repeat("Abcdefgh-123", 100), strings.Repeat("Abcdefgh-123", 100), ErrArchivePasswordLong},
		{"counts characters, not bytes", "çãõéíóúâêôàüñ¿¡ß", "çãõéíóúâêôàüñ¿¡ß", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateArchivePassword([]byte(tc.first), []byte(tc.second)); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	if MinArchivePasswordLength < 16 {
		t.Fatal("minimum archive password length must stay at 16 or more")
	}
}

func TestRead_ArchivePassword_AsksTwiceRetriesAndGivesUp(t *testing.T) {
	strong := []byte("Correct-Horse-Battery-7")
	terminal := &scriptedTerminal{secrets: [][]byte{[]byte("short"), []byte("short"), strong, strong}}
	password, err := ReadArchivePassword(terminal)
	if err != nil || string(password) != string(strong) {
		t.Fatalf("password %q err %v", password, err)
	}
	if len(terminal.prompts) != 4 || len(terminal.notices) != 1 || strings.Contains(terminal.notices[0], "short") {
		t.Fatalf("prompts %v notices %v (a notice must not echo the password)", terminal.prompts, terminal.notices)
	}

	mismatched := &scriptedTerminal{}
	for i := 0; i < maxArchivePasswordAttempts; i++ {
		mismatched.secrets = append(mismatched.secrets, strong, []byte("Different-Password-99"))
	}
	if _, err := ReadArchivePassword(mismatched); !errors.Is(err, ErrArchivePasswordMismatch) {
		t.Fatalf("mismatch every time must fail, got %v", err)
	}
	if _, err := ReadArchivePassword(&scriptedTerminal{}); !errors.Is(err, ErrAborted) {
		t.Fatalf("a terminal that goes away must abort, got %v", err)
	}
	if _, err := ReadArchivePassword(nil); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("no terminal must be refused, got %v", err)
	}
}

func TestRequire_Environment_Allowed(t *testing.T) {
	for _, env := range []string{"local", "testing", "staging", ""} {
		if err := RequireEnvironmentAllowed(env, false, nil); err != nil {
			t.Fatalf("APP_ENV=%q must be allowed: %v", env, err)
		}
	}
	for _, env := range []string{"production", "PRODUCTION", " prod "} {
		if err := RequireEnvironmentAllowed(env, false, &scriptedTerminal{lines: []string{ProductionConfirmationPhrase}}); !errors.Is(err, ErrProductionRefused) {
			t.Fatalf("APP_ENV=%q without --allow-production: got %v", env, err)
		}
	}
	if err := RequireEnvironmentAllowed("production", true, nil); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("production without a terminal: got %v", err)
	}
	if err := RequireEnvironmentAllowed("production", true, &scriptedTerminal{lines: []string{"yes"}}); !errors.Is(err, ErrProductionNotConfirmed) {
		t.Fatalf("wrong phrase: got %v", err)
	}
	if err := RequireEnvironmentAllowed("production", true, &scriptedTerminal{lines: []string{strings.ToLower(ProductionConfirmationPhrase)}}); !errors.Is(err, ErrProductionNotConfirmed) {
		t.Fatalf("phrase must match exactly: got %v", err)
	}
	if err := RequireEnvironmentAllowed("production", true, &scriptedTerminal{lines: []string{ProductionConfirmationPhrase + "\r"}}); err != nil {
		t.Fatalf("exact phrase must pass: %v", err)
	}
}

func TestOpen_TTY_RefusesSomethingThatIsNotATerminal(t *testing.T) {
	notATerminal := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(notATerminal, []byte("Correct-Horse-Battery-7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := openTTYAt(notATerminal); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("a regular file must not count as a terminal, got %v", err)
	}
	if _, err := openTTYAt(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, ErrNoTerminal) {
		t.Fatalf("a missing terminal must be refused, got %v", err)
	}
}
