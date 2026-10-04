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

type WalletRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WalletRepository
}

func TestWalletRepositorySuite(t *testing.T) {
	suite.Run(t, new(WalletRepositoryTestSuite))
}

func (s *WalletRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWalletRepository(nil)
}

func (s *WalletRepositoryTestSuite) makeWallet(chainID string) *models.Wallet {
	return &models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: chainID + " wallet",
		MPCCustomerShare: "deadbeef", MPCShareIV: "cafebabe", MPCShareSalt: "feedface",
		MPCSecretARN: "arn:test", MPCPublicKey: "02abc", MPCCurve: "secp256k1",
	}
}

func (s *WalletRepositoryTestSuite) TestCreate_Success() {
	w := s.makeWallet("eth")
	err := s.repo.Create(context.Background(), w)
	s.NoError(err)
}

func (s *WalletRepositoryTestSuite) TestFindByID_Found() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), w))

	found, err := s.repo.FindByID(context.Background(), w.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("eth", found.Chain)
}

func (s *WalletRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WalletRepositoryTestSuite) TestFindAll() {
	s.Require().NoError(s.repo.Create(context.Background(), s.makeWallet("eth")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeWallet("btc")))

	wallets, err := s.repo.FindAll(context.Background())
	s.NoError(err)
	s.Len(wallets, 2)
}

func (s *WalletRepositoryTestSuite) TestSetStatus() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), w))

	err := s.repo.SetStatus(context.Background(), w.ID, "frozen")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), w.ID)
	s.NoError(err)
	s.Equal("frozen", found.Status)
}

func (s *WalletRepositoryTestSuite) TestActivateClearsTheCode() {
	w := s.makeWallet("eth")
	code := "123456"
	w.ActivationCode = &code
	w.Status = "pending"
	s.Require().NoError(s.repo.Create(context.Background(), w))

	err := s.repo.Activate(context.Background(), w.ID, "active")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), w.ID)
	s.NoError(err)
	s.Equal("active", found.Status)
	s.Nil(found.ActivationCode)
}

func (s *WalletRepositoryTestSuite) TestPaginateByAccountAndMemberKeepsOnlyActiveMemberships() {
	accountID := uuid.New()
	otherAccountID := uuid.New()
	userID := uuid.New()
	assigned := s.walletOn(accountID, "eth")
	otherChain := s.walletOn(accountID, "btc")
	suspended := s.walletOn(accountID, "eth")
	removed := s.walletOn(accountID, "eth")
	elsewhere := s.walletOn(otherAccountID, "eth")

	members := repositories.NewWalletUserRepository(nil)
	s.membership(members, assigned.ID, userID, "active")
	s.membership(members, otherChain.ID, userID, "active")
	s.membership(members, suspended.ID, userID, "suspended")
	s.membership(members, removed.ID, userID, "active")
	s.Require().NoError(members.SoftDelete(context.Background(), removed.ID, userID))
	s.membership(members, elsewhere.ID, userID, "active")

	got, total, err := s.repo.PaginateByAccountAndMember(context.Background(), accountID, userID, "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(2), total)
	s.ElementsMatch([]uuid.UUID{assigned.ID, otherChain.ID}, walletIDs(got))

	ethOnly, ethTotal, err := s.repo.PaginateByAccountAndMember(context.Background(), accountID, userID, "eth", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(1), ethTotal)
	s.Equal([]uuid.UUID{assigned.ID}, walletIDs(ethOnly))

	none, noneTotal, err := s.repo.PaginateByAccountAndMember(context.Background(), accountID, uuid.New(), "", 20, 0)
	s.Require().NoError(err)
	s.Equal(int64(0), noneTotal)
	s.Empty(none)

	_, _, err = s.repo.PaginateByAccountAndMember(context.Background(), accountID, uuid.Nil, "", 20, 0)
	s.EqualError(err, "list account wallets: user is required")
}

func (s *WalletRepositoryTestSuite) walletOn(accountID uuid.UUID, chainID string) *models.Wallet {
	wallet := s.makeWallet(chainID)
	wallet.AccountID = &accountID
	s.Require().NoError(s.repo.Create(context.Background(), wallet))
	return wallet
}

func (s *WalletRepositoryTestSuite) membership(members *repositories.WalletUserRepository, walletID, userID uuid.UUID, status string) {
	s.Require().NoError(members.Create(context.Background(), &models.WalletUser{
		ID: uuid.New(), WalletID: walletID, UserID: userID, Roles: "viewer", Status: status,
	}))
}

func walletIDs(wallets []models.Wallet) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		ids = append(ids, wallet.ID)
	}
	return ids
}
