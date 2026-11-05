package push

import (
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/notifications"
)

// Notifier is what the ramp and KYC flows tell about a change a provider
// made, beside the live stream they already publish to. Each method decides
// whether the change is worth a notification, and sends it at most once.
//
// Like the live stream, it is best-effort and never fails its caller: the
// change it announces has already committed, and the row stays the source of
// truth the app reads when opened.
type Notifier interface {
	TransactionUpdated(tx *models.Transaction)
	// KycUpdated is told the status the row held before the change, so a
	// verdict is announced when it is reached rather than whenever a row
	// holding one is written.
	KycUpdated(previous string, row *models.CustomerProviderKyc)
}

// TransactionUpdated announces an order that reached the status ending it to
// every user who acts for its customer, a company's off their iOS devices (API
// contract §11.2). A customer that is gone is told nothing.
func (s *Service) TransactionUpdated(tx *models.Transaction) {
	n, ok := notifications.NewTransactionStatusNotification(tx)
	if !ok {
		return
	}
	customer, err := s.customers.FindByID(tx.CustomerID)
	if err != nil {
		facades.Log().Errorf("push: read customer %d of order %s: %v", tx.CustomerID, tx.ExternalID, err)
		return
	}
	if customer == nil {
		facades.Log().Warningf("push: order %s names customer %d, which is gone", tx.ExternalID, tx.CustomerID)
		return
	}
	if customer.IsBusiness() {
		n = n.ForBusiness()
	}
	members, err := s.members.MembersOf(tx.CustomerID)
	if err != nil {
		facades.Log().Errorf("push: who acts for customer %d: %v", tx.CustomerID, err)
		return
	}
	if err := s.Notify(members, n); err != nil {
		facades.Log().Errorf("push: notify order %s %s: %v", tx.ExternalID, tx.Status, err)
	}
}

// KycUpdated announces a provider's verdict on a customer's verification to
// every user who acts for it.
func (s *Service) KycUpdated(previous string, row *models.CustomerProviderKyc) {
	n, ok := notifications.NewKycReviewNotification(previous, row)
	if !ok {
		return
	}
	members, err := s.members.MembersOf(row.CustomerID)
	if err != nil {
		facades.Log().Errorf("push: who acts for customer %d: %v", row.CustomerID, err)
		return
	}
	if err := s.Notify(members, n); err != nil {
		facades.Log().Errorf("push: notify %s kyc %s: %v", row.Provider, row.KycStatus, err)
	}
}

var _ Notifier = (*Service)(nil)
