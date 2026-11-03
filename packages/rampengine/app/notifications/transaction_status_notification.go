package notifications

import (
	"strconv"

	"github.com/goravel/framework/contracts/translation"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/pusher"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// TransactionStatusNotification tells the users who act for a customer that
// one of its orders reached the status that ends it.
type TransactionStatusNotification struct {
	customerID uint
	externalID string
	txType     string
	status     string
	// business marks a company's order (ForBusiness).
	business bool
}

// NewTransactionStatusNotification announces tx's status, and answers false
// for a status that does not end the order. The app shows those as they
// happen; a notification for each step would be noise.
func NewTransactionStatusNotification(tx *models.Transaction) (*TransactionStatusNotification, bool) {
	if !models.IsTerminalStatus(tx.Status) {
		return nil, false
	}
	if tx.Type != models.TransactionTypeOnramp && tx.Type != models.TransactionTypeOfframp {
		return nil, false
	}
	return &TransactionStatusNotification{
		customerID: tx.CustomerID, externalID: tx.ExternalID, txType: tx.Type, status: tx.Status,
	}, true
}

// ForBusiness is the same order's notification as a company's: it skips the
// iOS devices of its members until the iOS app knows companies (API contract
// §11.2), and is otherwise the same text and the same order.
func (n *TransactionStatusNotification) ForBusiness() *TransactionStatusNotification {
	business := *n
	business.business = true
	return &business
}

// ID implements Notification. A refund after completion is a status of its
// own, and announced as one.
func (n *TransactionStatusNotification) ID() string {
	return "transaction:" + n.externalID + ":" + n.status
}

// CustomerID implements Notification.
func (n *TransactionStatusNotification) CustomerID() uint { return n.customerID }

// ReachesPlatform implements PlatformFilter: a company's order skips iOS,
// which knows no company yet; a person's reaches every device.
func (n *TransactionStatusNotification) ReachesPlatform(platform string) bool {
	return !n.business || platform != pusher.PlatformIOS
}

// ToPush implements Notification. Data carries the fields of the SSE
// transaction.updated frame, so the app opens the order the same way it
// handles a live update — the customer's id among them, as a decimal string
// (push data is strings).
func (n *TransactionStatusNotification) ToPush(lang translation.Translator) pusher.Message {
	key := "push.transaction." + n.txType + "." + n.status
	return pusher.Message{
		Title: lang.Get(key + ".title"),
		Body:  lang.Get(key + ".body"),
		Data: map[string]string{
			"event":       "transaction.updated",
			"id":          n.externalID,
			"type":        n.txType,
			"status":      n.status,
			"customer_id": strconv.FormatUint(uint64(n.customerID), 10),
		},
		// One order shows as one notification: a refund replaces the
		// completion it reverses.
		CollapseID: "transaction:" + n.externalID,
	}
}

var (
	_ Notification   = (*TransactionStatusNotification)(nil)
	_ PlatformFilter = (*TransactionStatusNotification)(nil)
)
