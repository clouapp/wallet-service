package mails

import (
	"fmt"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// PasswordResetMail is sent when a user requests a password reset.
type PasswordResetMail struct {
	To        string
	ResetLink string
}

func (m *PasswordResetMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: "Reset Your Password",
	}
}

func (m *PasswordResetMail) Content() *contractsmail.Content {
	return &contractsmail.Content{
		Html: fmt.Sprintf(`<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:40px auto;color:#333;">
  <h2>Reset Your Password</h2>
  <p>We received a request to reset your Vault account password.</p>
  <p>Click the button below to choose a new password. This link expires in 1 hour.</p>
  <p>
    <a href="%s" style="display:inline-block;padding:12px 24px;background:#1a56db;color:#fff;text-decoration:none;border-radius:4px;">
      Reset Password
    </a>
  </p>
  <p>If you did not request a password reset, you can safely ignore this email.</p>
</body>
</html>`, m.ResetLink),
	}
}

func (m *PasswordResetMail) Attachments() []string { return nil }
func (m *PasswordResetMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "password-reset"}
}
func (m *PasswordResetMail) Queue() *contractsmail.Queue { return nil }
