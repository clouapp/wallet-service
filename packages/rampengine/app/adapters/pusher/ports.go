// Package pusher is the push notification stack: one Transport per platform,
// the settings they read, and the factory that resolves a platform to one.
//
// It mirrors app/adapters/mailer with the platform in the driver's place. An
// iOS device is reachable only through APNs and an Android one only through
// FCM, so there is no driver to pick: each platform is switched on or off.
//
// Both transports speak to the vendor's HTTP API directly. A vendor SDK would
// add nothing but that HTTP call and the JWT that authenticates it.
package pusher

import (
	"context"
	"time"
)

// The platform vocabulary. It is declared here, not in app/settings, for the
// reason mailer declares its drivers: the transports read it and must not
// import the settings registry, while the registry may import this.
const (
	PlatformIOS     = "ios"
	PlatformAndroid = "android"
)

// The APNs environments. A device token is valid only against the host of the
// environment that issued it, and only the app knows which one that was: a
// build run from Xcode talks to the sandbox, a TestFlight or App Store build
// to production.
const (
	EnvironmentSandbox    = "sandbox"
	EnvironmentProduction = "production"
)

// DeliveryWindow is how long a vendor keeps trying a device that is offline. A
// status older than a day is history the app shows on its own when opened.
const DeliveryWindow = 24 * time.Hour

// Platforms is the closed vocabulary of platforms a device registers as.
func Platforms() []string {
	return []string{PlatformIOS, PlatformAndroid}
}

// Environments is the closed vocabulary of APNs environments.
func Environments() []string {
	return []string{EnvironmentSandbox, EnvironmentProduction}
}

// Message is a notification already rendered in its recipient's language.
// Rendering happens before the queue, as a mail's does; the worker only
// transports.
type Message struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	// Data reaches the app with the notification, for it to open the right
	// screen when the notification is tapped. Values are strings because that
	// is all an FCM data map carries, and APNs gets the same shape so the two
	// apps read one contract.
	Data map[string]string `json:"data,omitempty"`
	// CollapseID makes a newer notification about the same subject replace
	// the older one on the device instead of stacking under it.
	CollapseID string `json:"collapse_id,omitempty"`
}

// Device is where a message is delivered.
type Device struct {
	Platform string `json:"platform"`
	Token    string `json:"token"`
	// Environment is the APNs environment that issued the token. Empty on
	// Android, where there is only one.
	Environment string `json:"environment,omitempty"`
}

// Transport is one platform's send path.
type Transport interface {
	Platform() string
	Send(ctx context.Context, device Device, msg Message) error
}
