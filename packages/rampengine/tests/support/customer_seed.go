package support

import (
	"testing"

	ormmodel "github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// IndividualCustomer is customer id as every account holds it: an individual,
// active, whose id is its user's.
func IndividualCustomer(id uint) *models.Customer {
	return &models.Customer{Model: ormmodel.Model{ID: id}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

// IndividualCustomers is a customer repository answering every id with that
// individual: what a unit test acting for accounts reads a customer as.
func IndividualCustomers(t *testing.T) *mocks.MockCustomerRepository {
	customers := mocks.NewMockCustomerRepository(t)
	customers.EXPECT().FindByID(testifymock.Anything).RunAndReturn(func(id uint) (*models.Customer, error) {
		return IndividualCustomer(id), nil
	}).Maybe()
	return customers
}

// IndividualMembers is a membership repository answering every customer with
// its one member, the user whose id it took: who hears of an individual
// customer's changes.
func IndividualMembers(t *testing.T) *mocks.MockCustomerMemberRepository {
	members := mocks.NewMockCustomerMemberRepository(t)
	members.EXPECT().MembersOf(testifymock.Anything).RunAndReturn(func(customerID uint) ([]uint, error) {
		return []uint{customerID}, nil
	}).Maybe()
	return members
}

func Ptr[T any](v T)*T{return &v}
