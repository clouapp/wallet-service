package mails

import "github.com/goravel/framework/contracts/mail"

// otpMail is every one-time-code email. The templates share one data shape and
// differ only in their words, which adminMailable and clientMailable choose per
// audience and purpose.
type otpMail struct {
	to, code                            string
	view, subject, title, intro, expiry string
}

func (m *otpMail) Envelope() *mail.Envelope {
	return &mail.Envelope{Subject: m.subject, To: []string{m.to}}
}

func (m *otpMail) Content() *mail.Content {
	return &mail.Content{
		View: m.view,
		With: map[string]any{
			"code":   m.code,
			"title":  m.title,
			"intro":  m.intro,
			"expiry": m.expiry,
		},
	}
}

func (m *otpMail) Attachments() []string      { return nil }
func (m *otpMail) Headers() map[string]string { return nil }
func (m *otpMail) Queue() *mail.Queue         { return &mail.Queue{} }
