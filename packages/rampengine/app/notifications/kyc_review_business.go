package notifications

import (
	"slices"

	contractsmail "github.com/goravel/framework/contracts/mail"
	"github.com/goravel/framework/contracts/translation"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
	"github.com/macrowallets/waas/packages/rampengine/app/mails"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ForBusiness is the verdict on the KYB of the business customer
// customerID, named by name, its display name (API contract §11): the same
// verdict told in the company's words, and by e-mail too (KYB spec §7.4b). It
// is its own notification, by the company's id, so its member's own verdict
// is neither deduplicated against it nor replaced by it on the device.
func (n *KycReviewNotification) ForBusiness(customerID uint, name string) *KycReviewNotification {
	business := *n
	business.customerID = customerID
	business.business = name
	return &business
}

// Via implements Router: a person's verdict goes by push, as it always did;
// a company's by push and e-mail.
func (n *KycReviewNotification) Via() []string {
	if n.business == "" {
		return []string{ChannelPush}
	}
	return []string{ChannelPush, ChannelMail}
}

// ReachesPlatform implements PlatformFilter: a company's verdict skips iOS,
// which knows no company yet (API contract §11.2); a person's reaches every
// device.
func (n *KycReviewNotification) ReachesPlatform(platform string) bool {
	return n.business == "" || platform != pusher.PlatformIOS
}

// asBusiness is msg in the company's words, collapsed apart from the
// person's: the company's verdict never replaces its member's own on the
// device (API contract §11.2).
func (n *KycReviewNotification) asBusiness(msg pusher.Message, lang translation.Translator) pusher.Message {
	key := "push.kyb." + n.status
	msg.Title = lang.Get(key+".title", n.named())
	msg.Body = lang.Get(key+".body", n.named())
	msg.CollapseID = "kyc:" + n.customer() + ":" + n.provider
	return msg
}

// ToMail implements MailNotification: the approval, what to fix for each
// correctable category of a rejection, or the refusal that ends it, in the
// words the app uses (API contract §11.3). It names the company and nothing
// else: no document number, no provider's words.
func (n *KycReviewNotification) ToMail(lang translation.Translator, to string) contractsmail.Mailable {
	subject, paragraphs := n.mailText(lang)
	return mails.NewNoticeMail(to, mails.Notice{
		Locale:     lang.CurrentLocale(),
		Subject:    subject,
		Title:      subject,
		Paragraphs: paragraphs,
		Footer:     lang.Get("mail.notice.business_footer"),
	})
}

// mailText is the subject and the paragraphs of the verdict.
func (n *KycReviewNotification) mailText(lang translation.Translator) (string, []string) {
	if n.status == models.KycStatusApproved {
		return lang.Get("mail.kyb.approved.subject", n.named()), []string{lang.Get("mail.kyb.approved.body", n.named())}
	}
	fixes := n.correctableFixes()
	if len(fixes) == 0 {
		return lang.Get("mail.kyb.not_eligible.subject", n.named()), []string{lang.Get("mail.kyb.not_eligible.body", n.named())}
	}
	paragraphs := make([]string, 0, len(fixes))
	for _, fix := range fixes {
		paragraphs = append(paragraphs, lang.Get("mail.kyb.correction."+fix, n.named()))
	}
	return lang.Get("mail.kyb.correction.subject", n.named()), paragraphs
}

// correctableFixes is the text key of each fix the rejection asks for, once
// each, in the order categories are presented; none when any category is
// terminal or none was given, which is a refusal nothing fixes. A selfie
// refusal, which Bridge never asks a partner for, is a document to send
// again.
func (n *KycReviewNotification) correctableFixes() []string {
	var fixes []string
	for _, category := range models.KycRejectionCategories {
		if !slices.Contains(n.categories, category) {
			continue
		}
		if models.KycRejectionIsTerminal(category) {
			return nil
		}
		fix := category
		if category == models.KycRejectionSelfieMismatch {
			fix = models.KycRejectionDocumentUnreadable
		}
		if !slices.Contains(fixes, fix) {
			fixes = append(fixes, fix)
		}
	}
	return fixes
}

func (n *KycReviewNotification) named() translation.Option {
	return translation.Option{Replace: map[string]string{"name": n.business}}
}

var (
	_ MailNotification = (*KycReviewNotification)(nil)
	_ PlatformFilter   = (*KycReviewNotification)(nil)
)
