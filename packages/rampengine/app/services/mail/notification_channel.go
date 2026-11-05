package mail

import (
	"errors"
	"fmt"
	"strconv"
	"time"

	contractsmail "github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/translation"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/notifications"
)

// sentFor is how long a notification is remembered as mailed to a recipient,
// as push remembers one sent: longer than the webhook redeliveries and
// reconcile sweeps that can report one verdict again.
const sentFor = 7 * 24 * time.Hour

// Recipient is who a notification is mailed to: the account, the address and
// the language it is written in for them -- the application's fallback
// language when they have none (no device registered). Who the recipients
// are is the caller's to decide.
type Recipient struct {
	UserID uint
	Email  string
	Locale string
}

// NotificationChannel is the mail channel of app/notifications: what
// app/services/push is for push, for a notification whose Via lists mail.
type NotificationChannel struct{}

// NewNotificationChannel builds the channel.
func NewNotificationChannel() *NotificationChannel { return &NotificationChannel{} }

// Notify queues n for each recipient, rendered in their language, once per
// recipient and n.ID(), and does nothing for a notification whose Via does not
// list mail -- a person's KYC verdict, which goes by push alone.
//
// Each recipient is claimed apart, before their mail is queued: a claim per
// notification would let the first member's mail block every other member's
// for a week (KYB spec §5, F2, the bug push has). A claim whose mail could not
// be queued is released, so the next report of the same change tries again.
// One recipient that fails does not hold back the others; their errors are
// joined.
func (c *NotificationChannel) Notify(recipients []Recipient, n notifications.Notification) error {
	mailable, ok := n.(notifications.MailNotification)
	if !ok || !notifications.Goes(n, notifications.ChannelMail) {
		return nil
	}
	var errs []error
	for _, recipient := range recipients {
		key := "mail:sent:" + n.ID() + ":" + strconv.FormatUint(uint64(recipient.UserID), 10)
		if !facades.Cache().Add(key, true, sentFor) {
			continue
		}
		message := notifications.Translated(localeOf(recipient), func(lang translation.Translator) contractsmail.Mailable {
			return mailable.ToMail(lang, recipient.Email)
		})
		if err := facades.Mail().Queue(message); err != nil {
			facades.Cache().Forget(key)
			errs = append(errs, fmt.Errorf("mail: queue notification for user %d: %w", recipient.UserID, err))
		}
	}
	return errors.Join(errs...)
}

// localeOf is the language recipient reads, the application's fallback when
// they registered no device to say it.
func localeOf(recipient Recipient) string {
	if recipient.Locale != "" {
		return recipient.Locale
	}
	return facades.Config().GetString("app.fallback_locale")
}
