package mailer

import (
	"context"
	"log/slog"
)

const (
	smtpUnreadMessage = "mail smtp settings unread; keeping the env mailer"
	fromUnreadMessage = "mail delivery settings unread; keeping the env from header"
)

// Dial is one SMTP dial a send may apply. A false Use field leaves that env
// value in place. Password is plaintext for the dial only and is not logged.
type Dial struct {
	Host          string
	Port          int
	Encryption    string
	Username      string
	Password      string
	UseHost       bool
	UsePort       bool
	UseEncryption bool
	UseUsername   bool
	UsePassword   bool
}

// From is the From header one send may apply. A false Use field leaves that
// env value in place. Address and name are not secrets.
type From struct {
	Address    string
	Name       string
	UseAddress bool
	UseName    bool
}

// DialReader reads mail_smtp at send time. installed is false when no reader
// is configured, which keeps the env mailer. A non-nil error keeps it too.
type DialReader func(ctx context.Context) (dial Dial, installed bool, err error)

// FromReader reads the mail_delivery From header at send time. installed is
// false when no reader is configured. A non-nil error keeps the env header.
// The driver stored on mail_delivery is not part of this read.
type FromReader func(ctx context.Context) (from From, installed bool, err error)

// Hooks are the process mail document and the per-send readers. The mail
// lock is held by the caller for the whole send.
type Hooks struct {
	Baseline func() map[string]any
	Write    func(map[string]any)
	Restore  func()
	Observe  func()
	SMTP     DialReader
	From     FromReader
}

// Config reads mail_smtp and the mail_delivery From header at the moment of
// use and overlays them on the env mail document. It does not select a
// transport: the dial stays the SMTP mailer.
type Config struct {
	hooks Hooks
}

// NewConfig wires a Config over the process mail document and the readers.
func NewConfig(hooks Hooks) *Config {
	return &Config{hooks: hooks}
}

// Resolve returns the mail document for one send. A missing reader or a
// failed read leaves the env document in place.
func (c *Config) Resolve(ctx context.Context) map[string]any {
	if c == nil || c.hooks.Baseline == nil {
		return map[string]any{}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	base := c.hooks.Baseline()
	if base == nil {
		base = map[string]any{}
	}
	c.applySMTP(ctx, base)
	c.applyFrom(ctx, base)
	return base
}

// WriteResolved publishes the resolved document for the SMTP dial.
func (c *Config) WriteResolved(ctx context.Context) {
	if c == nil || c.hooks.Write == nil {
		return
	}
	c.hooks.Write(c.Resolve(ctx))
}

// Restore puts the env mail document back after the dial.
func (c *Config) Restore() {
	if c == nil || c.hooks.Restore == nil {
		return
	}
	c.hooks.Restore()
}

// Observe runs the send observer after the document is published and before
// the dial. The observer must not send mail.
func (c *Config) Observe() {
	if c == nil || c.hooks.Observe == nil {
		return
	}
	c.hooks.Observe()
}

func (c *Config) applySMTP(ctx context.Context, base map[string]any) {
	if c.hooks.SMTP == nil {
		return
	}
	overlay, installed, err := c.hooks.SMTP(ctx)
	if !installed {
		return
	}
	if err != nil {
		slog.Warn(smtpUnreadMessage)
		return
	}
	mergeDial(base, overlay)
}

func (c *Config) applyFrom(ctx context.Context, base map[string]any) {
	if c.hooks.From == nil {
		return
	}
	overlay, installed, err := c.hooks.From(ctx)
	if !installed {
		return
	}
	if err != nil {
		slog.Warn(fromUnreadMessage)
		return
	}
	mergeFrom(base, overlay)
}

// mergeDial overlays a stored mail_smtp row on the env mail document.
// Goravel reads mail.host, mail.port, mail.username, and mail.password at
// send time and picks the socket from the port. encryption is written on
// the smtp mailer as well. A field that is not in use stays on the env copy.
func mergeDial(cfg map[string]any, overlay Dial) {
	if cfg == nil {
		return
	}
	smtp := ensureSMTP(cfg)
	if overlay.UseHost {
		smtp["host"] = overlay.Host
		cfg["host"] = overlay.Host
	}
	if overlay.UsePort {
		smtp["port"] = overlay.Port
		cfg["port"] = overlay.Port
	}
	if overlay.UseEncryption {
		smtp["encryption"] = overlay.Encryption
		cfg["encryption"] = overlay.Encryption
	}
	if overlay.UseUsername {
		smtp["username"] = overlay.Username
		cfg["username"] = overlay.Username
	}
	if overlay.UsePassword {
		smtp["password"] = overlay.Password
		cfg["password"] = overlay.Password
	}
}

// mergeFrom overlays a stored mail_delivery From header on the env mail
// document. Goravel reads mail.from.address and mail.from.name at send time
// when the mailable leaves From empty. A field that is not in use stays on
// the env copy. The stored driver is not applied.
func mergeFrom(cfg map[string]any, overlay From) {
	if cfg == nil || (!overlay.UseAddress && !overlay.UseName) {
		return
	}
	from, _ := cfg["from"].(map[string]any)
	if from == nil {
		from = map[string]any{}
		cfg["from"] = from
	}
	if overlay.UseAddress {
		from["address"] = overlay.Address
	}
	if overlay.UseName {
		from["name"] = overlay.Name
	}
}

func ensureSMTP(cfg map[string]any) map[string]any {
	mailers, _ := cfg["mailers"].(map[string]any)
	if mailers == nil {
		mailers = map[string]any{}
		cfg["mailers"] = mailers
	}
	smtp, _ := mailers["smtp"].(map[string]any)
	if smtp == nil {
		smtp = map[string]any{}
		mailers["smtp"] = smtp
	}
	return smtp
}
