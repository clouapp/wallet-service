package mail

import (
	contractsmail "github.com/goravel/framework/contracts/mail"
)

// Facade adapts *Mailer to the framework mail contract. facades.Mail()
// returns it. The mailer reads mailer.Config at send time and the inner
// transport stays SMTP.
type Facade struct {
	mailer *Mailer
	inner  contractsmail.Mail
}

// NewFacade wraps a mailer and the SMTP transport.
func NewFacade(m *Mailer, inner contractsmail.Mail) *Facade {
	return &Facade{mailer: m, inner: inner}
}

// Mailer returns the mailer this facade sends through.
func (f *Facade) Mailer() *Mailer {
	if f == nil {
		return nil
	}
	return f.mailer
}

// Attach implements mail.Mail.
func (f *Facade) Attach(files []string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Attach(files))
}

// Bcc implements mail.Mail.
func (f *Facade) Bcc(addresses []string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Bcc(addresses))
}

// Cc implements mail.Mail.
func (f *Facade) Cc(addresses []string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Cc(addresses))
}

// Content implements mail.Mail.
func (f *Facade) Content(content contractsmail.Content) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Content(content))
}

// From implements mail.Mail.
func (f *Facade) From(address contractsmail.Address) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.From(address))
}

// Headers implements mail.Mail.
func (f *Facade) Headers(headers map[string]string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Headers(headers))
}

// Queue implements mail.Mail. The SMTP document is published first.
func (f *Facade) Queue(mailable ...contractsmail.Mailable) error {
	if f == nil || f.mailer == nil || f.inner == nil {
		return errMailerRequired
	}
	return f.mailer.Deliver(func() error { return f.inner.Queue(mailable...) })
}

// Send implements mail.Mail. The SMTP document is published first.
func (f *Facade) Send(mailable ...contractsmail.Mailable) error {
	if f == nil || f.mailer == nil || f.inner == nil {
		return errMailerRequired
	}
	return f.mailer.Deliver(func() error { return f.inner.Send(mailable...) })
}

// Subject implements mail.Mail.
func (f *Facade) Subject(subject string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.Subject(subject))
}

// To implements mail.Mail.
func (f *Facade) To(addresses []string) contractsmail.Mail {
	if f == nil || f.inner == nil {
		return f
	}
	return f.wrap(f.inner.To(addresses))
}

func (f *Facade) wrap(next contractsmail.Mail) contractsmail.Mail {
	if next == nil {
		return f
	}
	return &Facade{mailer: f.mailer, inner: next}
}

var _ contractsmail.Mail = (*Facade)(nil)
