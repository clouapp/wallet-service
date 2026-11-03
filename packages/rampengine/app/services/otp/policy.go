package otp

import (
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/identity"
	"github.com/macrowallets/waas/packages/rampengine/app/settings"
)

// SettingsReader is the slice of the settings service the policy reads.
type SettingsReader interface {
	Int(group, key string) (int, error)
}

// Policy answers the two per-send questions: how long a new code lives and how
// long before another may be mailed. Both are read at send time so an admin's
// change applies to the next code without a restart.
type Policy interface {
	TTL(purpose identity.OTPPurpose) time.Duration
	ResendCooldown() time.Duration
}

var _ Policy = (*SettingsPolicy)(nil)

// SettingsPolicy satisfies Policy from the `otp` settings group.
//
// A settings read error never fails a send: it logs and falls back to the registry default,
// because refusing every login over a cache outage is the worse failure.
type SettingsPolicy struct {
	settings        SettingsReader
	ttlFromSettings bool
	defaultTTL      time.Duration
	overrides       map[identity.OTPPurpose]time.Duration
}

// NewClientPolicy reads the code lifetime from otp.code_ttl_minutes.
func NewClientPolicy(s SettingsReader) *SettingsPolicy {
	return &SettingsPolicy{settings: s, ttlFromSettings: true}
}

// NewAdminPolicy keeps the configured admin lifetimes — deliberately shorter
// for login codes — and shares only the cooldown with the client.
func NewAdminPolicy(s SettingsReader, defaultTTL time.Duration, overrides map[identity.OTPPurpose]time.Duration) *SettingsPolicy {
	return &SettingsPolicy{settings: s, defaultTTL: defaultTTL, overrides: overrides}
}

func (p *SettingsPolicy) TTL(purpose identity.OTPPurpose) time.Duration {
	if !p.ttlFromSettings {
		if d, ok := p.overrides[purpose]; ok {
			return d
		}
		return p.defaultTTL
	}
	return time.Duration(p.intSetting(settings.KeyOTPCodeTTLMinutes, settings.DefaultOTPCodeTTLMinutes)) * time.Minute
}

func (p *SettingsPolicy) ResendCooldown() time.Duration {
	return time.Duration(p.intSetting(settings.KeyOTPResendCooldownSeconds, settings.DefaultOTPResendCooldownSeconds)) * time.Second
}

func (p *SettingsPolicy) intSetting(key string, def int) int {
	n, err := p.settings.Int(settings.GroupOTP, key)
	if err == nil && n > 0 {
		return n
	}
	if err != nil {
		facades.Log().Warningf("otp: read setting %s.%s: %v — using default %d", settings.GroupOTP, key, err, def)
	}
	return def
}
