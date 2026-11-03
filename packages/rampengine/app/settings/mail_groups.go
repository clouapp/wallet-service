package settings

import (
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/mailer"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// The permissions the mail credential groups declare. They are their own grants
// rather than settings.*, for the reason the provider groups have their own: an
// API key that sends mail signed with our domain is a different blast radius
// from editing the sender's display name, and the standard operator holds
// settings.update but not mail.update.
const (
	permMailView   = "mail.view"
	permMailUpdate = "mail.update"
)

// PermMailUpdate is the grant required to write a mail credential -- and to send
// a test message through one, which is the same credential by another name.
func PermMailUpdate() string { return permMailUpdate }

// SectionMail is the single settings page the whole mail stack shows on. Each
// group still saves on its own; the section only decides what the nav lists.
const SectionMail = "mail"

// BlockMailProviders titles the run of credential groups on that page.
const BlockMailProviders = "providers"

// mailGroups declares the driver picker plus one group per driver that has
// credentials.
//
// The vocabulary comes from app/adapters/mailer rather than being repeated
// here, the same way rampGroups takes it from app/adapters/ramp: a driver
// added to one list and not the other would be either unpickable in the admin
// or pickable with no transport behind it.
func mailGroups() []Group {
	groups := []Group{
		{
			Name: mailer.GroupDelivery,
			Settings: []Definition{
				{Key: mailer.SettingDriver, Label: "Driver", Type: TypeString, Options: mailer.AllDrivers(),
					Help:    "Which provider sends mail. Only one is active; there is no automatic failover.",
					Default: func() any { return facades.Config().GetString("mail.driver") }},
				{Key: mailer.SettingFromAddress, Label: "From Address", Type: TypeString,
					Help:    "Sender used by every driver that does not override it.",
					Default: func() any { return facades.Config().GetString("mail.from.address") }},
				{Key: mailer.SettingFromName, Label: "From Name", Type: TypeString,
					Default: func() any { return facades.Config().GetString("mail.from.name") }},
			},
			Section:  SectionMail,
			Validate: validateMailGroup,
		},
	}
	for _, driver := range mailer.CredentialDrivers() {
		groups = append(groups, credentialGroup(driver))
	}
	return groups
}

// credentialGroup builds one driver's group: its own credentials plus the
// sender override every driver carries.
//
// The override exists because each provider verifies a different domain, and a
// single global sender would lock the driver in place.
func credentialGroup(driver string) Group {
	defs := append(credentialDefinitions(driver),
		Definition{Key: mailer.SettingFromAddress, Label: "From Address (override)", Type: TypeString,
			Help:    "Leave empty to use the global sender.",
			Default: func() any { return "" }},
		Definition{Key: mailer.SettingFromName, Label: "From Name (override)", Type: TypeString,
			Default: func() any { return "" }},
	)
	return Group{
		Name:             mailer.DriverGroup(driver),
		Settings:         defs,
		Section:          SectionMail,
		Block:            BlockMailProviders,
		ViewPermission:   permMailView,
		UpdatePermission: permMailUpdate,
	}
}

// credentialDefinitions is what each driver needs to authenticate. Defaults are
// resolved lazily from config so that, with nothing stored, the .env stays the
// effective fallback.
func credentialDefinitions(driver string) []Definition {
	switch driver {
	case mailer.DriverSMTP:
		return []Definition{
			{Key: mailer.SettingHost, Label: "SMTP Host", Type: TypeString, Destination: true,
				Default: func() any { return facades.Config().GetString("mail.smtp.host") }},
			{Key: mailer.SettingPort, Label: "SMTP Port", Type: TypeInt,
				Default: func() any { return facades.Config().GetInt("mail.smtp.port") }},
			{Key: mailer.SettingEncryption, Label: "Encryption", Type: TypeString,
				Options: []string{mailer.EncryptionNone, mailer.EncryptionTLS, mailer.EncryptionStartTLS},
				Help:    "starttls on 587, tls on 465, none for a local catcher such as Mailpit.",
				Default: func() any { return facades.Config().GetString("mail.smtp.encryption") }},
			{Key: mailer.SettingUsername, Label: "SMTP Username", Type: TypeString,
				Default: func() any { return facades.Config().GetString("mail.smtp.username") }},
			{Key: mailer.SettingPassword, Label: "SMTP Password", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.smtp.password") }},
		}
	case mailer.DriverSES:
		return []Definition{
			{Key: mailer.SettingKey, Label: "Access Key ID", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.ses.key") }},
			{Key: mailer.SettingSecret, Label: "Secret Access Key", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.ses.secret") }},
			{Key: mailer.SettingRegion, Label: "Region", Type: TypeString,
				Default: func() any { return facades.Config().GetString("mail.ses.region") }},
		}
	case mailer.DriverMailgun:
		return []Definition{
			{Key: mailer.SettingDomain, Label: "Domain", Type: TypeString,
				Default: func() any { return facades.Config().GetString("mail.mailgun.domain") }},
			{Key: mailer.SettingSecret, Label: "API Key", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.mailgun.secret") }},
			{Key: mailer.SettingEndpoint, Label: "API Host", Type: TypeString, Destination: true,
				Help:    "api.mailgun.net, or api.eu.mailgun.net for an EU domain.",
				Default: func() any { return facades.Config().GetString("mail.mailgun.endpoint") }},
		}
	case mailer.DriverResend:
		return []Definition{
			{Key: mailer.SettingAPIKey, Label: "API Key", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.resend.api_key") }},
		}
	case mailer.DriverPostmark:
		return []Definition{
			{Key: mailer.SettingToken, Label: "Server Token", Type: TypeString, Secret: true,
				Default: func() any { return facades.Config().GetString("mail.postmark.token") }},
			{Key: mailer.SettingMessageStream, Label: "Message Stream", Type: TypeString,
				Help:    "Empty uses the server's default stream.",
				Default: func() any { return facades.Config().GetString("mail.postmark.message_stream_id") }},
		}
	default:
		// Unreachable while credentialGroup is driven by CredentialDrivers, and
		// deliberately empty rather than a panic: a driver added there without
		// credentials here gets a group with only the sender override, which is
		// visible in the admin instead of taking the process down at boot.
		return nil
	}
}

func validateMailGroup(effective map[string]string) error {
	return validateMailGroupIn(facades.Config().GetString("app.env"), effective)
}

// validateMailGroupIn is the environment-injected half, so the rule is testable
// without reaching for the config facade.
func validateMailGroupIn(appEnv string, effective map[string]string) error {
	if strings.TrimSpace(effective[mailer.SettingFromAddress]) == "" {
		return fmt.Errorf("%s is required: it is the fallback sender for every driver", mailer.SettingFromAddress)
	}
	if effective[mailer.SettingDriver] == mailer.DriverLog && appEnv == "production" {
		return fmt.Errorf("driver %q writes mail to the log instead of sending it; refused in production", mailer.DriverLog)
	}
	return nil
}
