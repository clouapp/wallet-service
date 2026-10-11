package repositories_test

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

const depositConfirmedType = "deposit.confirmed"

// confirmedDeposit inserts a deposit of walletID on chainID with status, confirmed
// confirmedAgo before now.
func (s *TransactionRepositoryTestSuite) confirmedDeposit(walletID uuid.UUID, chainID, status string, confirmedAgo time.Duration) models.Transaction {
	tx := s.makeTx(walletID, models.TxTypeDeposit, status)
	tx.Chain = chainID
	confirmedAt := time.Now().UTC().Add(-confirmedAgo)
	tx.ConfirmedAt = &confirmedAt
	s.Require().NoError(s.repo.Create(context.Background(), tx))
	return *tx
}

// webhookConfigCreated inserts a config of accountID subscribed to events and moves
// its created_at back by createdAgo.
func (s *TransactionRepositoryTestSuite) webhookConfigCreated(events []string, accountID, walletID *uuid.UUID, createdAgo time.Duration) models.WebhookConfig {
	cfg := fixtures.InsertScopedWebhookConfig(s.T(), "https://"+uuid.NewString()[:8]+".test/hook", "secret", events, accountID, walletID)
	_, err := facades.Orm().Query().Exec(
		`UPDATE webhook_configs SET created_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?) WHERE id = ?`,
		createdAgo.Seconds(), cfg.ID,
	)
	s.Require().NoError(err)
	return cfg
}

func (s *TransactionRepositoryTestSuite) enqueuedFor(cfg models.WebhookConfig, eventType string, subject uuid.UUID) {
	configID := cfg.ID
	subjectID := subject.String()
	s.Require().NoError(facades.Orm().Query().Create(&models.WebhookEvent{
		ID:              uuid.New(),
		TransactionID:   &subject,
		WebhookConfigID: &configID,
		SubjectID:       &subjectID,
		EventType:       eventType,
		Payload:         "{}",
		DeliveryURL:     cfg.URL,
		DeliveryStatus:  models.WebhookDeliveryDelivered,
		MaxAttempts:     10,
	}))
}

func transactionIDs(rows []models.Transaction) []uuid.UUID {
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return ids
}

func (s *TransactionRepositoryTestSuite) TestFind_ConfirmedDepositsMissingEvent_ReturnsOnlyDepositsAConfigNeverGot() {
	ctx := context.Background()
	owner := fixtures.InsertAccount(s.T(), "deposit owner")
	other := fixtures.InsertAccount(s.T(), "other tenant")
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &owner.ID)
	otherWallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &other.ID)
	cfg := s.webhookConfigCreated([]string{depositConfirmedType}, &owner.ID, nil, 2*time.Hour)
	s.webhookConfigCreated([]string{"withdrawal.confirmed"}, &other.ID, nil, 2*time.Hour)

	lost := s.confirmedDeposit(wallet.ID, "eth", "confirmed", time.Hour)
	lostOnSol := s.confirmedDeposit(wallet.ID, "sol", "confirmed", time.Hour)
	delivered := s.confirmedDeposit(wallet.ID, "eth", "confirmed", time.Hour)
	s.enqueuedFor(cfg, depositConfirmedType, delivered.ID)
	s.confirmedDeposit(wallet.ID, "eth", "confirmed", 72*time.Hour)
	s.confirmedDeposit(wallet.ID, "eth", "confirming", time.Hour)
	s.confirmedDeposit(wallet.ID, "eth", "confirmed", 3*time.Hour)
	s.confirmedDeposit(otherWallet.ID, "eth", "confirmed", time.Hour)
	withdrawal := s.makeTx(wallet.ID, models.TxTypeWithdrawal, "confirmed")
	confirmedAt := time.Now().UTC().Add(-time.Hour)
	withdrawal.ConfirmedAt = &confirmedAt
	s.Require().NoError(s.repo.Create(ctx, withdrawal))

	now := time.Now().UTC()
	from := now.Add(-48 * time.Hour)
	all, err := s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, from, now, nil, 10)
	s.Require().NoError(err)
	s.ElementsMatch([]uuid.UUID{lost.ID, lostOnSol.ID}, transactionIDs(all),
		"only confirmed deposits in the window that a config existing at confirmation never got")

	onEth, err := s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, from, now, []string{"eth"}, 10)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{lost.ID}, transactionIDs(onEth))
}

func (s *TransactionRepositoryTestSuite) TestFind_ConfirmedDepositsMissingEvent_FollowsTheConfigScope() {
	ctx := context.Background()
	owner := fixtures.InsertAccount(s.T(), "deposit owner")
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &owner.ID)
	otherWallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &owner.ID)
	walletCfg := s.webhookConfigCreated([]string{depositConfirmedType}, nil, &wallet.ID, 2*time.Hour)
	inactive := s.webhookConfigCreated([]string{depositConfirmedType}, &owner.ID, nil, 2*time.Hour)
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_configs SET is_active = false WHERE id = ?`, inactive.ID)
	s.Require().NoError(err)

	ofWallet := s.confirmedDeposit(wallet.ID, "eth", "confirmed", time.Hour)
	s.confirmedDeposit(otherWallet.ID, "eth", "confirmed", time.Hour)

	now := time.Now().UTC()
	rows, err := s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, now.Add(-48*time.Hour), now, nil, 10)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{ofWallet.ID}, transactionIDs(rows), "a wallet config sees its wallet only, an inactive config nothing")

	s.enqueuedFor(walletCfg, depositConfirmedType, ofWallet.ID)
	legacy := s.webhookConfigCreated([]string{depositConfirmedType}, nil, nil, 2*time.Hour)
	rows, err = s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, now.Add(-48*time.Hour), now, nil, 10)
	s.Require().NoError(err)
	s.Len(rows, 2, "a legacy config without an owner sees every wallet (%s)", legacy.ID)
}

func (s *TransactionRepositoryTestSuite) TestFind_ConfirmedDepositsMissingEvent_OldestConfirmationFirstUpToTheLimit() {
	ctx := context.Background()
	owner := fixtures.InsertAccount(s.T(), "deposit owner")
	wallet := fixtures.InsertWalletWithAccount(s.T(), "eth", &owner.ID)
	s.webhookConfigCreated([]string{depositConfirmedType}, &owner.ID, nil, 10*time.Hour)
	newest := s.confirmedDeposit(wallet.ID, "eth", "confirmed", time.Hour)
	oldest := s.confirmedDeposit(wallet.ID, "eth", "confirmed", 3*time.Hour)
	middle := s.confirmedDeposit(wallet.ID, "eth", "confirmed", 2*time.Hour)

	now := time.Now().UTC()
	rows, err := s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, now.Add(-48*time.Hour), now, nil, 2)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{oldest.ID, middle.ID}, transactionIDs(rows))
	s.NotContains(transactionIDs(rows), newest.ID)

	_, err = s.repo.FindConfirmedDepositsMissingEvent(ctx, depositConfirmedType, now.Add(-48*time.Hour), now, nil, 0)
	s.Error(err, "a limit is required")
	_, err = s.repo.FindConfirmedDepositsMissingEvent(ctx, "", now.Add(-48*time.Hour), now, nil, 2)
	s.Error(err, "an event type is required")
}
