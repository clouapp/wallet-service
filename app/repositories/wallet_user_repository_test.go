package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WalletUserRepositoryTestSuite struct {
	suite.Suite
	repo       *repositories.WalletUserRepository
	walletRepo *repositories.WalletRepository
}

func TestWalletUserRepositorySuite(t *testing.T) {
	suite.Run(t, new(WalletUserRepositoryTestSuite))
}

func (s *WalletUserRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWalletUserRepository(nil)
	s.walletRepo = repositories.NewWalletRepository(nil)
}

func (s *WalletUserRepositoryTestSuite) createWallet() uuid.UUID {
	w := &models.Wallet{
		ID: uuid.New(), Chain: "eth", Label: "test",
		MPCCustomerShare: "aa", MPCShareIV: "bb", MPCShareSalt: "cc",
		MPCSecretARN: "arn:test", MPCPublicKey: "02", MPCCurve: "secp256k1",
	}
	s.Require().NoError(s.walletRepo.Create(context.Background(), w))
	return w.ID
}

func (s *WalletUserRepositoryTestSuite) TestCreate_Success() {
	walletID := s.createWallet()
	wu := &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "owner", Status: "active"}
	err := s.repo.Create(context.Background(), wu)
	s.NoError(err)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletID() {
	walletID := s.createWallet()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "owner", Status: "active"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: uuid.New(), Roles: "viewer", Status: "active"}))

	members, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(members, 2)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletID_ExcludesSoftDeleted() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))
	s.Require().NoError(s.repo.SoftDelete(context.Background(), walletID, userID))

	members, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(members, 0)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUser_Found() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "admin", Status: "active"}))

	wu, err := s.repo.FindByWalletAndUser(context.Background(), walletID, userID)
	s.NoError(err)
	s.NotNil(wu)
	s.Equal("admin", wu.Roles)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUser_NotFound() {
	wu, err := s.repo.FindByWalletAndUser(context.Background(), uuid.New(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(wu)
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUser_IgnoresAMembershipThatIsNotActive() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "admin", Status: "suspended"}))

	wu, err := s.repo.FindByWalletAndUser(context.Background(), walletID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(wu)

	members, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(members, 1, "membership management still sees every status")
}

func (s *WalletUserRepositoryTestSuite) TestFindByWalletAndUserIncludeDeleted() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))
	s.Require().NoError(s.repo.SoftDelete(context.Background(), walletID, userID))

	active, err := s.repo.FindByWalletAndUser(context.Background(), walletID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(active)

	withDeleted, err := s.repo.FindByWalletAndUserIncludeDeleted(context.Background(), walletID, userID)
	s.NoError(err)
	s.NotNil(withDeleted)
	s.NotNil(withDeleted.DeletedAt)
}

func (s *WalletUserRepositoryTestSuite) TestUpdateField() {
	walletID := s.createWallet()
	userID := uuid.New()
	wu := &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}
	s.Require().NoError(s.repo.Create(context.Background(), wu))

	err := s.repo.SetRoles(context.Background(), wu.ID, "admin")
	s.NoError(err)

	found, err := s.repo.FindByWalletAndUser(context.Background(), walletID, userID)
	s.NoError(err)
	s.Equal("admin", found.Roles)
}

func (s *WalletUserRepositoryTestSuite) TestSoftDelete() {
	walletID := s.createWallet()
	userID := uuid.New()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WalletUser{ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: "active"}))

	err := s.repo.SoftDelete(context.Background(), walletID, userID)
	s.NoError(err)

	found, err := s.repo.FindByWalletAndUser(context.Background(), walletID, userID)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}
