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

// TestFindByExternalUserIDAndAccount_FiltersByAccount guards the IDOR fix on
// GET /api/v1/users/{external_id}/addresses: if two accounts each register an
// address for the same external_user_id, a query from account A must never
// return B's address. The filter is applied via the wallet's account_id.
func (s *AddressRepositoryTestSuite) TestFindByExternalUserIDAndAccount_FiltersByAccount() {
	accountA := mocks.InsertAccount(s.T(), "acc-A")
	accountB := mocks.InsertAccount(s.T(), "acc-B")

	walletA := mocks.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	walletB := mocks.InsertWalletWithAccount(s.T(), "eth", &accountB.ID)

	mocks.InsertAddress(s.T(), walletA.ID, "eth", "0xAAA1", "user_shared", 1)
	mocks.InsertAddress(s.T(), walletA.ID, "eth", "0xAAA2", "user_shared", 2)
	mocks.InsertAddress(s.T(), walletB.ID, "eth", "0xBBB1", "user_shared", 1)

	addrsA, err := s.repo.FindByExternalUserIDAndAccount("user_shared", accountA.ID)
	s.NoError(err)
	s.Len(addrsA, 2)
	for _, a := range addrsA {
		s.Equal(walletA.ID, a.WalletID, "account A must only see wallet A's addresses")
	}

	addrsB, err := s.repo.FindByExternalUserIDAndAccount("user_shared", accountB.ID)
	s.NoError(err)
	s.Len(addrsB, 1)
	s.Equal(walletB.ID, addrsB[0].WalletID)
}

// TestFindByExternalUserIDAndAccount_ExcludesUnassignedWallets confirms that
// addresses on wallets without an account_id (e.g. legacy data) are NOT
// returned — the filter requires an exact account match, never NULL.
func (s *AddressRepositoryTestSuite) TestFindByExternalUserIDAndAccount_ExcludesUnassignedWallets() {
	account := mocks.InsertAccount(s.T(), "acc-scoped")
	unassignedWallet := mocks.InsertWallet(s.T(), "eth") // account_id = NULL
	mocks.InsertAddress(s.T(), unassignedWallet.ID, "eth", "0xLEGACY", "user_123", 1)

	addrs, err := s.repo.FindByExternalUserIDAndAccount("user_123", account.ID)
	s.NoError(err)
	s.Empty(addrs)
}

// TestFindByChainAndAddressAndAccount_FiltersByAccount guards the IDOR fix on
// GET /api/v1/addresses/{address}: a caller from account B must get
// (nil, nil) back even when the address exists under account A.
func (s *AddressRepositoryTestSuite) TestFindByChainAndAddressAndAccount_FiltersByAccount() {
	accountA := mocks.InsertAccount(s.T(), "acc-A")
	accountB := mocks.InsertAccount(s.T(), "acc-B")

	walletA := mocks.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	mocks.InsertAddress(s.T(), walletA.ID, "eth", "0xSECRET", "user_a", 1)

	found, err := s.repo.FindByChainAndAddressAndAccount("eth", "0xSECRET", accountA.ID)
	s.NoError(err)
	s.Require().NotNil(found)
	s.Equal("0xSECRET", found.Address)

	// Cross-account lookup must return (nil, nil), indistinguishable from
	// the genuinely-not-found case.
	shouldBeNil, err := s.repo.FindByChainAndAddressAndAccount("eth", "0xSECRET", accountB.ID)
	s.NoError(err)
	s.Nil(shouldBeNil)

	// Non-existent address — same (nil, nil) result.
	missing, err := s.repo.FindByChainAndAddressAndAccount("eth", "0xDOESNOTEXIST", accountA.ID)
	s.NoError(err)
	s.Nil(missing)
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
