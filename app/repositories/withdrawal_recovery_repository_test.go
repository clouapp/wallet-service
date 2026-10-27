package repositories_test

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories"
	"github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/tests/feature/support/fixtures"
)

func recoveryRows(t *testing.T) (*repositories.WithdrawalRecoveryRepository, *models.WithdrawalRecoveryAttempt, withdrawrecovery.Binding) {
	t.Helper()
	fixtures.TestDB(t)
	q := facades.Orm().Query()
	network := int64(421614)
	require.NoError(t, q.Create(&models.Chain{ID: "arbitrum", Name: "Arbitrum Sepolia", AdapterType: "evm", NativeSymbol: "ETH", NativeDecimals: 18, NetworkID: &network, RpcURL: "https://rpc.invalid", IsTestnet: true, RequiredConfirmations: 120, Status: "active", Finality: models.FinalityConfirmations}))
	account := fixtures.InsertAccount(t, "recovery test")
	_, err := q.Model(&models.Account{}).Where("id = ?", account.ID).Update("environment", "test")
	require.NoError(t, err)
	wallet := fixtures.InsertWalletWithAccount(t, "arbitrum", &account.ID)
	source := fixtures.InsertAddress(t, wallet.ID, "arbitrum", "0x1111111111111111111111111111111111111111", "2", 1)
	nonce := uint64(0)
	b := withdrawrecovery.Binding{OperationID: uuid.New(), OriginalTransactionID: uuid.New(), OriginalTxHash: "0x" + strings.Repeat("a", 64), AccountID: account.ID, WalletID: wallet.ID, SourceAddressID: source.ID, SourceAddress: source.Address, SourceDerivationIndex: 1, SourceExternalUserID: "2", Chain: "arbitrum", NetworkID: 421614, Nonce: &nonce, Asset: "USDC", TokenContract: models.USDCContractArbSepolia, TokenDecimals: 6, AmountRaw: "6100000", Recipient: "0x2222222222222222222222222222222222222222", RequestHash: strings.Repeat("a", 64), ProviderFingerprint: strings.Repeat("b", 64), RequiredConfirmations: 120, Finality: models.FinalityConfirmations}
	require.NoError(t, q.Create(&models.Transaction{ID: b.OriginalTransactionID, AddressID: &source.ID, WalletID: wallet.ID, ExternalUserID: "2", Chain: "arbitrum", TxType: "withdrawal", TxHash: b.OriginalTxHash, LogIndex: -1, FromAddress: source.Address, ToAddress: b.Recipient, Amount: "6100000", Asset: "USDC", TokenContract: b.TokenContract, RequiredConfs: 120, Status: "failed", ErrorMessage: "broadcast_refused", Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, Origin: models.TxOriginUserRequest, RawPayload: `{"broadcast_outcome":"refused","sender_nonce":0,"broadcast_head":123}`, Finality: models.FinalityConfirmations}))
	require.NoError(t, q.Create(&models.Withdrawal{ID: b.OperationID, WalletID: wallet.ID, TransactionID: &b.OriginalTransactionID, AccountID: &account.ID, Status: "failed", Amount: "6.1", DestinationAddress: b.Recipient, FeeEstimate: "0"}))
	hash, _ := withdrawrecovery.Hash(b)
	bindingJSON, _ := withdrawrecovery.Canonical(b)
	p := withdrawrecovery.Proposal{Binding: b}
	proposalJSON, _ := withdrawrecovery.Canonical(p)
	proposalHash, _ := withdrawrecovery.Hash(p)
	row := &models.WithdrawalRecoveryAttempt{ID: uuid.New(), OperationID: b.OperationID, OriginalTransactionID: b.OriginalTransactionID, AccountID: b.AccountID, WalletID: b.WalletID, SourceAddressID: b.SourceAddressID, NetworkID: 421614, Sender: strings.ToLower(b.SourceAddress), SenderNonce: "0", BindingHash: hash, BindingJSON: bindingJSON, ProposalHash: proposalHash, ProposalJSON: proposalJSON, ReviewJSON: `{"reviewed_by":"independent"}`, Operator: "operator", State: models.RecoveryClaimed, CallJSON: "{}", GasResultJSON: "{}", ObservationJSON: "{}", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	return repositories.NewWithdrawalRecoveryRepository(nil), row, b
}

func recoveryCandidate(row *models.WithdrawalRecoveryAttempt, b withdrawrecovery.Binding) *models.Transaction {
	op := b.OperationID.String()
	source := b.SourceAddressID
	old := b.OriginalTransactionID
	raw := fmt.Sprintf(`{"broadcast_outcome":"pending","sender_nonce":0,"signed_tx":"sealed-bytes","recovery_attempt_id":%q,"recovery_operation_id":%q,"recovery_binding_hash":%q,"recovery_predecessor_transaction_id":%q}`, row.ID, op, row.BindingHash, old)
	return &models.Transaction{ID: uuid.New(), AddressID: &source, WalletID: b.WalletID, ExternalUserID: b.SourceExternalUserID, Chain: b.Chain, TxType: "withdrawal", TxHash: "0x" + strings.Repeat("c", 64), LogIndex: -1, FromAddress: b.SourceAddress, ToAddress: b.Recipient, Amount: b.AmountRaw, Asset: b.Asset, TokenContract: b.TokenContract, RequiredConfs: 120, Status: "pending", IdempotencyKey: &op, Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, ParentTransactionID: &old, Origin: models.TxOriginUserRequest, RawPayload: raw, Finality: models.FinalityConfirmations}
}

func TestRecoveryClaimIsDurableUniqueAndKeepsNonceZero(t *testing.T) {
	r, row, _ := recoveryRows(t)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan bool, 8)
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			copy := *row
			copy.ID = uuid.New()
			won, err := r.Claim(ctx, &copy)
			results <- won
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	wins := 0
	for err := range errs {
		require.NoError(t, err)
	}
	for won := range results {
		if won {
			wins++
		}
	}
	require.Equal(t, 1, wins)
	saved, err := r.FindByOperation(ctx, row.OperationID)
	require.NoError(t, err)
	require.Equal(t, "0", saved.SenderNonce)
	require.Equal(t, row.BindingHash, saved.BindingHash)
	count, err := facades.Orm().Query().Model(&models.WithdrawalRecoveryEvent{}).Where("attempt_id = ?", saved.ID).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestRecoveryClaimRejectsChangedProviderBindingAndOuterTransaction(t *testing.T) {
	r, row, _ := recoveryRows(t)
	ctx := context.Background()
	tx, err := facades.Orm().Query().BeginTransaction()
	require.NoError(t, err)
	outer := repositories.NewWithdrawalRecoveryRepository(tx)
	won, err := outer.Claim(ctx, row)
	require.ErrorContains(t, err, "outer transaction")
	require.False(t, won)
	require.NoError(t, tx.Rollback())
	_, err = facades.Orm().Query().Model(&models.Withdrawal{}).Where("id = ?", row.OperationID).Update("amount", "6.2")
	require.NoError(t, err)
	won, err = r.Claim(ctx, row)
	require.Error(t, err)
	require.False(t, won)
	count, err := facades.Orm().Query().Model(&models.WithdrawalRecoveryAttempt{}).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRecoveryDatabaseProtectsBindingEventsAndOrdinaryRetry(t *testing.T) {
	r, row, _ := recoveryRows(t)
	won, err := r.Claim(context.Background(), row)
	require.NoError(t, err)
	require.True(t, won)
	q := facades.Orm().Query()
	_, err = q.Model(&models.WithdrawalRecoveryAttempt{}).Where("id = ?", row.ID).Update("sender_nonce", "1")
	require.ErrorContains(t, err, "immutable")
	_, err = q.Exec("DELETE FROM withdrawal_recovery_attempts WHERE id = ?", row.ID)
	require.ErrorContains(t, err, "cannot be deleted")
	_, err = q.Exec("UPDATE withdrawal_recovery_events SET to_state = 'broadcast' WHERE attempt_id = ?", row.ID)
	require.ErrorContains(t, err, "append-only")
	err = repositories.NewWithdrawalRepository(nil).RetryBroadcast(context.Background(), row.OperationID, "6.1", "0x2222222222222222222222222222222222222222", nil, "0", "")
	require.ErrorContains(t, err, "ordinary withdrawal retry")
	saved, err := repositories.NewWithdrawalRepository(nil).FindByID(context.Background(), row.OperationID)
	require.NoError(t, err)
	require.Equal(t, "failed", saved.Status)
}

func TestRecoveryCandidateCASPreservesOriginalAndStopsStaleFailure(t *testing.T) {
	r, row, b := recoveryRows(t)
	ctx := context.Background()
	won, err := r.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	call := `{"nonce":0,"to":"` + b.TokenContract + `","gas_limit":60000,"gas_price":"2","value":"0","data":"a9059cbb"}`
	won, err = r.Transition(ctx, row.ID, models.RecoveryClaimed, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &call})
	require.NoError(t, err)
	require.True(t, won)
	row.State = models.RecoverySigning
	row.CallJSON = call
	candidate := recoveryCandidate(row, b)
	won, err = r.RecordCandidate(ctx, row, candidate, "sealed-bytes")
	require.NoError(t, err)
	require.True(t, won)
	saved, err := r.FindByOperation(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, *saved.CandidateTransactionID)
	require.Equal(t, "sealed-bytes", saved.SealedSignedTx)
	original, err := repositories.NewTransactionRepository(nil).FindByID(ctx, b.OriginalTransactionID)
	require.NoError(t, err)
	require.Equal(t, "failed", original.Status)
	require.Equal(t, b.OriginalTxHash, original.TxHash)
	require.Nil(t, original.IdempotencyKey)
	withdrawal, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, candidate.ID, *withdrawal.TransactionID)
	require.Equal(t, "broadcasting", withdrawal.Status)
	require.ErrorContains(t, repositories.NewWithdrawalRepository(nil).MarkFailed(ctx, b.OperationID, "old callback"), "stale predecessor")
	another := recoveryCandidate(row, b)
	won, err = r.RecordCandidate(ctx, row, another, "sealed-bytes")
	require.NoError(t, err)
	require.False(t, won)
	count, err := facades.Orm().Query().Model(&models.Transaction{}).Count()
	require.NoError(t, err)
	require.EqualValues(t, 2, count)
	// CAS of the send state and its event happens on the committed candidate.
	won, err = r.Transition(ctx, row.ID, models.RecoverySigned, models.RecoverySendClaimed, models.WithdrawalRecoveryUpdate{})
	require.NoError(t, err)
	require.True(t, won)
	won, err = r.Transition(ctx, row.ID, models.RecoverySigned, models.RecoverySendClaimed, models.WithdrawalRecoveryUpdate{})
	require.NoError(t, err)
	require.False(t, won)
	won, err = r.Transition(ctx, row.ID, models.RecoverySendClaimed, models.RecoverySendUnknown, models.WithdrawalRecoveryUpdate{})
	require.NoError(t, err)
	require.True(t, won)
	saved, err = r.FindByOperation(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, "sealed-bytes", saved.SealedSignedTx)
}

func TestRecoveryCandidateWrongAmountRollsBackBeforeLink(t *testing.T) {
	r, row, b := recoveryRows(t)
	ctx := context.Background()
	won, err := r.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	call := `{"nonce":0}`
	won, err = r.Transition(ctx, row.ID, models.RecoveryClaimed, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &call})
	require.NoError(t, err)
	require.True(t, won)
	row.State = models.RecoverySigning
	row.CallJSON = call
	candidate := recoveryCandidate(row, b)
	candidate.Amount = "6.1"
	won, err = r.RecordCandidate(ctx, row, candidate, "sealed-bytes")
	require.Error(t, err)
	require.False(t, won)
	withdrawal, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, b.OriginalTransactionID, *withdrawal.TransactionID)
	count, err := facades.Orm().Query().Model(&models.Transaction{}).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
}

func TestRecoveryNonceReservationCannotMoveToAnotherBusinessOperation(t *testing.T) {
	r, row, b := recoveryRows(t)
	ctx := context.Background()
	won, err := r.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	other := *row
	other.ID = uuid.New()
	other.OperationID = uuid.New()
	b.OperationID = other.OperationID
	other.BindingJSON, _ = withdrawrecovery.Canonical(b)
	other.BindingHash, _ = withdrawrecovery.Hash(b)
	p := withdrawrecovery.Proposal{Binding: b}
	other.ProposalJSON, _ = withdrawrecovery.Canonical(p)
	other.ProposalHash, _ = withdrawrecovery.Hash(p)
	require.NoError(t, facades.Orm().Query().Create(&models.Withdrawal{ID: other.OperationID, WalletID: b.WalletID, TransactionID: &b.OriginalTransactionID, AccountID: &b.AccountID, Status: "failed", Amount: "6.1", DestinationAddress: b.Recipient, FeeEstimate: "0"}))
	won, err = r.Claim(ctx, &other)
	require.NoError(t, err)
	require.False(t, won)
	err = facades.Orm().Query().Create(&other)
	require.ErrorContains(t, err, "withdrawal_recovery_sender_nonce_unique")
}

func TestRecoveryClaimAuditFailureRollsBackReservation(t *testing.T) {
	r, row, _ := recoveryRows(t)
	q := facades.Orm().Query()
	_, err := q.Exec(`CREATE FUNCTION test_recovery_event_failure() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'test audit insert failure'; END; $$ LANGUAGE plpgsql`)
	require.NoError(t, err)
	_, err = q.Exec(`CREATE TRIGGER test_recovery_event_failure BEFORE INSERT ON withdrawal_recovery_events FOR EACH ROW EXECUTE FUNCTION test_recovery_event_failure()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = facades.Orm().Query().Exec(`DROP TRIGGER IF EXISTS test_recovery_event_failure ON withdrawal_recovery_events`)
		_, _ = facades.Orm().Query().Exec(`DROP FUNCTION IF EXISTS test_recovery_event_failure()`)
	})
	won, err := r.Claim(context.Background(), row)
	require.ErrorContains(t, err, "audit insert failure")
	require.False(t, won)
	count, err := q.Model(&models.WithdrawalRecoveryAttempt{}).Count()
	require.NoError(t, err)
	require.Zero(t, count)
}

func TestRecoveryCandidateLinkFailureRollsBackSignedRowAndCAS(t *testing.T) {
	r, row, b := recoveryRows(t)
	ctx := context.Background()
	won, err := r.Claim(ctx, row)
	require.NoError(t, err)
	require.True(t, won)
	call := `{"nonce":0}`
	won, err = r.Transition(ctx, row.ID, models.RecoveryClaimed, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &call})
	require.NoError(t, err)
	require.True(t, won)
	row.State = models.RecoverySigning
	row.CallJSON = call
	q := facades.Orm().Query()
	_, err = q.Exec(`CREATE FUNCTION test_recovery_link_failure() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'test link persistence failure'; END; $$ LANGUAGE plpgsql`)
	require.NoError(t, err)
	_, err = q.Exec(`CREATE TRIGGER test_recovery_link_failure BEFORE UPDATE ON withdrawals FOR EACH ROW EXECUTE FUNCTION test_recovery_link_failure()`)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = facades.Orm().Query().Exec(`DROP TRIGGER IF EXISTS test_recovery_link_failure ON withdrawals`)
		_, _ = facades.Orm().Query().Exec(`DROP FUNCTION IF EXISTS test_recovery_link_failure()`)
	})
	candidate := recoveryCandidate(row, b)
	won, err = r.RecordCandidate(ctx, row, candidate, "sealed-bytes")
	require.ErrorContains(t, err, "link persistence failure")
	require.False(t, won)
	saved, err := r.FindByOperation(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, models.RecoverySigning, saved.State)
	require.Nil(t, saved.CandidateTransactionID)
	require.Empty(t, saved.SealedSignedTx)
	count, err := q.Model(&models.Transaction{}).Count()
	require.NoError(t, err)
	require.EqualValues(t, 1, count)
	withdrawal, err := repositories.NewWithdrawalRepository(nil).FindByID(ctx, b.OperationID)
	require.NoError(t, err)
	require.Equal(t, b.OriginalTransactionID, *withdrawal.TransactionID)
}
