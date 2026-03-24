package mails

import (
	"fmt"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// WelcomeMail is sent to a user after successful registration.
type WelcomeMail struct {
	To       string
	FullName string
}

func (m *WelcomeMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: "Welcome to Vault",
	}
}

func (m *WelcomeMail) Content() *contractsmail.Content {
	name := m.FullName
	if name == "" {
		name = "there"
	}
	return &contractsmail.Content{
		Html: fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:40px auto;color:#333;">
  <h2>Welcome to Vault, %s!</h2>
  <p>Your account has been created. You can now log in and start managing your wallets.</p>
  <p>
    <a href="https://vault.app/login" style="display:inline-block;padding:12px 24px;background:#1a56db;color:#fff;text-decoration:none;border-radius:4px;">
      Log In
    </a>
  </p>
  <p>If you have any questions, contact our support team at support@vault.app.</p>
</body>
</html>`, name),
	}
}

func (m *WelcomeMail) Attachments() []string { return nil }
func (m *WelcomeMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "welcome"}
}
func (m *WelcomeMail) Queue() *contractsmail.Queue { return nil }
