// Package mailer is the mail provider stack: one Transport per driver, the
// settings they read, and the factory that resolves a driver to one.
//
// It mirrors app/adapters/ramp, minus the router: mail has one active driver
// and no automatic failover, so there is nothing to walk.
package mailer

import "context"

// The driver vocabulary. It is declared here, not in app/settings, for the same
// reason AllProviderCodes lives in the ramp package: the transports read it and
// must not import the settings registry, while the registry may import this.
const (
	DriverLog      = "log"
	DriverSMTP     = "smtp"
	DriverSES      = "ses"
	DriverMailgun  = "mailgun"
	DriverResend   = "resend"
	DriverPostmark = "postmark"
)

// Address is a sender or a recipient.
type Address struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

// String renders the address the way providers accept it in a JSON field. SMTP
// header encoding is the SMTP transport's business, not this type's.
func (a Address) String() string {
	if a.Name == "" {
		return a.Address
	}
	return a.Name + " <" + a.Address + ">"
}

// Message is an email that has already been rendered. There is no view and no
// template here: rendering happens before the queue, and the worker only
// transports.
//
// It carries exactly what the application's mailables produce. Attachments,
// custom headers, cc and bcc are absent because nothing sends them; the
// conversion in app/services/mail refuses a mailable that declares one rather
// than dropping it silently.
type Message struct {
	Subject string   `json:"subject"`
	HTML    string   `json:"html"`
	From    Address  `json:"from"`
	To      []string `json:"to"`
}

// Transport is one driver's send path.
type Transport interface {
	Driver() string
	Send(ctx context.Context, msg Message) error
}
