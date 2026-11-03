// Package notifications holds what the app tells its users -- what each
// notification announces and how it reads in the recipient's language -- for
// every channel it goes out through: push, and e-mail for the ones that list
// it. It is to push and mail what app/mails is to email: the content, not the
// sending.
package notifications

import (
	"context"
	"slices"
	"sync"

	contractsmail "github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/translation"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
)

// The channels a notification goes out through, named as Goravel's
// notification module names them (notification.ChannelMail), so moving to
// it is a change of wiring, not of content.
const (
	ChannelPush = "push"
	ChannelMail = "mail"
)

// Notification is a notification before it is rendered for a recipient.
type Notification interface {
	// ID names what the notification announces. One ID is sent once: a
	// provider that reports the same status twice reaches the user once.
	ID() string
	// CustomerID is the customer whose affairs it announces: it reaches a
	// user only while they still act for that customer (KYB spec §5 F2). Zero
	// is the recipient's own, which they always act for.
	CustomerID() uint
	// ToPush renders the notification with lang, which is set to the
	// recipient device's locale. The texts live in lang/<locale>/push.json.
	ToPush(lang translation.Translator) pusher.Message
}

// Router is a notification that names the channels it goes out through, as
// a Goravel notification's Via does.
type Router interface {
	Via() []string
}

// MailNotification is a notification that can go out by e-mail: ToMail
// renders it for the address to, in lang, as a Goravel notification's
// ToMail does. Whether it does go is its Via's answer. The texts live in
// lang/<locale>/mail.json.
type MailNotification interface {
	Notification
	ToMail(lang translation.Translator, to string) contractsmail.Mailable
}

// PlatformFilter is a notification that reaches only some of a recipient's
// devices, by platform (pusher.Platform*): a business's, which skips iOS
// until the iOS app knows companies (KYB spec §15).
type PlatformFilter interface {
	ReachesPlatform(platform string) bool
}

// ReachesPlatform reports whether n goes to a device of platform: every
// platform, for a notification that names none.
func ReachesPlatform(n Notification, platform string) bool {
	if filter, ok := n.(PlatformFilter); ok {
		return filter.ReachesPlatform(platform)
	}
	return true
}

// ChannelsOf is the channels n goes out through: its Via, or push alone for a
// notification that names none -- every notification before e-mail was one.
func ChannelsOf(n Notification) []string {
	if router, ok := n.(Router); ok {
		return router.Via()
	}
	return []string{ChannelPush}
}

// Goes reports whether n goes out through channel.
func Goes(n Notification, channel string) bool {
	return slices.Contains(ChannelsOf(n), channel)
}

// renderMu serializes rendering for every channel. The framework's translator
// keeps the files it loaded in one package-level map but locks it per
// instance, and every facades.Lang call is a new instance: two first renders
// of a locale at once -- the queue worker and a request share this process,
// and push and mail render apart -- would write that map concurrently, which
// the runtime does not survive.
var renderMu sync.Mutex

// Translated runs render with a translator set to locale, one render at a
// time.
func Translated[T any](locale string, render func(translation.Translator) T) T {
	renderMu.Lock()
	defer renderMu.Unlock()
	lang := facades.Lang(context.Background())
	lang.SetLocale(locale)
	return render(lang)
}
