// Package settings shapes the answers of the platform settings routes that
// are not a settings view.
package settings

// MailTestSent is the answer of a platform mail test that reached the mailer.
type MailTestSent struct {
	Sent bool `json:"sent" example:"true"`
}

// NewMailTestSent marks the test message as sent.
func NewMailTestSent() MailTestSent {
	return MailTestSent{Sent: true}
}
