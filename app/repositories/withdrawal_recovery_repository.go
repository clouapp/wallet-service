package repositories

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/app/services/withdrawrecovery"
	"github.com/macrowallets/waas/pkg/amount"
	"github.com/macrowallets/waas/pkg/pgerr"
)

type WithdrawalRecoveryRepository struct{ db.Base }

func NewWithdrawalRecoveryRepository(query orm.Query) *WithdrawalRecoveryRepository {
	return &WithdrawalRecoveryRepository{Base: db.NewBase(query)}
}

func (r *WithdrawalRecoveryRepository) FindByOperation(ctx context.Context, id uuid.UUID) (*models.WithdrawalRecoveryAttempt, error) {
	var row models.WithdrawalRecoveryAttempt
	if err := r.Query(ctx).Where("operation_id = ?", id).FirstOrFail(&row); err != nil {
		return nil, db.LookupError(err, "find recovery attempt")
	}
	return &row, nil
}

// Claim owns its transaction: no key or send may rely on an uncommitted row.
// Row locks recheck the original provider binding before reserving the nonce.
func (r *WithdrawalRecoveryRepository) Claim(ctx context.Context, row *models.WithdrawalRecoveryAttempt) (bool, error) {
	if row == nil || row.State != models.RecoveryClaimed {
		return false, fmt.Errorf("recovery: invalid claim")
	}
	if err := r.requirePhysicalWriter(ctx); err != nil {
		return false, err
	}
	b, bindingErr := recoveryBinding(row)
	if bindingErr != nil {
		return false, bindingErr
	}
	won := false
	err := r.Transaction(ctx, func(tx orm.Query) error {
		if err := lockRecoveryKey(tx, "operation:"+row.OperationID.String()); err != nil {
			return err
		}
		if err := lockRecoveryKey(tx, fmt.Sprintf("nonce:%d:%s:%s", row.NetworkID, strings.ToLower(row.Sender), row.SenderNonce)); err != nil {
			return err
		}
		count, err := tx.Model(&models.WithdrawalRecoveryAttempt{}).Where("operation_id = ? OR (network_id = ? AND sender = ? AND sender_nonce = ?)", row.OperationID, row.NetworkID, row.Sender, row.SenderNonce).Count()
		if err != nil {
			return err
		}
		if count != 0 {
			return nil
		}
		if err := validateRecoveryRows(tx, b); err != nil {
			return err
		}
		if err := tx.Create(row); err != nil {
			return err
		}
		if err := appendRecoveryEvent(tx, row.ID, "", models.RecoveryClaimed, "{}"); err != nil {
			return err
		}
		won = true
		return nil
	})
	if pgerr.IsUniqueViolation(err) {
		return false, nil
	}
	return won, err
}

func (r *WithdrawalRecoveryRepository) requirePhysicalWriter(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("recovery: context is required")
	}
	if r.Query(ctx).InTransaction() {
		return fmt.Errorf("recovery: an outer transaction cannot establish a durable claim")
	}
	return nil
}

func recoveryBinding(row *models.WithdrawalRecoveryAttempt) (withdrawrecovery.Binding, error) {
	var b withdrawrecovery.Binding
	if row == nil || json.Unmarshal([]byte(row.BindingJSON), &b) != nil {
		return b, fmt.Errorf("recovery: invalid immutable binding")
	}
	if err := b.Validate(); err != nil {
		return b, err
	}
	hash, _ := withdrawrecovery.Hash(b)
	if row.ID == uuid.Nil || row.OperationID != b.OperationID || row.OriginalTransactionID != b.OriginalTransactionID || row.WalletID != b.WalletID || row.AccountID != b.AccountID || row.SourceAddressID != b.SourceAddressID || row.NetworkID != b.NetworkID || row.Sender != strings.ToLower(b.SourceAddress) || row.SenderNonce != fmt.Sprint(*b.Nonce) || row.BindingHash != hash {
		return b, fmt.Errorf("recovery: immutable claim fields disagree")
	}
	var p withdrawrecovery.Proposal
	if json.Unmarshal([]byte(row.ProposalJSON), &p) != nil {
		return b, fmt.Errorf("recovery: invalid proposal")
	}
	proposalHash, _ := withdrawrecovery.Hash(p)
	proposalBindingHash, _ := withdrawrecovery.Hash(p.Binding)
	if proposalHash != row.ProposalHash || proposalBindingHash != hash {
		return b, fmt.Errorf("recovery: proposal fields disagree")
	}
	return b, nil
}

func lockRecoveryKey(tx orm.Query, key string) error {
	_, err := tx.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "withdrawal-recovery:"+key)
	return err
}

func validateRecoveryRows(tx orm.Query, b withdrawrecovery.Binding) error {
	var account models.Account
	if err := tx.LockForUpdate().Where("id = ?", b.AccountID).FirstOrFail(&account); err != nil {
		return err
	}
	if account.Environment != models.EnvironmentTest || account.Status != "active" {
		return fmt.Errorf("recovery: account is not active test")
	}
	var chainRow models.Chain
	if err := tx.LockForUpdate().Where("id = ?", b.Chain).FirstOrFail(&chainRow); err != nil {
		return err
	}
	if chainRow.AdapterType != models.AdapterTypeEVM || !chainRow.IsTestnet || chainRow.NetworkID == nil || *chainRow.NetworkID != b.NetworkID || chainRow.Status != "active" || chainRow.RequiredConfirmations != b.RequiredConfirmations || chainRow.FinalityPolicy() != b.Finality {
		return fmt.Errorf("recovery: chain binding changed")
	}
	var wallet models.Wallet
	if err := tx.Select("id", "account_id", "chain", "status", "frozen_until").LockForUpdate().Where("id = ?", b.WalletID).FirstOrFail(&wallet); err != nil {
		return err
	}
	if wallet.AccountID == nil || *wallet.AccountID != b.AccountID || wallet.Chain != b.Chain || wallet.Status != "active" || (wallet.FrozenUntil != nil && wallet.FrozenUntil.After(time.Now().UTC())) {
		return fmt.Errorf("recovery: wallet binding changed")
	}
	var source models.Address
	if err := tx.LockForUpdate().Where("id = ?", b.SourceAddressID).FirstOrFail(&source); err != nil {
		return err
	}
	if source.WalletID != b.WalletID || source.Chain != b.Chain || !source.IsActive || !strings.EqualFold(source.Address, b.SourceAddress) || source.DerivationIndex != b.SourceDerivationIndex || source.ExternalUserID != b.SourceExternalUserID {
		return fmt.Errorf("recovery: source binding changed")
	}
	var withdrawal models.Withdrawal
	if err := tx.LockForUpdate().Where("id = ?", b.OperationID).FirstOrFail(&withdrawal); err != nil {
		return err
	}
	units, err := amount.DecimalToBaseUnits(amount.NormalizeDecimal(withdrawal.Amount), b.TokenDecimals)
	if err != nil || units.String() != b.AmountRaw || withdrawal.WalletID != b.WalletID || withdrawal.AccountID == nil || *withdrawal.AccountID != b.AccountID || withdrawal.TransactionID == nil || *withdrawal.TransactionID != b.OriginalTransactionID || withdrawal.Status != models.WithdrawalStatusFailed || withdrawal.DestinationTag != nil || !strings.EqualFold(withdrawal.DestinationAddress, b.Recipient) {
		return fmt.Errorf("recovery: original withdrawal changed")
	}
	var original models.Transaction
	if err := tx.LockForUpdate().Where("id = ?", b.OriginalTransactionID).FirstOrFail(&original); err != nil {
		return err
	}
	if original.Amount != b.AmountRaw || original.WalletID != b.WalletID || original.AddressID == nil || *original.AddressID != b.SourceAddressID || original.Chain != b.Chain || original.Asset != b.Asset || original.TxType != models.TxTypeWithdrawal || original.Status != "failed" || original.ErrorMessage != "broadcast_refused" || original.IdempotencyKey != nil || !strings.EqualFold(original.TxHash, b.OriginalTxHash) || !strings.EqualFold(original.FromAddress, b.SourceAddress) || !strings.EqualFold(original.ToAddress, b.Recipient) || !strings.EqualFold(original.TokenContract, b.TokenContract) || original.RequiredConfs != b.RequiredConfirmations || original.FinalityPolicy() != b.Finality {
		return fmt.Errorf("recovery: original transaction changed")
	}
	var payload struct {
		Outcome  string  `json:"broadcast_outcome"`
		Nonce    *uint64 `json:"sender_nonce"`
		SignedTx string  `json:"signed_tx"`
	}
	if json.Unmarshal([]byte(original.RawPayload), &payload) != nil || payload.Outcome != chain.BroadcastOutcomeRefused || payload.Nonce == nil || *payload.Nonce != *b.Nonce || payload.SignedTx != "" {
		return fmt.Errorf("recovery: original send proof is ineligible")
	}
	return nil
}

func allowedRecoveryTransition(from, to string) bool {
	switch from {
	case models.RecoveryClaimed:
		return to == models.RecoveryGasClaimed || to == models.RecoverySigning || to == models.RecoveryNeedsReconciliation
	case models.RecoveryGasClaimed:
		return to == models.RecoveryGasReady || to == models.RecoveryNeedsReconciliation
	case models.RecoveryGasReady:
		return to == models.RecoverySigning || to == models.RecoveryNeedsReconciliation
	case models.RecoverySigning:
		return to == models.RecoveryNeedsReconciliation
	case models.RecoverySigned, models.RecoverySendUnknown:
		return to == models.RecoverySendClaimed || to == models.RecoveryNeedsReconciliation
	case models.RecoverySendClaimed:
		return to == models.RecoveryBroadcast || to == models.RecoverySendUnknown || to == models.RecoveryNeedsReconciliation
	}
	return false
}

func (r *WithdrawalRecoveryRepository) Transition(ctx context.Context, id uuid.UUID, from, to string, update models.WithdrawalRecoveryUpdate) (bool, error) {
	if err := r.requirePhysicalWriter(ctx); err != nil {
		return false, err
	}
	if !allowedRecoveryTransition(from, to) {
		return false, fmt.Errorf("recovery: forbidden state transition")
	}
	if update.CallJSON != nil && to != models.RecoverySigning {
		return false, fmt.Errorf("recovery: exact call can only be fixed before signing")
	}
	if update.GasResultJSON != nil && to != models.RecoveryGasReady {
		return false, fmt.Errorf("recovery: gas proof can only be recorded after preparation")
	}
	won := false
	err := r.Transaction(ctx, func(tx orm.Query) error {
		var row models.WithdrawalRecoveryAttempt
		if err := tx.LockForUpdate().Where("id = ?", id).FirstOrFail(&row); err != nil {
			return err
		}
		if row.State != from {
			return nil
		}
		columns := map[string]any{"state": to, "updated_at": time.Now().UTC()}
		if update.CallJSON != nil {
			columns["call_json"] = *update.CallJSON
		}
		if update.GasResultJSON != nil {
			columns["gas_result_json"] = *update.GasResultJSON
		}
		observation := "{}"
		if update.ObservationJSON != nil {
			observation = *update.ObservationJSON
			columns["observation_json"] = observation
		}
		res, err := tx.Model(&models.WithdrawalRecoveryAttempt{}).Where("id = ? AND state = ?", id, from).Update(columns)
		if err != nil {
			return err
		}
		if res == nil || res.RowsAffected != 1 {
			return nil
		}
		if (to == models.RecoveryBroadcast || to == models.RecoverySendUnknown) && row.CandidateTransactionID != nil {
			var candidate models.Transaction
			if err := tx.LockForUpdate().Where("id = ?", *row.CandidateTransactionID).FirstOrFail(&candidate); err != nil {
				return err
			}
			if outcomeErr := recordRecoverySendOutcome(tx, row, candidate, to); outcomeErr != nil {
				return outcomeErr
			}
		}
		if err := appendRecoveryEvent(tx, id, from, to, observation); err != nil {
			return err
		}
		won = true
		return nil
	})
	return won, err
}

// recordRecoverySendOutcome cannot roll back a tracker result that committed
// while BroadcastTransaction was in flight. Only the current nonterminal
// candidate can advance to broadcast; matching terminal evidence stays intact.
func recordRecoverySendOutcome(tx orm.Query, attempt models.WithdrawalRecoveryAttempt, candidate models.Transaction, to string) error {
	var withdrawal models.Withdrawal
	if err := tx.LockForUpdate().Where("id = ?", attempt.OperationID).FirstOrFail(&withdrawal); err != nil {
		return err
	}
	if withdrawal.WalletID != attempt.WalletID || withdrawal.TransactionID == nil || *withdrawal.TransactionID != candidate.ID {
		return fmt.Errorf("recovery: withdrawal candidate link changed")
	}
	switch withdrawal.Status {
	case models.WithdrawalStatusFailed:
		if candidate.Status != models.WithdrawalStatusFailed || withdrawal.FailureReason == nil || *withdrawal.FailureReason == "" {
			return fmt.Errorf("recovery: failed candidate outcome is inconsistent")
		}
		return nil
	case models.WithdrawalStatusConfirmed:
		if candidate.Status != models.WithdrawalStatusConfirmed || withdrawal.FailureReason != nil {
			return fmt.Errorf("recovery: confirmed candidate outcome is inconsistent")
		}
		return nil
	case models.WithdrawalStatusBroadcasting, models.WithdrawalStatusBroadcast:
		if candidate.Status == models.WithdrawalStatusFailed || candidate.Status == models.WithdrawalStatusConfirmed {
			return fmt.Errorf("recovery: terminal candidate outcome is not reconciled")
		}
	default:
		return fmt.Errorf("recovery: withdrawal candidate state changed")
	}
	var payload map[string]any
	if json.Unmarshal([]byte(candidate.RawPayload), &payload) != nil {
		return fmt.Errorf("recovery: candidate payload invalid")
	}
	outcome := chain.BroadcastOutcomeUnknown
	if to == models.RecoveryBroadcast {
		outcome = chain.BroadcastOutcomeAccepted
	}
	payload[chain.BroadcastOutcomePayloadKey] = outcome
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err = tx.Model(&models.Transaction{}).Where("id = ?", candidate.ID).Update("raw_payload", string(raw)); err != nil {
		return err
	}
	res, err := tx.Model(&models.Withdrawal{}).Where("id = ? AND wallet_id = ? AND transaction_id = ? AND status IN (?, ?)", attempt.OperationID, attempt.WalletID, candidate.ID, models.WithdrawalStatusBroadcasting, models.WithdrawalStatusBroadcast).Update(map[string]any{"status": models.WithdrawalStatusBroadcast, "failure_reason": nil})
	if err != nil {
		return err
	}
	if res == nil || res.RowsAffected != 1 {
		return fmt.Errorf("recovery: withdrawal candidate state changed")
	}
	return nil
}

// RecordCandidate atomically preserves the old row, records the new signed row,
// and CAS-links the existing business withdrawal to it before any transmission.
func (r *WithdrawalRecoveryRepository) RecordCandidate(ctx context.Context, expected *models.WithdrawalRecoveryAttempt, candidate *models.Transaction, sealed string) (bool, error) {
	if err := r.requirePhysicalWriter(ctx); err != nil {
		return false, err
	}
	if expected == nil || candidate == nil || sealed == "" {
		return false, fmt.Errorf("recovery: signed candidate is required")
	}
	b, bindingErr := recoveryBinding(expected)
	if bindingErr != nil {
		return false, bindingErr
	}
	if err := validateRecoveryCandidate(expected, candidate, b, sealed); err != nil {
		return false, err
	}
	won := false
	err := r.Transaction(ctx, func(tx orm.Query) error {
		if err := lockRecoveryKey(tx, "operation:"+expected.OperationID.String()); err != nil {
			return err
		}
		var row models.WithdrawalRecoveryAttempt
		if err := tx.LockForUpdate().Where("id = ?", expected.ID).FirstOrFail(&row); err != nil {
			return err
		}
		if row.State != models.RecoverySigning || row.CandidateTransactionID != nil {
			return nil
		}
		if row.BindingHash != expected.BindingHash || row.ProposalHash != expected.ProposalHash || !sameRecoveryJSON(row.CallJSON, expected.CallJSON) {
			return fmt.Errorf("recovery: signed claim changed")
		}
		if err := validateRecoveryRows(tx, b); err != nil {
			return err
		}
		if err := candidate.ValidateAmounts(); err != nil {
			return err
		}
		if err := tx.Create(candidate); err != nil {
			return err
		}
		res, err := tx.Model(&models.WithdrawalRecoveryAttempt{}).Where("id = ? AND state = ? AND candidate_transaction_id IS NULL", row.ID, models.RecoverySigning).Update(map[string]any{"state": models.RecoverySigned, "candidate_transaction_id": candidate.ID, "candidate_hash": candidate.TxHash, "sealed_signed_tx": sealed, "updated_at": time.Now().UTC()})
		if err != nil {
			return err
		}
		if res == nil || res.RowsAffected != 1 {
			return fmt.Errorf("recovery: candidate claim changed")
		}
		res, err = tx.Model(&models.Withdrawal{}).Where("id = ? AND wallet_id = ? AND transaction_id = ? AND status = ?", b.OperationID, b.WalletID, b.OriginalTransactionID, models.WithdrawalStatusFailed).Update(map[string]any{"transaction_id": candidate.ID, "status": models.WithdrawalStatusBroadcasting, "failure_reason": nil})
		if err != nil {
			return err
		}
		if res == nil || res.RowsAffected != 1 {
			return fmt.Errorf("recovery: original withdrawal link changed")
		}
		if err := appendRecoveryEvent(tx, row.ID, models.RecoverySigning, models.RecoverySigned, `{"candidate_persisted_before_send":true}`); err != nil {
			return err
		}
		won = true
		return nil
	})
	return won, err
}

func validateRecoveryCandidate(row *models.WithdrawalRecoveryAttempt, c *models.Transaction, b withdrawrecovery.Binding, sealed string) error {
	if c.Amount != b.AmountRaw || c.ID == uuid.Nil || c.ID == b.OriginalTransactionID || c.AddressID == nil || *c.AddressID != b.SourceAddressID || c.WalletID != b.WalletID || c.ExternalUserID != b.SourceExternalUserID || c.Chain != b.Chain || c.TxType != models.TxTypeWithdrawal || c.Status != "pending" || c.Asset != b.Asset || !strings.EqualFold(c.TokenContract, b.TokenContract) || !strings.EqualFold(c.FromAddress, b.SourceAddress) || !strings.EqualFold(c.ToAddress, b.Recipient) || c.IdempotencyKey == nil || *c.IdempotencyKey != b.OperationID.String() || c.ParentTransactionID == nil || *c.ParentTransactionID != b.OriginalTransactionID || c.RequiredConfs != b.RequiredConfirmations || c.Finality != b.Finality {
		return fmt.Errorf("recovery: candidate economic binding differs")
	}
	var p struct {
		Attempt     string  `json:"recovery_attempt_id"`
		Operation   string  `json:"recovery_operation_id"`
		BindingHash string  `json:"recovery_binding_hash"`
		Predecessor string  `json:"recovery_predecessor_transaction_id"`
		Nonce       *uint64 `json:"sender_nonce"`
		Signed      string  `json:"signed_tx"`
		Outcome     string  `json:"broadcast_outcome"`
	}
	if json.Unmarshal([]byte(c.RawPayload), &p) != nil || p.Attempt != row.ID.String() || p.Operation != b.OperationID.String() || p.BindingHash != row.BindingHash || p.Predecessor != b.OriginalTransactionID.String() || p.Nonce == nil || *p.Nonce != *b.Nonce || p.Signed != sealed || p.Outcome != chain.BroadcastOutcomePending {
		return fmt.Errorf("recovery: candidate durable payload differs")
	}
	return nil
}

func appendRecoveryEvent(tx orm.Query, id uuid.UUID, from, to, observation string) error {
	if !json.Valid([]byte(observation)) {
		return fmt.Errorf("recovery: invalid observation")
	}
	return tx.Create(&models.WithdrawalRecoveryEvent{ID: uuid.New(), AttemptID: id, FromState: from, ToState: to, ObservationJSON: observation, CreatedAt: time.Now().UTC()})
}

var _ withdrawrecovery.Store = (*WithdrawalRecoveryRepository)(nil)

func sameRecoveryJSON(left, right string) bool {
	var l, r any
	a := json.NewDecoder(strings.NewReader(left))
	a.UseNumber()
	b := json.NewDecoder(strings.NewReader(right))
	b.UseNumber()
	return a.Decode(&l) == nil && b.Decode(&r) == nil && reflect.DeepEqual(l, r)
}
