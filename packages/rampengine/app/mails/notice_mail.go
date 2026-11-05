package mails

import "github.com/goravel/framework/contracts/mail"

// Notice is what a notification e-mail says, already in the recipient's
// language: app/notifications writes it, notice.tmpl lays it out.
type Notice struct {
	// Locale is the language it is written in, for the page's lang.
	Locale     string
	Subject    string
	Title      string
	Paragraphs []string
	Footer     string
}

// noticeMail is a notification e-mail to one recipient.
type noticeMail struct {
	to     string
	notice Notice
}

// NewNoticeMail is notice mailed to to.
func NewNoticeMail(to string, notice Notice) mail.Mailable {
	return &noticeMail{to: to, notice: notice}
}

func (m *noticeMail) Envelope() *mail.Envelope {
	return &mail.Envelope{Subject: m.notice.Subject, To: []string{m.to}}
}

func (m *noticeMail) Content() *mail.Content {
	return &mail.Content{
		View: "notice.tmpl",
		With: map[string]any{
			"lang":       m.notice.Locale,
			"title":      m.notice.Title,
			"paragraphs": m.notice.Paragraphs,
			"footer":     m.notice.Footer,
		},
	}
}

func (m *noticeMail) Attachments() []string      { return nil }
func (m *noticeMail) Headers() map[string]string { return nil }
func (m *noticeMail) Queue() *mail.Queue         { return &mail.Queue{} }
