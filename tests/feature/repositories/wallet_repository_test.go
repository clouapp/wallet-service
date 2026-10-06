package repositories_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type WalletRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WalletRepository
}

func TestWallet_Repository_Suite(t *testing.T) {
	suite.Run(t, new(WalletRepositoryTestSuite))
}

func (s *WalletRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWalletRepository(nil)
}

func (s *WalletRepositoryTestSuite) makeWallet(chainID string) *models.Wallet {
	return &models.Wallet{
		ID: uuid.New(), Chain: chainID, Label: chainID + " wallet",
		MPCCustomerShare: "deadbeef", MPCShareIV: "cafebabe", MPCShareSalt: "feedface",
		MPCSecretARN: "arn:test", MPCPublicKey: "02abc", MPCCurve: "secp256k1",
	}
}

func (s *WalletRepositoryTestSuite) TestWalletRepository_Create_Success() {
	w := s.makeWallet("eth")
	err := s.repo.Create(context.Background(), w)
	s.NoError(err)
}

func (s *WalletRepositoryTestSuite) TestFind_ByID_Found() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), w))

	found, err := s.repo.FindByID(context.Background(), w.ID)
	s.NoError(err)
	s.NotNil(found)
	s.Equal("eth", found.Chain)
}

func (s *WalletRepositoryTestSuite) TestFind_ByID_NotFound() {
	found, err := s.repo.FindByID(context.Background(), uuid.New())
	s.ErrorIs(err, models.ErrRepositoryNotFound)
	s.Nil(found)
}

func (s *WalletRepositoryTestSuite) TestWalletRepository_Find_All() {
	s.Require().NoError(s.repo.Create(context.Background(), s.makeWallet("eth")))
	s.Require().NoError(s.repo.Create(context.Background(), s.makeWallet("btc")))

	wallets, err := s.repo.FindAll(context.Background())
	s.NoError(err)
	s.Len(wallets, 2)
}

func (s *WalletRepositoryTestSuite) TestWalletRepository_Set_Status() {
	w := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), w))

	err := s.repo.SetStatus(context.Background(), w.ID, "frozen")
	s.NoError(err)

	found, err := s.repo.FindByID(context.Background(), w.ID)
	s.NoError(err)
	s.Equal("frozen", found.Status)
}

func (s *WalletRepositoryTestSuite) TestActivate_Clears_TheCode() {
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

func (s *WalletRepositoryTestSuite) TestPaginate_By_AccountAndMemberKeepsOnlyActiveMemberships() {
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

func (s *WalletRepositoryTestSuite) TestWithin_Commits_AGasCheckAndItsWebhook() {
	wallet := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), wallet))
	eventID := uuid.New()
	checkedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	err := s.repo.Within(context.Background(), func(ctx context.Context) error {
		if checkErr := s.repo.RecordGasCheck(ctx, wallet.ID, checkedAt, models.GasStatusSeeded, true); checkErr != nil {
			return checkErr
		}
		return repositories.NewWebhookEventRepository(nil).Create(ctx, gasStatusEvent(eventID))
	})
	s.NoError(err)

	found, findErr := s.repo.FindByID(context.Background(), wallet.ID)
	s.NoError(findErr)
	s.Equal(models.GasStatusSeeded, found.GasStatus)
	s.NotNil(found.GasLastCheckedAt)
	s.True(found.GasLastCheckedAt.Equal(checkedAt))
	s.Equal(int64(1), s.countWebhookEvents(eventID))
}

func (s *WalletRepositoryTestSuite) TestWithin_Rolls_BackAGasCheckAndItsWebhook() {
	wallet := s.makeWallet("eth")
	s.Require().NoError(s.repo.Create(context.Background(), wallet))
	eventID := uuid.New()
	checkedAt := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)

	err := s.repo.Within(context.Background(), func(ctx context.Context) error {
		if checkErr := s.repo.RecordGasCheck(ctx, wallet.ID, checkedAt, models.GasStatusSeeded, true); checkErr != nil {
			return checkErr
		}
		if createErr := repositories.NewWebhookEventRepository(nil).Create(ctx, gasStatusEvent(eventID)); createErr != nil {
			return createErr
		}
		return errors.New("fail the gas-status update")
	})
	s.Error(err)

	found, findErr := s.repo.FindByID(context.Background(), wallet.ID)
	s.NoError(findErr)
	s.Equal(models.GasStatusUnseeded, found.GasStatus)
	s.Nil(found.GasLastCheckedAt)
	s.Equal(int64(0), s.countWebhookEvents(eventID))
}

func gasStatusEvent(eventID uuid.UUID) *models.WebhookEvent {
	return &models.WebhookEvent{
		ID:             eventID,
		EventType:      "wallet.gas_status.changed",
		Payload:        "{}",
		DeliveryURL:    "https://example.test/hooks",
		DeliveryStatus: "pending",
	}
}

func (s *WalletRepositoryTestSuite) countWebhookEvents(eventID uuid.UUID) int64 {
	s.T().Helper()
	count, err := facades.Orm().Query().Model(&models.WebhookEvent{}).Where("id = ?", eventID).Count()
	s.Require().NoError(err)
	return count
}

func walletIDs(wallets []models.Wallet) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(wallets))
	for _, wallet := range wallets {
		ids = append(ids, wallet.ID)
	}
	return ids
}
