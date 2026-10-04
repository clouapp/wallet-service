package settings

import (
	"context"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"strings"
)

const (
	keyMailDriver      = "driver"
	keyMailFromAddress = "from_address"
	keyMailFromName    = "from_name"

	mailDriverLog      = "log"
	mailDriverSMTP     = "smtp"
	mailDriverSES      = "ses"
	mailDriverMailgun  = "mailgun"
	mailDriverResend   = "resend"
	mailDriverPostmark = "postmark"

	// defaultMailFromAddress and defaultMailFromName match config/mail.go when
	// MAIL_FROM_ADDRESS and MAIL_FROM_NAME are unset. A send with no stored
	// row does not use these: it keeps the process From header.
	defaultMailFromAddress = "noreply@vault.dev"
	defaultMailFromName    = "Vault"

	maxMailFromAddressLength = 254
)

// MailDelivery is the From header the mailer may apply at send time.
// A false Use field means that value is missing or invalid and the env
// From header keeps its own. Driver is validated on write and is not applied
// here: the process mailer stays the SMTP mailer.
type MailDelivery struct {
	Address    string
	Name       string
	UseAddress bool
	UseName    bool
}

func mailDeliveryGroup() Group {
	return Group{
		Name:    groupMailDelivery,
		Scope:   ScopePlatform,
		Section: sectionMail,
		Block:   "From",
		// S1.4.4 moves MAIL_FROM_ADDRESS/NAME here, with the driver selector.
		// from_address and from_name are not secrets. This group names no
		// permission: a platform_admins row is the gate.
		Settings: []Definition{
			{
				Key:     keyMailDriver,
				Label:   "Driver",
				Help:    "Which mail transport sends. log is refused in production.",
				Type:    TypeString,
				Options: mailDeliveryDrivers(),
				Default: func() any { return mailDriverSMTP },
			},
			{
				Key:     keyMailFromAddress,
				Label:   "From address",
				Help:    "Address on the From header. Required.",
				Type:    TypeString,
				Default: func() any { return defaultMailFromAddress },
			},
			{
				Key:     keyMailFromName,
				Label:   "From name",
				Help:    "Name on the From header. A blank name is refused.",
				Type:    TypeString,
				Default: func() any { return defaultMailFromName },
			},
		},
		Validate: validateMailDelivery,
	}
}

// mailDeliveryDrivers is the closed vocabulary S1.4.4 names for
// mailer.AllDrivers. The credential groups for the non-SMTP drivers are not
// part of this group.
func mailDeliveryDrivers() []string {
	return []string{
		mailDriverLog,
		mailDriverSMTP,
		mailDriverSES,
		mailDriverMailgun,
		mailDriverResend,
		mailDriverPostmark,
	}
}

func validateMailDelivery(effective map[string]string) error {
	fields := map[string][]string{}
	if message := mailDriverMessage(effective[keyMailDriver]); message != "" {
		fields[keyMailDriver] = []string{message}
	}
	if message := mailFromAddressMessage(effective[keyMailFromAddress]); message != "" {
		fields[keyMailFromAddress] = []string{message}
	}
	if message := mailFromNameMessage(effective[keyMailFromName]); message != "" {
		fields[keyMailFromName] = []string{message}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

// EffectiveMailDelivery reads mail_delivery at the moment of use. A missing
// row leaves every Use field false so the caller keeps the env From header.
// One invalid key leaves that field unused. A database failure is returned
// so the caller can keep the env From header and still send.
func (s *Service) EffectiveMailDelivery(ctx context.Context) (MailDelivery, error) {
	if s == nil {
		return MailDelivery{}, errServiceRequired
	}
	if ctx == nil {
		return MailDelivery{}, fmt.Errorf("mail delivery settings: context is required")
	}
	group, ok := FindGroup(groupMailDelivery)
	if !ok {
		return MailDelivery{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group.Name)
	if err != nil {
		return MailDelivery{}, err
	}
	return parseMailDelivery(stored), nil
}

func parseMailDelivery(stored map[string]string) MailDelivery {
	var out MailDelivery
	if !mailDeliveryKeyPresent(stored, keyMailFromAddress) {
		// A missing address keeps the env From address.
	} else if address, ok := usableMailFromAddress(stored[keyMailFromAddress]); ok {
		out.Address = address
		out.UseAddress = true
	} else {
		slog.Warn("mail delivery setting fell back to the env from header", "key", keyMailFromAddress)
	}
	if !mailDeliveryKeyPresent(stored, keyMailFromName) {
		// A missing name keeps the env From name.
	} else if name, ok := usableMailFromName(stored[keyMailFromName]); ok {
		out.Name = name
		out.UseName = true
	} else {
		slog.Warn("mail delivery setting fell back to the env from header", "key", keyMailFromName)
	}
	return out
}

func mailDeliveryKeyPresent(stored map[string]string, key string) bool {
	_, ok := stored[key]
	return ok
}

func usableMailFromAddress(raw string) (string, bool) {
	address := strings.TrimSpace(raw)
	if mailFromAddressMessage(address) != "" {
		return "", false
	}
	return address, true
}

func usableMailFromName(raw string) (string, bool) {
	name := strings.TrimSpace(raw)
	if mailFromNameMessage(name) != "" {
		return "", false
	}
	return name, true
}

func mailFromAddressMessage(raw string) string {
	address := strings.TrimSpace(raw)
	if address == "" {
		return "is required"
	}
	if len(address) > maxMailFromAddressLength || strings.ContainsAny(address, " \t\r\n<>") {
		return "must be an email address"
	}
	parsed, err := mail.ParseAddress(address)
	if err != nil || parsed == nil || parsed.Address != address {
		return "must be an email address"
	}
	return ""
}

func mailFromNameMessage(raw string) string {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "is required"
	}
	if strings.ContainsAny(name, "\r\n<>") {
		return "must be a name"
	}
	return ""
}

func mailDriverMessage(raw string) string {
	driver := strings.TrimSpace(raw)
	if driver == "" || !mailDriverKnown(driver) {
		return "must be one of: " + strings.Join(mailDeliveryDrivers(), ", ")
	}
	if driver == mailDriverLog && productionMailEnv() {
		return "must not be log in production"
	}
	return ""
}

func mailDriverKnown(driver string) bool {
	for _, option := range mailDeliveryDrivers() {
		if driver == option {
			return true
		}
	}
	return false
}

func productionMailEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("APP_ENV"))) {
	case "production", "prod":
		return true
	default:
		return false
	}
}
