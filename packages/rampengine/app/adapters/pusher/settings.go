package pusher

import (
	"fmt"
)

// The settings groups, one per vendor. A group is named for the service that
// holds the credentials, not for the platform it reaches: an operator pastes an
// APNs key into push_apns.
const (
	GroupAPNs = "push_apns"
	GroupFCM  = "push_fcm"
)

// The keys inside the push groups. They are constants because the registry and
// this reader have to name them identically, and a typo in either is a silent
// misread rather than a compile error.
const (
	SettingEnabled = "enabled"

	SettingTeamID   = "team_id"
	SettingBundleID = "bundle_id"
	// One APNs key per environment. A key restricted to an app's topics
	// reaches a single environment, and a team holds only a few that reach
	// both, so the panel takes each environment's key on its own. A team
	// scoped key that reaches both is pasted into both.
	SettingSandboxKeyID         = "sandbox_key_id"
	SettingSandboxPrivateKey    = "sandbox_private_key"
	SettingProductionKeyID      = "production_key_id"
	SettingProductionPrivateKey = "production_private_key"

	SettingServiceAccount = "service_account"
)

// PushSettingsSource is the narrow settings dependency this package reads.
//
// Declared here rather than imported so the transports never depend on the
// settings service, and so app/settings can import this package to declare the
// push groups without creating a cycle. The Push prefix keeps it from sharing
// a mock file with the ramp and mail sources, as MailSettingsSource explains.
type PushSettingsSource interface {
	String(group, key string) (string, error)
	Bool(group, key string) (bool, error)
	Secret(group, key string) (string, error)
}

// PlatformGroup is the settings group holding a platform's credentials.
func PlatformGroup(platform string) (string, error) {
	switch platform {
	case PlatformIOS:
		return GroupAPNs, nil
	case PlatformAndroid:
		return GroupFCM, nil
	}
	return "", fmt.Errorf("push: platform %q: %w", platform, ErrUnknownPlatform)
}

// Config reads the push stack's admin state out of settings. Transports read
// their own credentials through it on every send, so a key changed in the
// panel takes effect on the next notification without a restart.
type Config struct{ settings PushSettingsSource }

// NewConfig reads the push groups through settings.
func NewConfig(settings PushSettingsSource) *Config { return &Config{settings: settings} }

// Enabled reports whether an operator switched a platform's delivery on.
func (c *Config) Enabled(platform string) (bool, error) {
	group, err := PlatformGroup(platform)
	if err != nil {
		return false, err
	}
	enabled, err := c.settings.Bool(group, SettingEnabled)
	if err != nil {
		return false, fmt.Errorf("push config: %s %s: %w", group, SettingEnabled, err)
	}
	return enabled, nil
}

// Value returns one of a platform's non-secret settings, blank when unset.
func (c *Config) Value(platform, key string) (string, error) {
	group, err := PlatformGroup(platform)
	if err != nil {
		return "", err
	}
	value, err := c.settings.String(group, key)
	if err != nil {
		return "", fmt.Errorf("push config: %s %s: %w", group, key, err)
	}
	return value, nil
}

// RequireValue returns one of a platform's non-secret settings, refusing a
// blank one the way every transport refuses to send without it.
func (c *Config) RequireValue(platform, key string) (string, error) {
	value, err := c.Value(platform, key)
	if err != nil {
		return "", err
	}
	group, err := PlatformGroup(platform)
	if err != nil {
		return "", err
	}
	return value, requireSet(group, key, value)
}

// RequireSecret returns one of a platform's credentials in plaintext, refusing
// a blank one.
func (c *Config) RequireSecret(platform, key string) (string, error) {
	group, err := PlatformGroup(platform)
	if err != nil {
		return "", err
	}
	value, err := c.settings.Secret(group, key)
	if err != nil {
		return "", fmt.Errorf("push config: %s %s: %w", group, key, err)
	}
	return value, requireSet(group, key, value)
}

func requireSet(group, key, value string) error {
	if value == "" {
		return fmt.Errorf("%s: %s is not configured", group, key)
	}
	return nil
}
