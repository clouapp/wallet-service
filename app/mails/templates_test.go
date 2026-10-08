package mails

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbedded_Templates_ArePresentational(t *testing.T) {
	const resetLink = "https://example.test/reset-password?token=one-time-link"
	reset := (&PasswordResetMail{To: "person@example.test", ResetLink: resetLink}).Content().Html
	if !strings.Contains(reset, `href="`+resetLink+`"`) || !strings.Contains(reset, "Reset Password") {
		t.Fatal("reset template did not present the link the mailable built")
	}

	const inviteLink = "https://example.test/accept-invite?token=one-time-link"
	invite := (&UserInviteMail{
		To:          "person@example.test",
		InvitedBy:   "Ada",
		AccountName: "North",
		InviteLink:  inviteLink,
	}).Content().Html
	if !strings.Contains(invite, `href="`+inviteLink+`"`) || !strings.Contains(invite, "Ada") || !strings.Contains(invite, "North") {
		t.Fatal("invite template did not present the data the mailable built")
	}

	welcome := (&WelcomeMail{To: "person@example.test"}).Content().Html
	if !strings.Contains(welcome, "Welcome to Vault, there!") {
		t.Fatal("welcome mailable did not build the empty-name fallback")
	}
	named := (&WelcomeMail{FullName: "Ada <b>"}).Content().Html
	if strings.Contains(named, "<b>") || !strings.Contains(named, "Ada &lt;b&gt;") {
		t.Fatal("welcome template did not stay presentational")
	}

	settings := (&SettingsTestMail{To: "person@example.test"}).Content().Html
	if !strings.Contains(settings, "This is a test message from Vault.") {
		t.Fatal("settings test template did not render")
	}

	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("embedded templates = %d", len(entries))
	}
	for _, entry := range entries {
		raw, err := templateFS.ReadFile("templates/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		source := string(raw)
		for _, forbidden := range []string{"one-time-link", "passphrase", "private key", "BEGIN "} {
			if strings.Contains(strings.ToLower(source), strings.ToLower(forbidden)) {
				t.Fatalf("%s contains %q", entry.Name(), forbidden)
			}
		}
	}

	matches, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "<!DOCTYPE") {
			t.Fatalf("%s inlines HTML", name)
		}
	}
}
