package mail

import (
	"context"
	"fmt"
	"strings"

	contractsmail "github.com/goravel/framework/contracts/mail"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/mailer"
)

// Facade adapts *Mailer to the framework's mail contract and is registered over
// binding.Mail, so facades.Mail() keeps being the application's only way out
// while the implementation behind it becomes multi-driver.
//
// It is an adapter, not the real thing: code that needs the narrow API, or that
// needs to name a driver, injects *Mailer directly. The admin test-send is the
// only caller that does.
type Facade struct {
	mailer *Mailer
	msg    mailer.Message
	view   string
	with   map[string]any

	// unsupported records builder calls the transports cannot honour. They
	// return mail.Mail and so cannot refuse at the call itself; the send does.
	unsupported []string
}

// NewFacade wraps a mailer in the framework's contract.
func NewFacade(m *Mailer) *Facade { return &Facade{mailer: m} }

// clone is what makes every builder method non-mutating, so two messages built
// from one facade cannot bleed into each other.
func (f *Facade) clone() *Facade {
	copied := *f
	copied.unsupported = append([]string(nil), f.unsupported...)
	copied.msg.To = append([]string(nil), f.msg.To...)
	return &copied
}

// Subject implements mail.Mail.
func (f *Facade) Subject(subject string) contractsmail.Mail {
	next := f.clone()
	next.msg.Subject = subject
	return next
}

// To implements mail.Mail.
func (f *Facade) To(addresses []string) contractsmail.Mail {
	next := f.clone()
	next.msg.To = addresses
	return next
}

// From implements mail.Mail. A sender named here beats the driver's settings.
func (f *Facade) From(address contractsmail.Address) contractsmail.Mail {
	next := f.clone()
	next.msg.From = mailer.Address{Address: address.Address, Name: address.Name}
	return next
}

// Content implements mail.Mail.
func (f *Facade) Content(content contractsmail.Content) contractsmail.Mail {
	next := f.clone()
	next.msg.HTML = content.Html
	next.view = content.View
	next.with = content.With
	return next
}

// Attach implements mail.Mail. See reject.
func (f *Facade) Attach(files []string) contractsmail.Mail {
	return f.reject("attachments", len(files) > 0)
}

// Cc implements mail.Mail. See reject.
func (f *Facade) Cc(addresses []string) contractsmail.Mail {
	return f.reject("cc", len(addresses) > 0)
}

// Bcc implements mail.Mail. See reject.
func (f *Facade) Bcc(addresses []string) contractsmail.Mail {
	return f.reject("bcc", len(addresses) > 0)
}

// Headers implements mail.Mail. See reject.
func (f *Facade) Headers(headers map[string]string) contractsmail.Mail {
	return f.reject("headers", len(headers) > 0)
}

// Queue implements mail.Mail.
func (f *Facade) Queue(mailable ...contractsmail.Mailable) error {
	if err := f.refusal(); err != nil {
		return err
	}
	if len(mailable) > 0 {
		return f.mailer.Queue(mailable[0])
	}
	msg, err := f.built()
	if err != nil {
		return err
	}
	return f.mailer.QueueMessage(msg)
}

// Send implements mail.Mail.
//
// The contract carries no context, so the deadline starts here. Application
// code should still prefer Queue -- a synchronous send holds the request.
func (f *Facade) Send(mailable ...contractsmail.Mailable) error {
	if err := f.refusal(); err != nil {
		return err
	}
	if len(mailable) > 0 {
		return f.mailer.Send(context.Background(), mailable[0], "")
	}
	msg, err := f.built()
	if err != nil {
		return err
	}
	return f.mailer.SendMessage(context.Background(), msg, "")
}

// reject records an ask no transport can honour.
//
// The empty call is a no-op on purpose: Attach(nil) asks for nothing, and
// refusing it would break a caller that passes an empty slice unconditionally.
func (f *Facade) reject(what string, asked bool) contractsmail.Mail {
	if !asked {
		return f
	}
	next := f.clone()
	next.unsupported = append(next.unsupported, what)
	return next
}

func (f *Facade) refusal() error {
	if len(f.unsupported) == 0 {
		return nil
	}
	return fmt.Errorf("mail: %s not supported by the mail transports", strings.Join(f.unsupported, ", "))
}

// built renders the fluent state into a message.
func (f *Facade) built() (mailer.Message, error) {
	msg := f.msg
	if f.view != "" {
		html, err := f.mailer.Render(f.view, f.with)
		if err != nil {
			return mailer.Message{}, fmt.Errorf("mail: render %s: %w", f.view, err)
		}
		msg.HTML = html
	}
	return msg, nil
}

var _ contractsmail.Mail = (*Facade)(nil)
