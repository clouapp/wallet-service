// Package settings declares the fixed catalog of known configuration settings.
// The settings table stores only values; everything else (type, label, default,
// secret flag) is defined here. Defaults are resolved lazily from the
// .env-backed config so that, with no stored value, the .env remains the
// effective fallback.
package settings

import (
	"fmt"
	"slices"
	"strconv"
	"sync"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/mailer"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// Type is the value type of a setting, used for casting and UI rendering.
type Type string

const (
	TypeString Type = "string"
	TypeInt    Type = "int"
	TypeBool   Type = "bool"
	// TypeDecimal is a fixed-point number carried as a string end to end. Money
	// and percentages never become a float: JSON has one numeric type and it
	// loses cents.
	TypeDecimal Type = "decimal"
	// TypeStringList is an ordered list of values from a closed vocabulary. It
	// is stored as CSV, so a list setting reads the same as it always did.
	TypeStringList Type = "string_list"
	// TypeRSAPublicKeyPEM is a PEM-armoured RSA public key.
	//
	// It is a type rather than a per-setting validator for the reason Options
	// is: the check belongs to the shape of the value, so every setting that
	// declares it gets the rule, and the admin form learns from the same
	// declaration that this field needs more than one line.
	//
	// That second half is not cosmetic. A PEM pasted into a single-line input
	// arrives with its line breaks gone, `pem.Decode` refuses it, and until
	// this type existed the mangled key was encrypted and stored without
	// complaint — surfacing days later as an unexplained 401 on every webhook
	// delivery.
	TypeRSAPublicKeyPEM Type = "rsa_public_key_pem"
	// TypeECPrivateKeyPEM is a PEM-armoured PKCS#8 P-256 private key: the .p8
	// file APNs issues. It is a type for TypeRSAPublicKeyPEM's reasons: the
	// key is checked the way the APNs transport reads it while the plaintext
	// still exists, and the form renders a field its line breaks survive.
	TypeECPrivateKeyPEM Type = "ec_private_key_pem"
	// TypeServiceAccountJSON is a Google service account key file, pasted
	// whole. It is checked the way FCM reads it, so a file the transport would
	// refuse is refused on save rather than on the first notification.
	TypeServiceAccountJSON Type = "service_account_json"
)

// GroupMailDelivery is the settings group holding the mail driver choice and
// the global sender. The name is the mailer package's, so the registry and the
// transports cannot disagree on it.
const GroupMailDelivery = mailer.GroupDelivery

// GroupAuth is the settings group for admin authentication knobs.
const GroupAuth = "auth"

// SectionAuthentication is the settings page for sign-in and one-time codes.
// It is not named "auth" because that is also a group name and would make
// /settings/auth ambiguous between the page and the group API.
const SectionAuthentication = "authentication"

// Definition describes one known setting.
type Definition struct {
	Key    string
	Label  string
	Help   string
	Type   Type
	Secret bool
	// Destination is where the group's secrets are sent. Moving it must not
	// take a stored secret along, so the write that moves it re-enters them.
	Destination bool
	// Options is the closed vocabulary a value may come from. Declaring it
	// moves the check into the type layer — every group with an enum gets the
	// rule without writing a validator — and tells the admin form to render a
	// picker instead of a free text field, so a wrong value stops being
	// something an operator can type at all.
	Options []string
	// UpdatePermission is a permission an admin must hold to write this one
	// key, on top of the group's. Empty means the group's rule is enough.
	UpdatePermission string
	Default          func() any // resolved lazily, typically from facades.Config().Get(...)
}

// Group is one unit of saving: its identity, who may read and write it, and the
// cross-field rule that must hold before a write lands. It is not a page — see
// Section.
//
// An empty permission means the group is reachable with the settings.* pair the
// route middleware already requires. A group that names its own permission
// requires that one *in addition*, which is how a group holding live payment
// credentials stays out of reach of an operator who may edit SMTP.
type Group struct {
	Name     string
	Settings []Definition
	// Section is the settings page this group appears on. Empty means the group
	// is a page of its own, which is the ordinary case.
	//
	// Saving, permissions and validation are all per group; navigation is not.
	// Five groups that are really one decision — which providers exist and how
	// each rail picks between them — belong on one page, and tying the nav to
	// the save unit was what turned that decision into five pages.
	Section string
	// Block titles a run of groups within a section. Empty renders no heading.
	Block            string
	ViewPermission   string
	UpdatePermission string
	// Validate receives the effective post-write state of the group's
	// non-secret settings. It runs before anything is persisted.
	Validate func(effective map[string]string) error
}

// SectionName is the page this group belongs to, defaulting to the group itself.
func (g Group) SectionName() string {
	if g.Section == "" {
		return g.Name
	}
	return g.Section
}

// Registry returns the ordered catalog of all known settings groups. It is one
// shared instance: callers must not modify it.
func Registry() []Group {
	return catalog()
}

// maxOTPAttemptsCeiling keeps the admin code cap a cap: raised without bound,
// it would leave every admin code to the per-address limiter alone.
const maxOTPAttemptsCeiling = 20

func validateAuthGroup(effective map[string]string) error {
	if n, err := strconv.Atoi(effective["max_otp_attempts"]); err == nil && n > maxOTPAttemptsCeiling {
		return fmt.Errorf("max_otp_attempts must be at most %d", maxOTPAttemptsCeiling)
	}
	return nil
}

// catalog is built once, because every (group, key) read goes through it —
// several per ramp routing decision. Memoising freezes no .env value: every
// config read sits inside a Default closure and still resolves at call time.
// Clipped, so an append by a caller copies instead of writing into it.
var catalog = sync.OnceValue(func() []Group {
	groups := append(mailGroups(), Group{
		Name:    GroupAuth,
		Section: SectionAuthentication,
		Settings: []Definition{
			{Key: "max_otp_attempts", Label: "Max OTP Attempts", Type: TypeInt,
				Help:    "Failed guesses allowed against a single login code before it is invalidated.",
				Default: func() any { return facades.Config().GetInt("admin.login.max_otp_attempts") }},
		},
		Validate: validateAuthGroup,
	})
	groups = append(groups, otpGroups()...)
	groups = append(groups, walletRestoreGroups()...)
	groups = append(groups, rampGroups()...)
	groups = append(groups, postalGroups()...)
	groups = append(groups, companyRegistryGroups()...)
	return slices.Clip(append(groups, pushGroups()...))
})

// GroupsInSection returns the groups shown on one settings page, in registry
// order. A group with no Section is a page of its own, so its own name is its
// section (see SectionName). Empty means no such page.
func GroupsInSection(section string) []Group {
	var out []Group
	for _, g := range Registry() {
		if g.SectionName() == section {
			out = append(out, g)
		}
	}
	return out
}

func FindGroup(name string) (Group, bool) {
	for _, g := range Registry() {
		if g.Name == name {
			return g, true
		}
	}
	return Group{}, false
}

// ByGroup returns the definitions belonging to a group, in registry order.
func ByGroup(group string) []Definition {
	g, ok := FindGroup(group)
	if !ok {
		return []Definition{}
	}
	return g.Settings
}

func Find(group, key string) (Definition, bool) {
	for _, d := range ByGroup(group) {
		if d.Key == key {
			return d, true
		}
	}
	return Definition{}, false
}
