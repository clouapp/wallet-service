package broadcast

import (
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// MemberDirectory says who acts for a customer:
// repositories.CustomerMemberRepository, the one answer the push reads too.
type MemberDirectory interface {
	MembersOf(customerID uint) ([]uint, error)
}

// Audience tells every user who acts for a customer what happened to it, on
// each one's own stream (KYB spec §2): the stream stays the user's, the events
// become the customer's, and each names the customer it is about. It is the
// one place an event of a customer fans out to its members.
//
// Best-effort like the stream itself: the change has committed, so a failure
// to read who to tell is logged, never returned.
type Audience struct {
	broadcaster Broadcaster
	members     MemberDirectory
}

// NewAudience wires the audience.
func NewAudience(broadcaster Broadcaster, members MemberDirectory) Audience {
	return Audience{broadcaster: broadcaster, members: members}
}

// Transaction publishes tx's event to every member of its customer.
func (a Audience) Transaction(tx *models.Transaction) {
	event := NewTransactionEvent(tx)
	for _, userID := range a.membersOf(tx.CustomerID, "order "+tx.ExternalID) {
		a.broadcaster.Publish(userID, event)
	}
}

// Kyc publishes event to every member of its customer.
func (a Audience) Kyc(event KycEvent) {
	for _, userID := range a.membersOf(event.CustomerID, event.Provider+" kyc") {
		a.broadcaster.PublishKyc(userID, event)
	}
}

func (a Audience) membersOf(customerID uint, what string) []uint {
	members, err := a.members.MembersOf(customerID)
	if err != nil {
		facades.Log().Errorf("broadcast: who acts for customer %d, to tell of its %s: %v", customerID, what, err)
		return nil
	}
	return members
}
