// Package customers is the customer a client request acts for: who holds the
// money, kept apart from who logs in (spec §3, §4).
package customers

import (
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// ErrCustomerNotFound is a customer the user is not a member of. It is also
// the answer for one that does not exist, so a request cannot learn which ids
// do.
var ErrCustomerNotFound = errors.New("customers: customer not found")

// Service resolves the customers a user acts for.
type Service struct {
	members repositories.CustomerMemberRepository
}

// NewService wires the service.
func NewService(members repositories.CustomerMemberRepository) *Service {
	return &Service{members: members}
}

// Membership is the user's membership of customerID, with its customer
// loaded, or ErrCustomerNotFound — also for a membership whose customer did
// not load, which names nothing a request could act for.
func (s *Service) Membership(user *models.User, customerID uint) (*models.CustomerMember, error) {
	member, err := s.members.FindByCustomerAndUser(customerID, user.ID)
	if err != nil {
		return nil, fmt.Errorf("customers: membership: %w", err)
	}
	if member == nil || member.Customer == nil {
		return nil, ErrCustomerNotFound
	}
	return member, nil
}
