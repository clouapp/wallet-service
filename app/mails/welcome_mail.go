package mails

import (
	"html/template"

	contractsmail "github.com/goravel/framework/contracts/mail"
)

// WelcomeMail is sent to a user after successful registration. LoginLink is
// the front end's sign-in page; empty sends the message without a button.
type WelcomeMail struct {
	To        string
	FullName  string
	LoginLink string
}

type welcomeData struct {
	Name      string
	LoginLink template.URL
}

func (m *WelcomeMail) Envelope() *contractsmail.Envelope {
	return &contractsmail.Envelope{
		To:      []string{m.To},
		Subject: "Welcome to Macro Wallets",
	}
}

func (m *WelcomeMail) Content() *contractsmail.Content {
	name := m.FullName
	if name == "" {
		name = "there"
	}
	return &contractsmail.Content{
		Html: render("welcome.html", welcomeData{Name: name, LoginLink: template.URL(m.LoginLink)}),
	}
}

func (m *WelcomeMail) Attachments() []string { return nil }
func (m *WelcomeMail) Headers() map[string]string {
	return map[string]string{"X-Vault-Mail-Type": "welcome"}
}
func (m *WelcomeMail) Queue() *contractsmail.Queue { return nil }
