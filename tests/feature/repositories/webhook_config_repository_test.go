package repositories_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/settings"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type WebhookConfigRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WebhookConfigRepository
}

func TestWebhook_Config_RepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookConfigRepositoryTestSuite))
}

func (s *WebhookConfigRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWebhookConfigRepository(repositories.WebhookConfigRepositoryDeps{
		Cipher: facades.Crypt(),
	})
}

func (s *WebhookConfigRepositoryTestSuite) insertWallet() uuid.UUID {
	w := fixtures.InsertWallet(s.T(), "eth")
	return w.ID
}

func (s *WebhookConfigRepositoryTestSuite) TestWebhookConfigRepository_Create_Success() {
	walletID := s.insertWallet()
	cfg := &models.WebhookConfig{
		ID: uuid.New(), URL: "https://example.com/hook", Secret: "sec",
		Events: `{"deposit.confirmed"}`, IsActive: true, WalletID: &walletID, Type: "wallet",
	}
	err := s.repo.Create(context.Background(), cfg)
	s.NoError(err)
}

func (s *WebhookConfigRepositoryTestSuite) storedSecret(id uuid.UUID) string {
	var secret string
	s.Require().NoError(facades.Orm().Query().Raw(`SELECT secret FROM webhook_configs WHERE id = ?`, id).Scan(&secret))
	return secret
}

func (s *WebhookConfigRepositoryTestSuite) TestSecret_Is_SealedAtRestAndOpenedOnRead() {
	walletID := s.insertWallet()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://sealed.test", Secret: "whsec_plain", Events: `{"a"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))

	s.Equal("whsec_plain", cfg.Secret, "the caller keeps the plaintext")
	stored := s.storedSecret(cfg.ID)
	s.NotEqual("whsec_plain", stored)
	s.True(settings.IsSealed(stored), "webhook secret must be stored with the seal prefix")

	byID, err := s.repo.FindByID(context.Background(), cfg.ID)
	s.Require().NoError(err)
	s.Equal("whsec_plain", byID.Secret)
	byWallet, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.Require().NoError(err)
	s.Require().Len(byWallet, 1)
	s.Equal("whsec_plain", byWallet[0].Secret)
}

func (s *WebhookConfigRepositoryTestSuite) TestUpdate_Fields_SealsASecret() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://rotate.test", Secret: "old", Events: `{"a"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))
	fields := map[string]any{"secret": "rotated"}

	s.Require().NoError(s.repo.UpdateFields(context.Background(), cfg.ID, fields))

	s.Equal("rotated", fields["secret"], "the caller's map is not rewritten")
	s.True(settings.IsSealed(s.storedSecret(cfg.ID)), "rotated webhook secret must be stored with the seal prefix")
	loaded, err := s.repo.FindByID(context.Background(), cfg.ID)
	s.Require().NoError(err)
	s.Equal("rotated", loaded.Secret)
}

func (s *WebhookConfigRepositoryTestSuite) TestFind_Refuses_ASecretStoredInPlaintext() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://plain.test", Secret: "s", Events: `{"a"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_configs SET secret = 'plaintext' WHERE id = ?`, cfg.ID)
	s.Require().NoError(err)

	_, err = s.repo.FindByID(context.Background(), cfg.ID)
	s.ErrorIs(err, settings.ErrNotSealed)
	_, err = s.repo.FindActive(context.Background())
	s.ErrorIs(err, settings.ErrNotSealed)
}

func (s *WebhookConfigRepositoryTestSuite) TestFind_By_WalletID() {
	walletID := s.insertWallet()
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}))

	cfgs, err := s.repo.FindByWalletID(context.Background(), walletID)
	s.NoError(err)
	s.Len(cfgs, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestFind_ByIDAndWallet_Found() {
	walletID := s.insertWallet()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://f.com", Secret: "s", Events: `{"x"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))

	found, err := s.repo.FindByIDAndWallet(context.Background(), cfg.ID, walletID)
	s.NoError(err)
	s.NotNil(found)
}

func (s *WebhookConfigRepositoryTestSuite) TestFind_ByIDAndWallet_WrongWallet() {
	walletID := s.insertWallet()
	otherWallet := s.insertWallet()
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://f.com", Secret: "s", Events: `{"x"}`, IsActive: true, WalletID: &walletID, Type: "wallet"}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))

	found, err := s.repo.FindByIDAndWallet(context.Background(), cfg.ID, otherWallet)
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WebhookConfigRepositoryTestSuite) TestWebhookConfigRepository_Find_Active() {
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: true}))

	inactiveCfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://c.com", Secret: "s", Events: `{"c"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(context.Background(), inactiveCfg))
	facades.Orm().Query().Model(inactiveCfg).Where("id = ?", inactiveCfg.ID).Update("is_active", false)

	active, err := s.repo.FindActive(context.Background())
	s.NoError(err)
	s.Len(active, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestWebhookConfigRepository_Find_All() {
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://a.com", Secret: "s", Events: `{"a"}`, IsActive: true}))
	s.Require().NoError(s.repo.Create(context.Background(), &models.WebhookConfig{ID: uuid.New(), URL: "https://b.com", Secret: "s", Events: `{"b"}`, IsActive: false}))

	all, err := s.repo.FindAll(context.Background())
	s.NoError(err)
	s.Len(all, 2)
}

func (s *WebhookConfigRepositoryTestSuite) TestWebhookConfigRepository_Delete_Succeeds() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://d.com", Secret: "s", Events: `{"d"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))

	err := s.repo.Delete(context.Background(), cfg)
	s.NoError(err)

	all, err := s.repo.FindAll(context.Background())
	s.NoError(err)
	s.Len(all, 0)
}

func (s *WebhookConfigRepositoryTestSuite) TestDelete_By_ID() {
	cfg := &models.WebhookConfig{ID: uuid.New(), URL: "https://e.com", Secret: "s", Events: `{"e"}`, IsActive: true}
	s.Require().NoError(s.repo.Create(context.Background(), cfg))

	err := s.repo.DeleteByID(context.Background(), cfg.ID)
	s.NoError(err)

	all, err := s.repo.FindAll(context.Background())
	s.NoError(err)
	s.Len(all, 0)
}
