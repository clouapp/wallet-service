package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WalletRepositoryTestSuite struct {
	suite.Suite
	repo repositories.WalletRepository
}

func TestWalletRepositorySuite(t *testing.T) {
	suite.Run(t, new(WalletRepositoryTestSuite))
}

func (s *WalletRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWalletRepository()
}

func (s *WalletRepositoryTestSuite) makeWallet(chainID string) *models.Wallet {
	return &models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: chainID + " wallet",
		MPCCustomerShare: "deadbeef", MPCShareIV: "cafebabe", MPCShareSalt: "feedface",
		MPCSecretARN: "arn:test", MPCPublicKey: "02abc", MPCCurve: "secp256k1",
		DepositAddress: "0x" + uuid.NewString()[:16],
	}
}

func (s *WalletRepositoryTestSuite) TestCreate_Success() {
	w := s.makeWallet("eth")
	err := s.repo.Create(w)
	s.NoError(err)
}

func (s *WalletRepositoryTestSuite) TestFindByID_Found() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(w))

	found, err := s.repo.FindByID(w.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("eth", found.Chain)
}

func (s *WalletRepositoryTestSuite) TestFindByID_NotFound() {
	found, err := s.repo.FindByID(uuid.New())
	s.NoError(err)
	s.Nil(found)
}

func (s *WalletRepositoryTestSuite) TestFindAll() {
	s.Require().NoError(s.repo.Create(s.makeWallet("eth")))
	s.Require().NoError(s.repo.Create(s.makeWallet("btc")))

	wallets, err := s.repo.FindAll()
	s.NoError(err)
	s.Len(wallets, 2)
}

func (s *WalletRepositoryTestSuite) TestCountByChain() {
	s.Require().NoError(s.repo.Create(s.makeWallet("eth")))
	s.Require().NoError(s.repo.Create(s.makeWallet("eth")))
	s.Require().NoError(s.repo.Create(s.makeWallet("btc")))

	count, err := s.repo.CountByChain("eth")
	s.NoError(err)
	s.Equal(int64(2), count)

	count, err = s.repo.CountByChain("btc")
	s.NoError(err)
	s.Equal(int64(1), count)

	count, err = s.repo.CountByChain("sol")
	s.NoError(err)
	s.Equal(int64(0), count)
}

func (s *WalletRepositoryTestSuite) TestUpdateField() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(w))

	err := s.repo.UpdateField(w.ID, "status", "frozen")
	s.NoError(err)

	found, err := s.repo.FindByID(w.ID)
	s.NoError(err)
	s.Equal("frozen", found.Status)
}

func (s *WalletRepositoryTestSuite) TestUpdateFields() {
	w := s.makeWallet("eth")
	code := "123456"
	w.ActivationCode = &code
	w.Status = "pending"
	s.Require().NoError(s.repo.Create(w))

	err := s.repo.UpdateFields(w.ID, map[string]interface{}{
		"status":          "active",
		"activation_code": nil,
	})
	s.NoError(err)

	found, err := s.repo.FindByID(w.ID)
	s.NoError(err)
	s.Equal("active", found.Status)
}
