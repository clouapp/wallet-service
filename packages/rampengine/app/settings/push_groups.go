package settings

import (
	"fmt"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
)

// The permissions the push groups declare. They are their own grants rather
// than settings.*, for the reason mail's are: a key that sends notifications
// to every installed app as XIP is a different blast radius from the sender's
// display name, and the standard operator holds settings.update but not
// push.update.
const (
	permPushView   = "push.view"
	permPushUpdate = "push.update"
)

// SectionPush is the settings page both push groups show on. Each still saves
// on its own; the section only decides what the nav lists.
const SectionPush = "push"

// pushGroups declares one group per vendor. Each platform is switched on by
// its own group: an iOS device is reachable only through APNs and an Android
// one only through FCM, so there is no driver to pick between them.
//
// Like the provider credentials, they live in settings alone, with no .env
// fallback: a group nobody saved is off and empty.
func pushGroups() []Group {
	return []Group{
		{
			Name: pusher.GroupAPNs,
			Settings: []Definition{
				{Key: pusher.SettingEnabled, Label: "Enabled", Type: TypeBool,
					Help:    "Deliver notifications to iOS devices.",
					Default: func() any { return false }},
				{Key: pusher.SettingTeamID, Label: "Team ID", Type: TypeString,
					Help:    "The Team ID of the Apple Developer account that created the keys.",
					Default: func() any { return "" }},
				{Key: pusher.SettingBundleID, Label: "Bundle ID", Type: TypeString,
					Help:    "The bundle identifier of the app this environment serves; APNs reads it as the topic.",
					Default: func() any { return "" }},
				{Key: pusher.SettingSandboxKeyID, Label: "Sandbox Key ID", Type: TypeString,
					Help:    "The key that reaches builds run from Xcode. Blank leaves them without notifications.",
					Default: func() any { return "" }},
				{Key: pusher.SettingSandboxPrivateKey, Label: "Sandbox Private Key (.p8)", Type: TypeECPrivateKeyPEM, Secret: true,
					Help:    "Paste the .p8 file whole, line breaks and BEGIN/END lines included.",
					Default: func() any { return "" }},
				{Key: pusher.SettingProductionKeyID, Label: "Production Key ID", Type: TypeString,
					Help:    "The key that reaches TestFlight and App Store builds. Blank leaves them without notifications.",
					Default: func() any { return "" }},
				{Key: pusher.SettingProductionPrivateKey, Label: "Production Private Key (.p8)", Type: TypeECPrivateKeyPEM, Secret: true,
					Help:    "Paste the .p8 file whole, line breaks and BEGIN/END lines included.",
					Default: func() any { return "" }},
			},
			Section:          SectionPush,
			ViewPermission:   permPushView,
			UpdatePermission: permPushUpdate,
			Validate:         validateAPNsGroup,
		},
		{
			Name: pusher.GroupFCM,
			Settings: []Definition{
				{Key: pusher.SettingEnabled, Label: "Enabled", Type: TypeBool,
					Help:    "Deliver notifications to Android devices.",
					Default: func() any { return false }},
				{Key: pusher.SettingServiceAccount, Label: "Service Account", Type: TypeServiceAccountJSON, Secret: true,
					Help:    "The JSON key file of a service account allowed to send through Firebase Cloud Messaging, pasted whole.",
					Default: func() any { return "" }},
			},
			Section:          SectionPush,
			ViewPermission:   permPushView,
			UpdatePermission: permPushUpdate,
		},
	}
}

// validateAPNsGroup refuses to switch iOS delivery on without the identifiers
// every send is signed and addressed with, and without a key for at least one
// environment. Either environment may go without: an installation can serve
// the stores alone. The keys themselves are secrets, which no group rule sees;
// the transport refuses to sign with one missing.
func validateAPNsGroup(effective map[string]string) error {
	if effective[pusher.SettingEnabled] != "true" {
		return nil
	}
	for _, key := range []string{pusher.SettingTeamID, pusher.SettingBundleID} {
		if strings.TrimSpace(effective[key]) == "" {
			return fmt.Errorf("%s is required to enable iOS delivery", key)
		}
	}
	if strings.TrimSpace(effective[pusher.SettingSandboxKeyID]) == "" &&
		strings.TrimSpace(effective[pusher.SettingProductionKeyID]) == "" {
		return fmt.Errorf("%s or %s is required to enable iOS delivery", pusher.SettingSandboxKeyID, pusher.SettingProductionKeyID)
	}
	return nil
}
