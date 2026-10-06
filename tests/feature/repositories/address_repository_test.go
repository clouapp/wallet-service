package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type AddressRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.AddressRepository
}

func TestAddress_Repository_Suite(t *testing.T) {
	suite.Run(t, new(AddressRepositoryTestSuite))
}

func (s *AddressRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewAddressRepository(nil)
}

func (s *AddressRepositoryTestSuite) insertWallet(chainID string) uuid.UUID {
	w := fixtures.InsertWallet(s.T(), chainID)
	return w.ID
}

func (s *AddressRepositoryTestSuite) TestCount_By_ChainAndAddress() {
	walletID := s.insertWallet("eth")
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xABC", "user1", 0)

	count, err := s.repo.CountByChainAndAddress(context.Background(), "eth", "0xABC")
	s.Require().NoError(err)
	s.Equal(int64(1), count)

	count, err = s.repo.CountByChainAndAddress(context.Background(), "eth", "0xNONE")
	s.Require().NoError(err)
	s.Equal(int64(0), count)
}

func (s *AddressRepositoryTestSuite) TestFind_ByChainAndAddress_Found() {
	walletID := s.insertWallet("eth")
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xFIND", "user1", 0)

	addr, err := s.repo.FindByChainAndAddress(context.Background(), "eth", "0xFIND")
	s.Require().NoError(err)
	s.Require().NotNil(addr)
	s.Equal("0xFIND", addr.Address)
}

func (s *AddressRepositoryTestSuite) TestFind_ByChainAndAddress_NotFound() {
	addr, err := s.repo.FindByChainAndAddress(context.Background(), "eth", "0xNOPE")
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(addr)
}

func (s *AddressRepositoryTestSuite) TestFind_By_ExternalUserID() {
	walletID := s.insertWallet("eth")
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xA1", "user_ext", 0)
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xA2", "user_ext", 1)

	addrs, err := s.repo.FindByExternalUserID(context.Background(), "user_ext")
	s.Require().NoError(err)
	s.Len(addrs, 2)
}

func (s *AddressRepositoryTestSuite) TestFind_By_WalletID() {
	wA := s.insertWallet("eth")
	wB := s.insertWallet("btc")
	fixtures.InsertAddress(s.T(), wA, "eth", "0xW1A", "u1", 0)
	fixtures.InsertAddress(s.T(), wA, "eth", "0xW1B", "u2", 1)
	fixtures.InsertAddress(s.T(), wB, "btc", "bc1q1", "u3", 0)

	addrs, err := s.repo.FindByWalletID(context.Background(), wA)
	s.Require().NoError(err)
	// InsertWallet seeds the deposit address in addition to the two rows above.
	s.Len(addrs, 3)
	var got []string
	for _, addr := range addrs {
		got = append(got, addr.Address)
	}
	s.Contains(got, "0xW1A")
	s.Contains(got, "0xW1B")
}

// TestFindByExternalUserIDAndAccount_FiltersByAccount guards the IDOR fix on
// GET /api/v1/users/{external_id}/addresses: if two accounts each register an
// address for the same external_user_id, a query from account A must never
// return B's address. The filter is applied via the wallet's account_id.
func (s *AddressRepositoryTestSuite) TestFind_ByExternalUserIDAndAccount_FiltersByAccount() {
	accountA := fixtures.InsertAccount(s.T(), "acc-A")
	accountB := fixtures.InsertAccount(s.T(), "acc-B")

	walletA := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	walletB := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountB.ID)

	fixtures.InsertAddress(s.T(), walletA.ID, "eth", "0xAAA1", "user_shared", 1)
	fixtures.InsertAddress(s.T(), walletA.ID, "eth", "0xAAA2", "user_shared", 2)
	fixtures.InsertAddress(s.T(), walletB.ID, "eth", "0xBBB1", "user_shared", 1)

	addrsA, err := s.repo.FindByExternalUserIDAndAccount(context.Background(), "user_shared", accountA.ID)
	s.Require().NoError(err)
	s.Len(addrsA, 2)
	for _, a := range addrsA {
		s.Equal(walletA.ID, a.WalletID, "account A must only see wallet A's addresses")
	}

	addrsB, err := s.repo.FindByExternalUserIDAndAccount(context.Background(), "user_shared", accountB.ID)
	s.Require().NoError(err)
	s.Require().Len(addrsB, 1)
	s.Equal(walletB.ID, addrsB[0].WalletID)
}

// TestFindByExternalUserIDAndAccount_ExcludesUnassignedWallets confirms that
// addresses on wallets without an account_id (e.g. legacy data) are NOT
// returned — the filter requires an exact account match, never NULL.
func (s *AddressRepositoryTestSuite) TestFind_ByExternalUserIDAndAccount_ExcludesUnassignedWallets() {
	account := fixtures.InsertAccount(s.T(), "acc-scoped")
	unassignedWallet := fixtures.InsertWallet(s.T(), "eth") // account_id = NULL
	fixtures.InsertAddress(s.T(), unassignedWallet.ID, "eth", "0xLEGACY", "user_123", 1)

	addrs, err := s.repo.FindByExternalUserIDAndAccount(context.Background(), "user_123", account.ID)
	s.Require().NoError(err)
	s.Empty(addrs)
}

// TestFindByChainAndAddressAndAccount_FiltersByAccount guards the IDOR fix on
// GET /api/v1/addresses/{address}: a caller from account B must get the
// not-found sentinel even when the address exists under account A.
func (s *AddressRepositoryTestSuite) TestFind_ByChainAndAddressAndAccount_FiltersByAccount() {
	accountA := fixtures.InsertAccount(s.T(), "acc-A")
	accountB := fixtures.InsertAccount(s.T(), "acc-B")

	walletA := fixtures.InsertWalletWithAccount(s.T(), "eth", &accountA.ID)
	fixtures.InsertAddress(s.T(), walletA.ID, "eth", "0xSECRET", "user_a", 1)

	found, err := s.repo.FindByChainAndAddressAndAccount(context.Background(), "eth", "0xSECRET", accountA.ID)
	s.Require().NoError(err)
	s.Require().NotNil(found)
	s.Equal("0xSECRET", found.Address)

	// Cross-account lookup is the not-found sentinel, indistinguishable from
	// the genuinely-not-found case.
	shouldBeNil, err := s.repo.FindByChainAndAddressAndAccount(context.Background(), "eth", "0xSECRET", accountB.ID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(shouldBeNil)

	missing, err := s.repo.FindByChainAndAddressAndAccount(context.Background(), "eth", "0xDOESNOTEXIST", accountA.ID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(missing)
}

func (s *AddressRepositoryTestSuite) TestPluck_Active_Addresses() {
	walletID := s.insertWallet("eth")
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xACTIVE1", "u1", 0)
	fixtures.InsertAddress(s.T(), walletID, "eth", "0xACTIVE2", "u2", 1)

	inactive := fixtures.InsertAddress(s.T(), walletID, "eth", "0xINACTIVE", "u3", 2)
	facades.Orm().Query().Model(&inactive).Where("id = ?", inactive.ID).Update("is_active", false)

	addrs, err := s.repo.PluckActiveAddresses(context.Background(), "eth")
	s.Require().NoError(err)
	// InsertWallet seeds an active deposit address beside the two rows above.
	s.Len(addrs, 3)
	s.Contains(addrs, "0xACTIVE1")
	s.Contains(addrs, "0xACTIVE2")
	s.NotContains(addrs, "0xINACTIVE")
}
