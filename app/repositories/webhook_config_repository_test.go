package repositories_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/mocks"
)

type WebhookConfigRepositoryTestSuite struct {
	suite.Suite
	repo repositories.WebhookConfigRepository
}

func TestWebhookConfigRepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookConfigRepositoryTestSuite))
}

func (s *WebhookConfigRepositoryTestSuite) SetupTest() {
	mocks.TestDB(s.T())
	s.repo = repositories.NewWebhookConfigRepository()
}

func (s *WebhookConfigRepositoryTestSuite) insertWallet() uuid.UUID {
	w := mocks.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WebhookConfigRepositoryTestSuite) TestCreate_Success() {
	walletID := s.insertWallet()
	cfg := &models.WebhookConfig{
		ID: uuid.New(), URL: "https://example.com/hook", Secret: "sec",
		Events: `{"deposit.confirmed"}`, IsActive: true, WalletID: &walletID, Type: "wallet",
	}
	err := s.repo.Create(cfg)
	s.NoError(err)
}

func (s *WebhookConfigRepositoryTestSuite) TestFindByWalletID() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}))
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}))

	cfgs, err := s.repo.FindByWalletID(walletID)
	s.NoError(err)
	s.Len(cfgs, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestFindByIDAndWallet_Found() {
	walletID := s.insertWallet()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://f.com", Secret: "s", Events: `{"x"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}
	s.Require().NoError(s.repo.Create(cfg))

	found, err := s.repo.FindByIDAndWallet(cfg.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
}

func (s *WebhookConfigRepositoryTestSuite) TestFindByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	otherWallet := s.insertWallet()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://f.com", Secret: "s", Events: `{"x"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}
	s.Require().NoError(s.repo.Create(cfg))

	found, err := s.repo.FindByIDAndWallet(cfg.ID, otherWallet)
	s.NoError(err)
	s.Nil(found)
}

func (s *WebhookConfigRepositoryTestSuite) TestFindActive() {
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true}))
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: true}))

	inactiveCfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://c.com", Secret: "s", Events: `{"c"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(inactiveCfg))
	facades.Orm().Query().Model(inactiveCfg).Where("id = ?", inactiveCfg.ID).Update("is_active", false)

	active, err := s.repo.FindActive()
	s.NoError(err)
	s.Len(active, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestFindAll() {
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true}))
	s.Require().NoError(s.repo.Create(&models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: false}))

	all, err := s.repo.FindAll()
	s.NoError(err)
	s.Len(all, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestDelete() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://d.com", Secret: "s", Events: `{"d"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(cfg))

	err := s.repo.Delete(cfg)
	s.NoError(err)

	all, err := s.repo.FindAll()
	s.NoError(err)
	s.Len(all, 0)
}

func (s *WebhookConfigRepositoryTestSuite) TestDeleteByID() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://e.com", Secret: "s", Events: `{"e"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(cfg))

	err := s.repo.DeleteByID(cfg.ID)
	s.NoError(err)

	all, err := s.repo.FindAll()
	s.NoError(err)
	s.Len(all, 0)
}
