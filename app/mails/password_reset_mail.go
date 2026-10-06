package mails

import (
	"html/template"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// PasswordResetMail is sent when a user requests a password reset.
type PasswordResetMail struct {
	To        string
	ResetLink string
}

type passwordResetData struct {
	ResetLink template.URL
}

func (m *PasswordResetMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: "Reset Your Password",
	}
}

func (m *PasswordResetMail) Content() *contractsmail.Content {
	return &contractsmail.Content{
		Html: render("password_reset.html", passwordResetData{
			ResetLink: template.URL(m.ResetLink),
		}),
	}
}

func (m *PasswordResetMail) Attachments() []string { return nil }
func (m *PasswordResetMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "password-reset"}
}
func (m *PasswordResetMail) Queue() *contractsmail.Queue { return nil }
