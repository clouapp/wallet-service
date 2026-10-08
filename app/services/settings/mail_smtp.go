package settings

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/macrowallets/waas/app/policies"
)

const (
	sectionMail = "mail"

	keyMailHost       = "host"
	keyMailPort       = "port"
	keyMailEncryption = "encryption"
	keyMailUsername   = "username"
	keyMailPassword   = "password"

	mailEncryptionNone     = "none"
	mailEncryptionTLS      = "tls"
	mailEncryptionStartTLS = "starttls"

	// defaultMailPort and defaultMailEncryption match config/mail.go when the
	// environment variables are unset. A send with no stored row does not use
	// these: it keeps the process mail config, which already applied MAIL_*.
	defaultMailPort       = 587
	defaultMailEncryption = mailEncryptionTLS

	minMailPort       = 1
	maxMailPort       = 65535
	maxMailHostLength = 253
)

// MailSMTP is the mail_smtp row the mailer may apply at send time.
// A false Use field means that value is missing or invalid and the env
// mailer keeps its own. Password is opened plaintext; callers must not log it.
type MailSMTP struct {
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

func mailSMTPGroup() Group {
	return Group{
		Name:    groupMailSMTP,
		Scope:   ScopePlatform,
		Section: sectionMail,
		Block:   "SMTP",
		// S1.4.4 moves MAIL_HOST/PORT/ENCRYPTION/USERNAME/PASSWORD here.
		// password is a Secret. S1.4.7 names mail.view and mail.update on
		// this credential so settings.update is not the grant. No account
		// role holds that pair. There is no platform permission catalog, so
		// a platform_admins row stands in. host is the Destination.
		ViewPermission:   policies.PermMailView,
		UpdatePermission: policies.PermMailUpdate,
		Settings: []Definition{
			{
				Key:         keyMailHost,
				Label:       "Host",
				Help:        "SMTP server the mailer connects to.",
				Type:        TypeString,
				Destination: true,
				Default:     func() any { return "" },
			},
			{
				Key:     keyMailPort,
				Label:   "Port",
				Help:    "SMTP port, from 1 to 65535.",
				Type:    TypeInt,
				Default: func() any { return defaultMailPort },
			},
			{
				Key:     keyMailEncryption,
				Label:   "Encryption",
				Help:    "How the mailer secures the SMTP connection.",
				Type:    TypeString,
				Options: []string{mailEncryptionNone, mailEncryptionTLS, mailEncryptionStartTLS},
				Default: func() any { return defaultMailEncryption },
			},
			{
				Key:     keyMailUsername,
				Label:   "Username",
				Help:    "SMTP username. Not a secret.",
				Type:    TypeString,
				Default: func() any { return "" },
			},
			{
				Key:     keyMailPassword,
				Label:   "Password",
				Help:    "SMTP password. Write it to replace the stored one; a blank keeps it.",
				Type:    TypeString,
				Secret:  true,
				Default: func() any { return "" },
			},
		},
		Validate: validateMailSMTP,
	}
}

func validateMailSMTP(effective map[string]string) error {
	fields := map[string][]string{}
	if message := mailHostMessage(effective[keyMailHost]); message != "" {
		fields[keyMailHost] = []string{message}
	}
	if _, ok := mailPort(effective[keyMailPort]); !ok {
		fields[keyMailPort] = []string{fmt.Sprintf("must be between %d and %d", minMailPort, maxMailPort)}
	}
	if !mailEncryption(effective[keyMailEncryption]) {
		fields[keyMailEncryption] = []string{"must be one of: none, tls, starttls"}
	}
	if len(fields) == 0 {
		return nil
	}
	return &ValidationError{Fields: fields}
}

// EffectiveMailSMTP reads mail_smtp at the moment of use. A missing row
// leaves every Use field false so the caller keeps the env mailer. One
// invalid key leaves that field unused. A database failure is returned so
// the caller can keep the env mailer and still send. The password is opened
// here and is never written to the log.
func (s *Service) EffectiveMailSMTP(ctx context.Context) (MailSMTP, error) {
	if s == nil {
		return MailSMTP{}, errServiceRequired
	}
	if ctx == nil {
		return MailSMTP{}, fmt.Errorf("mail smtp settings: context is required")
	}
	group, ok := FindGroup(groupMailSMTP)
	if !ok {
		return MailSMTP{}, ErrGroupNotFound
	}
	stored, err := s.platformValues(ctx, group.Name)
	if err != nil {
		return MailSMTP{}, err
	}
	return s.parseMailSMTP(stored), nil
}

func (s *Service) parseMailSMTP(stored map[string]string) MailSMTP {
	var out MailSMTP
	if !mailSMTPKeyPresent(stored, keyMailHost) {
		// A missing host keeps the env host.
	} else if host, ok := usableMailHost(stored[keyMailHost]); ok {
		out.Host = host
		out.UseHost = true
	} else {
		slog.Warn("mail smtp setting fell back to the env mailer", "key", keyMailHost)
	}
	if !mailSMTPKeyPresent(stored, keyMailPort) {
		// A missing port keeps the env port.
	} else if port, ok := mailPort(stored[keyMailPort]); ok {
		out.Port = port
		out.UsePort = true
	} else {
		slog.Warn("mail smtp setting fell back to the env mailer", "key", keyMailPort)
	}
	if !mailSMTPKeyPresent(stored, keyMailEncryption) {
		// A missing encryption keeps the env encryption.
	} else if mailEncryption(stored[keyMailEncryption]) {
		out.Encryption = strings.TrimSpace(stored[keyMailEncryption])
		out.UseEncryption = true
	} else {
		slog.Warn("mail smtp setting fell back to the env mailer", "key", keyMailEncryption)
	}
	if mailSMTPKeyPresent(stored, keyMailUsername) {
		out.Username = stored[keyMailUsername]
		out.UseUsername = true
	}
	if !mailSMTPKeyPresent(stored, keyMailPassword) || strings.TrimSpace(stored[keyMailPassword]) == "" {
		return out
	}
	if s == nil || s.sealer == nil || !IsSealed(stored[keyMailPassword]) {
		slog.Warn("mail smtp setting fell back to the env mailer", "key", keyMailPassword)
		return out
	}
	opened, err := s.sealer.Open(stored[keyMailPassword])
	if err != nil || opened == "" {
		slog.Warn("mail smtp setting fell back to the env mailer", "key", keyMailPassword)
		return out
	}
	out.Password = opened
	out.UsePassword = true
	return out
}

func mailSMTPKeyPresent(stored map[string]string, key string) bool {
	_, ok := stored[key]
	return ok
}

func usableMailHost(raw string) (string, bool) {
	host := strings.TrimSpace(raw)
	if mailHostMessage(host) != "" || host == "" {
		return "", false
	}
	return host, true
}

func mailHostMessage(raw string) string {
	host := strings.TrimSpace(raw)
	if host == "" {
		return ""
	}
	if len(host) > maxMailHostLength || strings.ContainsAny(host, " \t\r\n") {
		return "must be a host name"
	}
	return ""
}

func mailPort(raw string) (int, bool) {
	parsed, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || parsed < minMailPort || parsed > maxMailPort {
		return 0, false
	}
	return parsed, true
}

func mailEncryption(raw string) bool {
	switch strings.TrimSpace(raw) {
	case mailEncryptionNone, mailEncryptionTLS, mailEncryptionStartTLS:
		return true
	default:
		return false
	}
}

// secretRequiredAfterDestinationMove is the Destination rule. A blank or
// omitted secret keeps the stored ciphertext, except when a Destination
// field changes: the secret has to be sent again. An empty result means
// the write may proceed.
func secretRequiredAfterDestinationMove(group Group, stored, writes map[string]string) *ValidationError {
	if !destinationMoved(group, stored, writes) {
		return &ValidationError{}
	}
	invalid := &ValidationError{}
	for _, definition := range group.Settings {
		if !definition.Secret {
			continue
		}
		if _, ok := writes[definition.Key]; ok {
			continue
		}
		invalid.add(definition.Key, "must be set when host changes")
	}
	return invalid
}

func destinationMoved(group Group, stored, writes map[string]string) bool {
	for _, definition := range group.Settings {
		if !definition.Destination {
			continue
		}
		next, ok := writes[definition.Key]
		if !ok {
			continue
		}
		if next != stored[definition.Key] {
			return true
		}
	}
	return false
}
