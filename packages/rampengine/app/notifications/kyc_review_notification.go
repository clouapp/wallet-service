package notifications

import (
	"strconv"

	"github.com/goravel/framework/contracts/translation"
	"github.com/goravel/framework/support/carbon"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// KycReviewNotification tells the users who act for a customer that a
// provider decided on its verification.
type KycReviewNotification struct {
	customerID uint
	provider   string
	status     string
	// decidedAt tells one rejection from the next: a user who fixes their
	// documents and is refused again is told again.
	decidedAt int64
	// categories are why a rejection refused, for the e-mail of a business.
	categories []string
	// business is the name of the company the verdict is about, empty for a
	// person (ForBusiness).
	business string
}

// NewKycReviewNotification announces the verdict row now holds, coming from
// previous, and answers false when there is none to announce: a status that
// is not a verdict -- under review, incomplete and the rest are steps the app
// walks the user through while they are in it -- or a verdict the row already
// held, which a requirements change or a hosted link rewrites unchanged.
func NewKycReviewNotification(previous string, row *models.CustomerProviderKyc) (*KycReviewNotification, bool) {
	if row.KycStatus == previous {
		return nil, false
	}
	if row.KycStatus != models.KycStatusApproved && row.KycStatus != models.KycStatusRejected {
		return nil, false
	}
	return &KycReviewNotification{
		customerID: row.CustomerID, provider: row.Provider, status: row.KycStatus, decidedAt: carbon.Now().Timestamp(),
		categories: row.DecodedRejectionCategories(),
	}, true
}

// ID implements Notification.
//
// An approval is one per customer, whichever provider gave it: a submission
// fans out to every provider, and "your identity was verified" is one fact
// however many of them confirm it. A rejection is one per decision, because
// each names its own fix. Keyed by the customer, so a company's verdict never
// claims its member's own; a person's customer id is the user's, so a
// person's ids are the ones they always were.
func (n *KycReviewNotification) ID() string {
	customer := n.customer()
	if n.status == models.KycStatusApproved {
		return "kyc:" + customer + ":" + n.status
	}
	return "kyc:" + customer + ":" + n.provider + ":" + n.status + ":" + strconv.FormatInt(n.decidedAt, 10)
}

// CustomerID implements Notification.
func (n *KycReviewNotification) CustomerID() uint { return n.customerID }

// customer is the customer id as the ids and the push data write it.
func (n *KycReviewNotification) customer() string {
	return strconv.FormatUint(uint64(n.customerID), 10)
}

// ToPush implements Notification. Data carries the fields of the SSE
// kyc.updated frame, so the app opens the verification the same way it
// handles a live update — the customer's id among them, as a decimal string
// (push data is strings).
func (n *KycReviewNotification) ToPush(lang translation.Translator) pusher.Message {
	key := "push.kyc." + n.status
	msg := pusher.Message{
		Title: lang.Get(key + ".title"),
		Body:  lang.Get(key + ".body"),
		Data: map[string]string{
			"event":       "kyc.updated",
			"provider":    n.provider,
			"kyc_status":  n.status,
			"customer_id": n.customer(),
		},
		CollapseID: "kyc:" + n.provider,
	}
	if n.business != "" {
		return n.asBusiness(msg, lang)
	}
	return msg
}

var _ Notification = (*KycReviewNotification)(nil)
