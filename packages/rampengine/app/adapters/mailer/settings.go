package mailer

import (
	"fmt"
	"slices"
)

// GroupDelivery is the settings group holding the driver choice and the global
// sender.
//
// It is mail_delivery and not mail so that "mail" is free to be the section the
// whole stack shows on: a section sharing a name with a group makes
// /settings/<name> ambiguous, which is the same reason the ramp stack is a
// providers section over provider_* groups.
const GroupDelivery = "mail_delivery"

// The keys inside the mail groups. They are constants because the registry and
// this reader have to name them identically, and a typo in either is a silent
// misread rather than a compile error.
const (
	SettingDriver        = "driver"
	SettingFromAddress   = "from_address"
	SettingFromName      = "from_name"
	SettingHost          = "host"
	SettingPort          = "port"
	SettingEncryption    = "encryption"
	SettingUsername      = "username"
	SettingPassword      = "password"
	SettingKey           = "key"
	SettingSecret        = "secret"
	SettingRegion        = "region"
	SettingDomain        = "domain"
	SettingEndpoint      = "endpoint"
	SettingAPIKey        = "api_key"
	SettingToken         = "token"
	SettingMessageStream = "message_stream_id"
)

// The SMTP encryption vocabulary.
const (
	EncryptionNone     = "none"
	EncryptionTLS      = "tls"
	EncryptionStartTLS = "starttls"
)

// MailSettingsSource is the narrow settings dependency this package reads.
//
// Declared here rather than imported so the transports never depend on the
// settings service, and so app/settings can import this package to declare the
// mail groups without creating a cycle.
//
// The name carries the Mail prefix because mockery writes every mock to
// app/mocks/mock_<interface>.go, and a second SettingsSource would silently
// overwrite the ramp one.
type MailSettingsSource interface {
	String(group, key string) (string, error)
	Int(group, key string) (int, error)
	Secret(group, key string) (string, error)
}

// AllDrivers is the closed vocabulary of drivers, in the order the admin picker
// lists them. It is the single source of truth: app/settings declares it as the
// Options of mail.driver by calling this.
func AllDrivers() []string {
	return []string{DriverLog, DriverSMTP, DriverSES, DriverMailgun, DriverResend, DriverPostmark}
}

// CredentialDrivers is every driver that has something to configure. DriverLog
// is absent: it takes no credentials, so it gets no settings group.
func CredentialDrivers() []string {
	return []string{DriverSMTP, DriverSES, DriverMailgun, DriverResend, DriverPostmark}
}

// DriverGroup is the settings group holding one driver's credentials.
func DriverGroup(driver string) string { return "mail_" + driver }

// HasCredentialGroup reports whether a driver has a settings group at all.
//
// It matters because the settings service refuses a key the registry does not
// declare, so reading a group that was never registered is an error rather than
// an empty answer -- and DriverLog, which takes no credentials, has no group.
func HasCredentialGroup(driver string) bool {
	return slices.Contains(CredentialDrivers(), driver)
}

// Config reads the mail stack's admin state out of settings. Transports read
// their own credentials through it on every send, so a key changed in the panel
// takes effect on the next message without a restart.
type Config struct{ settings MailSettingsSource }

func NewConfig(settings MailSettingsSource) *Config { return &Config{settings: settings} }

func (c *Config) Driver() (string, error) {
	return c.read(GroupDelivery, SettingDriver)
}

// From resolves the sender for a driver: its own override when it sets an
// address, the global sender otherwise. The address decides -- a driver that
// overrides only the display name is not claiming a different identity.
func (c *Config) From(driver string) (Address, error) {
	globalAddress, err := c.read(GroupDelivery, SettingFromAddress)
	if err != nil {
		return Address{}, err
	}
	globalName, err := c.read(GroupDelivery, SettingFromName)
	if err != nil {
		return Address{}, err
	}
	// A driver with no group has no override to read, and asking for one would
	// be an unknown-key error rather than a blank.
	if !HasCredentialGroup(driver) {
		return Address{Address: globalAddress, Name: globalName}, nil
	}
	group := DriverGroup(driver)
	address, err := c.read(group, SettingFromAddress)
	if err != nil {
		return Address{}, err
	}
	if address == "" {
		return Address{Address: globalAddress, Name: globalName}, nil
	}
	name, err := c.read(group, SettingFromName)
	if err != nil {
		return Address{}, err
	}
	if name == "" {
		name = globalName
	}
	return Address{Address: address, Name: name}, nil
}

// Value returns one of a driver's non-secret settings.
func (c *Config) Value(driver, key string) (string, error) {
	return c.read(DriverGroup(driver), key)
}

func (c *Config) Int(driver, key string) (int, error) {
	value, err := c.settings.Int(DriverGroup(driver), key)
	if err != nil {
		return 0, fmt.Errorf("mailer config: %s %s: %w", driver, key, err)
	}
	return value, nil
}

// Secret returns one of a driver's credentials in plaintext.
func (c *Config) Secret(driver, key string) (string, error) {
	value, err := c.settings.Secret(DriverGroup(driver), key)
	if err != nil {
		return "", fmt.Errorf("mailer config: %s %s: %w", driver, key, err)
	}
	return value, nil
}

// RequireValue is Value refusing a blank setting, the way every driver refuses
// to send without its host or domain.
func (c *Config) RequireValue(driver, key string) (string, error) {
	value, err := c.Value(driver, key)
	if err != nil {
		return "", err
	}
	return value, requireSet(driver, key, value)
}

// RequireSecret is Secret refusing a blank credential.
func (c *Config) RequireSecret(driver, key string) (string, error) {
	value, err := c.Secret(driver, key)
	if err != nil {
		return "", err
	}
	return value, requireSet(driver, key, value)
}

func requireSet(driver, key, value string) error {
	if value == "" {
		return fmt.Errorf("%s: %s is not configured", driver, key)
	}
	return nil
}

func (c *Config) read(group, key string) (string, error) {
	value, err := c.settings.String(group, key)
	if err != nil {
		return "", fmt.Errorf("mailer config: %s %s: %w", group, key, err)
	}
	return value, nil
}
