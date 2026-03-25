package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WalletUserRepositoryTestSuite struct {
	suite.Suite
	repo       repositories.WalletUserRepository
	walletRepo repositories.WalletRepository
}

func TestWalletUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(WalletUserRepositoryTestSuite))
}

func (s *WalletUserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWalletUserRepository()
	s.walletRepo = repositories.NewWalletRepository()
}

func (s *WalletUserRepositoryTestSuite) createWallet() uuid.UUID {
	w := &models.Wallet{
		ID: uuid.New(), Chain: "eth", Label: "test",
		MPCCustomerShare: "aa", MPCShareIV: "bb", MPCShareSalt: "cc",
		MPCSecretARN: "arn:test", MPCPublicKey: "02", MPCCurve: "secp256k1",
		DepositAddress: "0x" + uuid.NewString()[:8],
	}
	s.Require().NoError(s.walletRepo.Create(w))
	return w.ID
}

func (s *WalletUserRepositoryTestSuite) TestCreate_Success() {
	walletID := s.createWallet()
	wu := &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "owner", Status: "active"}
	err := s.repo.Create(wu)
	s.NoError(err)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletID() {
	walletID := s.createWallet()
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "owner", Status: "active"}))
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "viewer", Status: "active"}))

	members, err := s.repo.FindByWalletID(walletID)
	s.NoError(err)
	s.Len(members, 2)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletID_ExcludesSoftDeleted() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))
	s.Require().NoError(s.repo.SoftDelete(walletID, userID))

	members, err := s.repo.FindByWalletID(walletID)
	s.NoError(err)
	s.Len(members, 0)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUser_Found() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "admin", Status: "active"}))

	wu, err := s.repo.FindByWalletAndUser(walletID, userID)
	s.NoError(err)
	s.NotNil(wu)
	s.Equal("admin", wu.Roles)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUser_NotFound() {
	wu, err := s.repo.FindByWalletAndUser(uuid.New(), uuid.New())
	s.NoError(err)
	s.Nil(wu)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUserIncludeDeleted() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))
	s.Require().NoError(s.repo.SoftDelete(walletID, userID))

	active, err := s.repo.FindByWalletAndUser(walletID, userID)
	s.NoError(err)
	s.Nil(active)

	withDeleted, err := s.repo.FindByWalletAndUserIncludeDeleted(walletID, userID)
	s.NoError(err)
	s.NotNil(withDeleted)
	s.NotNil(withDeleted.DeletedAt)
}

func (s *WalletUserRepositoryTestSuite) TestUpdateField() {
	walletID := s.createWallet()
	userID := uuid.New()
	wu := &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}
	s.Require().NoError(s.repo.Create(wu))

	err := s.repo.UpdateField(wu.ID, "roles", "admin")
	s.NoError(err)

	found, err := s.repo.FindByWalletAndUser(walletID, userID)
	s.NoError(err)
	s.Equal("admin", found.Roles)
}

func (s *WalletUserRepositoryTestSuite) TestSoftDelete() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(&models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))

	err := s.repo.SoftDelete(walletID, userID)
	s.NoError(err)

	found, err := s.repo.FindByWalletAndUser(walletID, userID)
	s.NoError(err)
	s.Nil(found)
}
