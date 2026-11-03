package mails

import "github.com/goravel/framework/contracts/mail"

// TestMail is what the admin's "send a test email" button sends.
//
// It deliberately does not use the OTP layout: the point is to prove that a
// driver's credentials work, and borrowing a layout built around a one-time
// code would make the message say something it does not mean.
type TestMail struct {
	To     string
	Driver string
}

// NewTestMail builds the test message. driver is shown in the body so the
// recipient can tell which provider delivered it.
func NewTestMail(to, driver string) *TestMail { return &TestMail{To: to, Driver: driver} }

func (m *TestMail) Envelope() *mail.Envelope {
	return &mail.Envelope{
		Subject: "XIP mail configuration test",
		To:      []string{m.To},
	}
}

func (m *TestMail) Content() *mail.Content {
	return &mail.Content{
		View: "test.tmpl",
		With: map[string]any{"driver": m.Driver},
	}
}

func (m *TestMail) Attachments() []string      { return nil }
func (m *TestMail) Headers() map[string]string { return nil }
func (m *TestMail) Queue() *mail.Queue         { return &mail.Queue{} }
