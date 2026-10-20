package repositories_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/suite"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

type WebhookEventRepositoryTestSuite struct {
	suite.Suite
	repo *repositories.WebhookEventRepository
}

func TestWebhook_Event_RepositorySuite(t *testing.T) {
	suite.Run(t, new(WebhookEventRepositoryTestSuite))
}

func (s *WebhookEventRepositoryTestSuite) SetupTest() {
	fixtures.TestDB(s.T())
	s.repo = repositories.NewWebhookEventRepository(nil)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Create_Success() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	err := s.repo.Create(context.Background(), event)
	s.NoError(err)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Mark_Delivered() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))

	err := s.repo.MarkDelivered(context.Background(), event.ID.String())
	s.Require().NoError(err)

	var check models.WebhookEvent
	facades.Orm().Query().Where("id = ?", event.ID).First(&check)
	s.Equal("delivered", check.DeliveryStatus)
	s.NotNil(check.DeliveredAt)
	s.Equal(1, check.Attempts)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_Increment_Attempt() {
	event := &models.WebhookEvent{
		ID:             uuid.New(),
		EventType:      "deposit.confirmed",
		Payload:        `{"test": true}`,
		DeliveryURL:    "https://example.com/hook",
		DeliveryStatus: "pending",
		Attempts:       0,
		MaxAttempts:    10,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))

	_ = s.repo.IncrementAttempt(context.Background(), event.ID.String(), "HTTP 500")
	_ = s.repo.IncrementAttempt(context.Background(), event.ID.String(), "HTTP 502")

	var check models.WebhookEvent
	facades.Orm().Query().Where("id = ?", event.ID).First(&check)
	s.Equal(2, check.Attempts)
	s.Equal("HTTP 502", check.LastError)
}

// insertOutboxRow stores one event for configID (nil leaves the column empty)
// and moves its timestamps back by createdAgo and updatedAgo.
func (s *WebhookEventRepositoryTestSuite) insertOutboxRow(configID *uuid.UUID, status string, attempts int, createdAgo, updatedAgo time.Duration) models.WebhookEvent {
	event := &models.WebhookEvent{
		ID:              uuid.New(),
		WebhookConfigID: configID,
		EventType:       "deposit.confirmed",
		Payload:         `{"test": true}`,
		DeliveryURL:     "https://example.com/hook",
		DeliveryStatus:  status,
		Attempts:        attempts,
		MaxAttempts:     3,
	}
	s.Require().NoError(s.repo.Create(context.Background(), event))
	_, err := facades.Orm().Query().Exec(
		`UPDATE webhook_events SET created_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?), updated_at = (NOW() AT TIME ZONE 'UTC') - make_interval(secs => ?) WHERE id = ?`,
		createdAgo.Seconds(), updatedAgo.Seconds(), event.ID,
	)
	s.Require().NoError(err)
	return *event
}

func (s *WebhookEventRepositoryTestSuite) storedOutboxRow(id uuid.UUID) models.WebhookEvent {
	var row models.WebhookEvent
	s.Require().NoError(facades.Orm().Query().Where("id = ?", id).First(&row))
	return row
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_TakesOnlyLostPendingRows() {
	ctx := context.Background()
	active := fixtures.InsertWebhookConfig(s.T(), "https://active.test/hook", "secret", []string{"deposit.confirmed"})
	inactive := fixtures.InsertWebhookConfig(s.T(), "https://inactive.test/hook", "secret", []string{"deposit.confirmed"})
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_configs SET is_active = false WHERE id = ?`, inactive.ID)
	s.Require().NoError(err)
	stale := 5 * time.Minute

	lost := s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 0, 10*time.Minute, 10*time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 0, time.Minute, time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 1, time.Hour, time.Minute)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryDelivered, 1, time.Hour, time.Hour)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryFailed, 1, time.Hour, time.Hour)
	s.insertOutboxRow(&active.ID, models.WebhookDeliveryPending, 3, time.Hour, time.Hour)
	s.insertOutboxRow(&inactive.ID, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)
	s.insertOutboxRow(nil, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)

	claimed, err := s.repo.ClaimStaleForRedispatch(ctx, stale, 10)
	s.Require().NoError(err)
	s.Require().Len(claimed, 1)
	s.Equal(lost.ID, claimed[0].ID)
	s.Equal(1, claimed[0].Attempts, "a re-dispatch counts as one attempt")
	s.Equal(lost.Payload, claimed[0].Payload)
	s.Require().NotNil(claimed[0].WebhookConfigID)
	s.Equal(active.ID, *claimed[0].WebhookConfigID)

	stored := s.storedOutboxRow(lost.ID)
	s.Equal(1, stored.Attempts)
	s.Equal(models.WebhookDeliveryPending, stored.DeliveryStatus)
	s.Require().NotNil(stored.UpdatedAt)
	s.WithinDuration(time.Now().UTC(), stored.UpdatedAt.StdTime(), time.Minute, "the claim touches the row")

	again, err := s.repo.ClaimStaleForRedispatch(ctx, stale, 10)
	s.Require().NoError(err)
	s.Empty(again, "a claimed row waits another stale period")
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_TakesTheOldestUpToTheLimit() {
	ctx := context.Background()
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://active.test/hook", "secret", []string{"deposit.confirmed"})
	oldest := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, 3*time.Hour, 3*time.Hour)
	middle := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, 2*time.Hour, 2*time.Hour)
	newest := s.insertOutboxRow(&cfg.ID, models.WebhookDeliveryPending, 0, time.Hour, time.Hour)

	claimed, err := s.repo.ClaimStaleForRedispatch(ctx, 5*time.Minute, 2)
	s.Require().NoError(err)
	ids := map[uuid.UUID]bool{}
	for _, row := range claimed {
		ids[row.ID] = true
	}
	s.Len(claimed, 2)
	s.True(ids[oldest.ID] && ids[middle.ID], "the oldest rows go first")
	s.False(ids[newest.ID])
	s.Equal(0, s.storedOutboxRow(newest.ID).Attempts)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_ClaimStaleForRedispatch_RefusesABadWindow() {
	_, err := s.repo.ClaimStaleForRedispatch(context.Background(), 0, 10)
	s.Error(err)
	_, err = s.repo.ClaimStaleForRedispatch(context.Background(), time.Minute, 0)
	s.Error(err)
}

// outboxRowOf stores one event of type eventType for configID, created createdAgo
// and last touched updatedAgo before now.
func (s *WebhookEventRepositoryTestSuite) outboxRowOf(configID uuid.UUID, eventType, status string, createdAgo, updatedAgo time.Duration) models.WebhookEvent {
	row := s.insertOutboxRow(&configID, status, 2, createdAgo, updatedAgo)
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_events SET event_type = ? WHERE id = ?`, eventType, row.ID)
	s.Require().NoError(err)
	if status == models.WebhookDeliveryDelivered {
		_, err = facades.Orm().Query().Exec(`UPDATE webhook_events SET delivered_at = NOW() WHERE id = ?`, row.ID)
		s.Require().NoError(err)
	}
	row.EventType = eventType
	return row
}

// accountWallet inserts an account and one wallet of it.
func (s *WebhookEventRepositoryTestSuite) accountWallet() (uuid.UUID, models.Wallet) {
	account := fixtures.InsertAccount(s.T(), "redelivery-"+uuid.NewString()[:8])
	return account.ID, fixtures.InsertWalletWithAccount(s.T(), "eth", &account.ID)
}

// payloadNames sets the event's payload to name walletID in data.wallet_id, the way
// an event without a transaction (wallet.gas_status.changed, a withdrawal event sent
// before its transaction existed) carries its wallet.
func (s *WebhookEventRepositoryTestSuite) payloadNames(eventID uuid.UUID, walletID string) {
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_events SET payload = ? WHERE id = ?`,
		`{"type":"wallet.gas_status.changed","data":{"wallet_id":"`+walletID+`","chain":"eth"}}`, eventID)
	s.Require().NoError(err)
}

// transactionOf points the event at a new transaction of wallet.
func (s *WebhookEventRepositoryTestSuite) transactionOf(eventID uuid.UUID, wallet models.Wallet) {
	tx := fixtures.InsertTransaction(s.T(), wallet.ID, wallet.DepositAddressID, "eth", models.TxTypeDeposit, "confirmed", "ETH", "1", 1)
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_events SET transaction_id = ? WHERE id = ?`, tx.ID, eventID)
	s.Require().NoError(err)
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_FindForRedelivery_SelectsTheConfigsEventsOfTheWindowAndTypes() {
	ctx := context.Background()
	accountID, wallet := s.accountWallet()
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://owner.test/hook", "secret", []string{"deposit.confirmed"})
	other := fixtures.InsertWebhookConfig(s.T(), "https://other.test/hook", "secret", []string{"deposit.confirmed"})
	older := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, 90*time.Minute, 90*time.Minute)
	newer := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryFailed, time.Hour, time.Hour)
	otherType := s.outboxRowOf(cfg.ID, "withdrawal.confirmed", models.WebhookDeliveryDelivered, time.Hour, time.Hour)
	beforeWindow := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, 3*time.Hour, 3*time.Hour)
	afterWindow := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, time.Minute, time.Minute)
	ofOther := s.outboxRowOf(other.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, time.Hour, time.Hour)
	for _, row := range []models.WebhookEvent{older, newer, otherType, beforeWindow, afterWindow, ofOther} {
		s.transactionOf(row.ID, wallet)
	}

	now := time.Now().UTC()
	ids, err := s.repo.FindForRedelivery(ctx, cfg.ID, accountID, now.Add(-2*time.Hour), now.Add(-30*time.Minute), []string{"deposit.confirmed"}, 10)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{older.ID, newer.ID}, ids, "the config's deposit.confirmed of the window, oldest first")

	all, err := s.repo.FindForRedelivery(ctx, cfg.ID, accountID, now.Add(-2*time.Hour), now, nil, 10)
	s.Require().NoError(err)
	s.Len(all, 4, "no type filter selects every type of the window")

	limited, err := s.repo.FindForRedelivery(ctx, cfg.ID, accountID, now.Add(-2*time.Hour), now, nil, 1)
	s.Require().NoError(err)
	s.Equal([]uuid.UUID{older.ID}, limited)

	_, err = s.repo.FindForRedelivery(ctx, cfg.ID, uuid.Nil, now.Add(-2*time.Hour), now, nil, 10)
	s.Error(err, "a redelivery is always for one account")
}

// walletScopedRows stores, on one config, an event of each way an event names its
// wallet, for the account and for another one. It returns the two the account owns.
func (s *WebhookEventRepositoryTestSuite) walletScopedRows(configID uuid.UUID) (accountID uuid.UUID, owned []uuid.UUID, others []uuid.UUID) {
	accountID, wallet := s.accountWallet()
	_, otherWallet := s.accountWallet()
	unownedWallet := fixtures.InsertWallet(s.T(), "eth")
	row := func() uuid.UUID {
		return s.outboxRowOf(configID, "deposit.confirmed", models.WebhookDeliveryDelivered, time.Hour, time.Hour).ID
	}

	ownByTransaction := row()
	s.transactionOf(ownByTransaction, wallet)
	ownByPayload := row()
	s.payloadNames(ownByPayload, wallet.ID.String())

	otherByTransaction := row()
	s.transactionOf(otherByTransaction, otherWallet)
	otherByPayload := row()
	s.payloadNames(otherByPayload, otherWallet.ID.String())
	transactionWinsOverPayload := row()
	s.transactionOf(transactionWinsOverPayload, otherWallet)
	s.payloadNames(transactionWinsOverPayload, wallet.ID.String())
	walletWithoutAccount := row()
	s.payloadNames(walletWithoutAccount, unownedWallet.ID.String())
	noWallet := row()
	notAWallet := row()
	s.payloadNames(notAWallet, "not-a-wallet")

	return accountID, []uuid.UUID{ownByTransaction, ownByPayload},
		[]uuid.UUID{otherByTransaction, otherByPayload, transactionWinsOverPayload, walletWithoutAccount, noWallet, notAWallet}
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_FindForRedelivery_SelectsOnlyTheEventsOfTheAccountsWallets() {
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://legacy.test/hook", "secret", []string{"deposit.confirmed"})
	accountID, owned, _ := s.walletScopedRows(cfg.ID)

	now := time.Now().UTC()
	ids, err := s.repo.FindForRedelivery(context.Background(), cfg.ID, accountID, now.Add(-2*time.Hour), now, nil, 100)
	s.Require().NoError(err)
	s.ElementsMatch(owned, ids, "only the events of the account's own wallets, by their transaction or by their payload")
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_RequeueForRedelivery_LeavesTheEventsOfAnotherAccountsWallets() {
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://legacy.test/hook", "secret", []string{"deposit.confirmed"})
	accountID, owned, others := s.walletScopedRows(cfg.ID)

	now := time.Now().UTC()
	requeued, err := s.repo.RequeueForRedelivery(context.Background(), cfg.ID, accountID, append(append([]uuid.UUID{}, owned...), others...),
		now.Add(-7*24*time.Hour), now.Add(-time.Minute))
	s.Require().NoError(err)
	ids := make([]uuid.UUID, 0, len(requeued))
	for _, row := range requeued {
		ids = append(ids, row.ID)
	}
	s.ElementsMatch(owned, ids)
	for _, id := range others {
		s.Equal(models.WebhookDeliveryDelivered, s.storedOutboxRow(id).DeliveryStatus, "event %s is not about a wallet of the account", id)
	}

	_, err = s.repo.RequeueForRedelivery(context.Background(), cfg.ID, uuid.Nil, owned, now.Add(-7*24*time.Hour), now.Add(-time.Minute))
	s.Error(err, "a redelivery is always for one account")
}

func (s *WebhookEventRepositoryTestSuite) TestWebhookEventRepository_RequeueForRedelivery_ResetsOnlyTheConfigsSettledEvents() {
	ctx := context.Background()
	accountID, wallet := s.accountWallet()
	cfg := fixtures.InsertWebhookConfig(s.T(), "https://owner.test/hook", "secret", []string{"deposit.confirmed"})
	other := fixtures.InsertWebhookConfig(s.T(), "https://other.test/hook", "secret", []string{"deposit.confirmed"})
	delivered := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, time.Hour, time.Hour)
	failed := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryFailed, time.Hour, time.Hour)
	stuck := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryPending, time.Hour, time.Hour)
	inFlight := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryPending, time.Hour, 10*time.Second)
	tooOld := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, 9*24*time.Hour, 9*24*time.Hour)
	ofOther := s.outboxRowOf(other.ID, "deposit.confirmed", models.WebhookDeliveryDelivered, time.Hour, time.Hour)
	neverTouched := s.outboxRowOf(cfg.ID, "deposit.confirmed", models.WebhookDeliveryPending, time.Hour, time.Hour)
	for _, row := range []models.WebhookEvent{delivered, failed, stuck, inFlight, tooOld, ofOther, neverTouched} {
		s.payloadNames(row.ID, wallet.ID.String())
	}
	_, err := facades.Orm().Query().Exec(`UPDATE webhook_events SET updated_at = NULL WHERE id = ?`, neverTouched.ID)
	s.Require().NoError(err)

	now := time.Now().UTC()
	requeued, err := s.repo.RequeueForRedelivery(ctx, cfg.ID, accountID,
		[]uuid.UUID{delivered.ID, failed.ID, stuck.ID, inFlight.ID, tooOld.ID, ofOther.ID, neverTouched.ID, uuid.New()},
		now.Add(-7*24*time.Hour), now.Add(-time.Minute))
	s.Require().NoError(err)
	s.ElementsMatch([]uuid.UUID{delivered.ID, failed.ID, stuck.ID, neverTouched.ID}, func() []uuid.UUID {
		ids := make([]uuid.UUID, 0, len(requeued))
		for _, row := range requeued {
			ids = append(ids, row.ID)
		}
		return ids
	}())
	for _, row := range requeued {
		s.Equal(models.WebhookDeliveryPending, row.DeliveryStatus)
		s.Zero(row.Attempts)
		s.Nil(row.DeliveredAt)
		s.Equal(s.storedOutboxRow(delivered.ID).Payload, row.Payload)
	}
	for _, id := range []uuid.UUID{delivered.ID, failed.ID, stuck.ID} {
		stored := s.storedOutboxRow(id)
		s.Equal(models.WebhookDeliveryPending, stored.DeliveryStatus)
		s.Zero(stored.Attempts)
		s.Nil(stored.DeliveredAt)
	}
	s.Equal(2, s.storedOutboxRow(inFlight.ID).Attempts, "an event queued moments ago is left alone")
	s.Equal(models.WebhookDeliveryDelivered, s.storedOutboxRow(tooOld.ID).DeliveryStatus, "an event past the window is left alone")
	s.Equal(models.WebhookDeliveryDelivered, s.storedOutboxRow(ofOther.ID).DeliveryStatus, "another config's event is left alone")

	none, err := s.repo.RequeueForRedelivery(ctx, cfg.ID, accountID, nil, now.Add(-7*24*time.Hour), now.Add(-time.Minute))
	s.Require().NoError(err)
	s.Empty(none)
}
