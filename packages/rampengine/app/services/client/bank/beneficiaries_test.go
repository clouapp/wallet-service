package bank

import (
	"errors"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

func TestCreateNormalizesValidatesAndDerives(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	in := validInput()
	in.RoutingNumber, in.AccountNumber = "021 000 021", "1234-56789"
	repo.EXPECT().Create(uint(7), testifymock.MatchedBy(func(w repositories.BankBeneficiaryWrite) bool {
		return w.RoutingNumber == "021000021" && w.AccountNumber == "123456789" &&
			w.AccountLast4 == "6789" && w.RoutingLast4 == "0021" &&
			w.Fingerprint == Fingerprint("021000021", "123456789")
	})).Return(&repositories.BankBeneficiaryRecord{ExternalID: "x", Revision: 1}, nil)

	got, err := NewService(repo).Create(7, in)
	require.NoError(t, err)
	require.Equal(t, 1, got.Revision)
}

func TestCreateRefusesInvalidDataWithoutWriting(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	in := validInput()
	in.RoutingNumber = "123456789"
	_, err := NewService(repo).Create(7, in)
	var fe *FieldError
	require.True(t, errors.As(err, &fe))
	require.Equal(t, "routing_number", fe.Field)
	// The mock fails the test on any unexpected call: nothing was written.
}

func TestUpdatePassesTheExternalIDAndWrapsRepositoryErrors(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	repo.EXPECT().Update(uint(7), "ext", testifymock.Anything).Return(nil, repositories.ErrBankBeneficiaryDuplicate)
	_, err := NewService(repo).Update(7, "ext", validInput())
	require.ErrorIs(t, err, ErrBankBeneficiaryDuplicate)
}

func TestGetAndDeleteKeepNotFound(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	repo.EXPECT().FindByExternalID(uint(7), "ext").Return(nil, repositories.ErrBankBeneficiaryNotFound)
	repo.EXPECT().Delete(uint(7), "ext").Return(repositories.ErrBankBeneficiaryNotFound)
	s := NewService(repo)
	_, err := s.Get(7, "ext")
	require.ErrorIs(t, err, ErrBankBeneficiaryNotFound)
	require.ErrorIs(t, s.Delete(7, "ext"), ErrBankBeneficiaryNotFound)
}

func TestGetForPayoutAnswersTheRecordAndItsRowID(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	repo.EXPECT().FindByExternalID(uint(7), "ext").Return(&repositories.BankBeneficiaryRecord{
		ID: 42, ExternalID: "ext", Revision: 3,
		BankBeneficiaryWrite: repositories.BankBeneficiaryWrite{
			AccountType: "checking", AccountNumber: "123456789", AccountLast4: "6789", RoutingLast4: "0021",
		},
	}, nil)

	got, err := NewService(repo).GetForPayout(7, "ext")
	require.NoError(t, err)
	require.Equal(t, uint(42), got.ID)
	require.Equal(t, "ext", got.ExternalID)
	require.Equal(t, 3, got.Revision)
	require.Equal(t, "checking", got.AccountType)
	require.Equal(t, "6789", got.AccountLast4)
	require.Equal(t, "0021", got.RoutingLast4)
}

func TestGetForPayoutKeepsNotFound(t *testing.T) {
	repo := mocks.NewMockCustomerBankBeneficiaryRepository(t)
	repo.EXPECT().FindByExternalID(uint(7), "ext").Return(nil, repositories.ErrBankBeneficiaryNotFound)
	_, err := NewService(repo).GetForPayout(7, "ext")
	require.ErrorIs(t, err, ErrBankBeneficiaryNotFound)
}
