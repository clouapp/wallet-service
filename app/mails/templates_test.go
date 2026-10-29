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
	if !strings.Contains(welcome, "Welcome to Macro Wallets, there!") {
		t.Fatal("welcome mailable did not build the empty-name fallback")
	}
	if strings.Contains(welcome, "<a href") {
		t.Fatal("a welcome without a login link drew a button")
	}
	const loginLink = "https://app.example.test/login"
	if signIn := (&WelcomeMail{LoginLink: loginLink}).Content().Html; !strings.Contains(signIn, `href="`+loginLink+`"`) || !strings.Contains(signIn, "Sign In") {
		t.Fatal("welcome template did not present the login link")
	}
	named := (&WelcomeMail{FullName: "Ada <b>"}).Content().Html
	if strings.Contains(named, "<b>") || !strings.Contains(named, "Ada &lt;b&gt;") {
		t.Fatal("welcome template did not stay presentational")
	}

	settings := (&SettingsTestMail{To: "person@example.test"}).Content().Html
	if !strings.Contains(settings, "This is a test message from Macro Wallets.") {
		t.Fatal("settings test template did not render")
	}

	entries, err := templateFS.ReadDir("templates")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Fatalf("embedded templates = %d", len(entries))
	}
	for _, entry := range entries {
		if entry.Name() != "layout.html" {
			body := render(entry.Name(), sampleData(entry.Name()))
			if !strings.Contains(body, "Macro Wallets") || strings.Contains(body, "Vault") || strings.Contains(body, "vault.app") {
				t.Fatalf("%s is not framed as Macro Wallets", entry.Name())
			}
		}
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

// sampleData is a mailable's data for the template name.
func sampleData(name string) any {
	switch name {
	case "user_invite.html":
		return userInviteData{AccountName: "North", InvitedBy: "Ada", InviteLink: "https://example.test/accept-invite"}
	case "password_reset.html":
		return passwordResetData{ResetLink: "https://example.test/login/recover/confirm"}
	case "welcome.html":
		return welcomeData{Name: "Ada", LoginLink: "https://example.test/login"}
	}
	return nil
}
