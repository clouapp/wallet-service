package mails

import contractsmail "github.com/goravel/framework/contracts/mail"

// WelcomeMail is sent to a user after successful registration.
type WelcomeMail struct {
	To       string
	FullName string
}

type welcomeData struct {
	Name string
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
		Html: render("welcome.html", welcomeData{Name: name}),
	}
}

func (m *WelcomeMail) Attachments() []string { return nil }
func (m *WelcomeMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "welcome"}
}
func (m *WelcomeMail) Queue() *contractsmail.Queue { return nil }
