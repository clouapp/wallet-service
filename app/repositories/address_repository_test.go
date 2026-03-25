package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type AddressRepositoryTestSuite struct {
	suite.Suite
	repo repositories.AddressRepository
}

func TestAddressRepositorySuite(t *testing.T) {
	suite.Run(t, new(AddressRepositoryTestSuite))
}

func (s *AddressRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewAddressRepository()
}

func (s *AddressRepositoryTestSuite) insertWallet(chainID string) uuid.UUID {
	w := mocks.InsertWallet(s.T(), chainID)
	return w.ID
}

func (s *AddressRepositoryTestSuite) TestCountByChainAndAddress() {
	walletID := s.insertWallet("eth")
	mocks.InsertAddress(s.T(), walletID, "eth", "0xABC", "user1", 0)

	count, err := s.repo.CountByChainAndAddress("eth", "0xABC")
	s.NoError(err)
	s.Equal(int64(1), count)

	count, err = s.repo.CountByChainAndAddress("eth", "0xNONE")
	s.NoError(err)
	s.Equal(int64(0), count)
}

func (s *AddressRepositoryTestSuite) TestFindByChainAndAddress_Found() {
	walletID := s.insertWallet("eth")
	mocks.InsertAddress(s.T(), walletID, "eth", "0xFIND", "user1", 0)

	addr, err := s.repo.FindByChainAndAddress("eth", "0xFIND")
	s.NoError(err)
	s.NotNil(addr)
	s.Equal("0xFIND", addr.Address)
}

func (s *AddressRepositoryTestSuite) TestFindByChainAndAddress_NotFound() {
	addr, err := s.repo.FindByChainAndAddress("eth", "0xNOPE")
	s.NoError(err)
	s.Nil(addr)
}

func (s *AddressRepositoryTestSuite) TestFindByExternalUserID() {
	walletID := s.insertWallet("eth")
	mocks.InsertAddress(s.T(), walletID, "eth", "0xA1", "user_ext", 0)
	mocks.InsertAddress(s.T(), walletID, "eth", "0xA2", "user_ext", 1)

	addrs, err := s.repo.FindByExternalUserID("user_ext")
	s.NoError(err)
	s.Len(addrs, 2)
}

func (s *AddressRepositoryTestSuite) TestFindByWalletID() {
	wA := s.insertWallet("eth")
	wB := s.insertWallet("btc")
	mocks.InsertAddress(s.T(), wA, "eth", "0xW1A", "u1", 0)
	mocks.InsertAddress(s.T(), wA, "eth", "0xW1B", "u2", 1)
	mocks.InsertAddress(s.T(), wB, "btc", "bc1q1", "u3", 0)

	addrs, err := s.repo.FindByWalletID(wA)
	s.NoError(err)
	s.Len(addrs, 2)
}

func (s *AddressRepositoryTestSuite) TestPluckActiveAddresses() {
	walletID := s.insertWallet("eth")
	mocks.InsertAddress(s.T(), walletID, "eth", "0xACTIVE1", "u1", 0)
	mocks.InsertAddress(s.T(), walletID, "eth", "0xACTIVE2", "u2", 1)

	inactive := mocks.InsertAddress(s.T(), walletID, "eth", "0xINACTIVE", "u3", 2)
	facades.Orm().Query().Model(&inactive).Where("id = ?", inactive.ID).Update("is_active", false)

	addrs, err := s.repo.PluckActiveAddresses("eth")
	s.NoError(err)
	s.Len(addrs, 2)
	s.Contains(addrs, "0xACTIVE1")
	s.Contains(addrs, "0xACTIVE2")
}
