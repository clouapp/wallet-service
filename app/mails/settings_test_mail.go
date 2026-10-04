package mails

import contractsmail "github.com/goravel/framework/contracts/mail"

// SettingsTestMail is the one message POST /v1/platform/settings/mail/test sends.
// The body is static: it never carries SMTP settings.
type SettingsTestMail struct {
	To string
}

func (m *SettingsTestMail) Envelope() *contractsmail.Envelope {
	to := ""
	if m != nil {
		to = m.To
	}
	return &contractsmail.Envelope{
		To:      []string{to},
		Subject: "Mail test",
	}
}

func (m *SettingsTestMail) Content() *contractsmail.Content {
	return &contractsmail.Content{
		Html: `<!DOCTYPE html>
<html>
<body style="font-family:sans-serif;max-width:600px;margin:40px auto;color:#333;">
  <p>This is a test message from Vault.</p>
</body>
</html>`,
	}
}

func (m *SettingsTestMail) Attachments() []string { return nil }

func (m *SettingsTestMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "settings-test"}
}

func (m *SettingsTestMail) Queue() *contractsmail.Queue { return nil }
