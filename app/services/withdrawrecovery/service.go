package withdrawrecovery

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/mpcshare"
)

func NewService(deps Dependencies) (*Service, error) {
	if deps.Store == nil || deps.Observer == nil || deps.CodeRevision == "" {
		return nil, refused("store, observer and code revision are required")
	}
	if deps.Now == nil {
		deps.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{deps: deps}, nil
}

// Inspect constructs a proposal without acquiring a claim, fetching keys,
// seeding gas, signing or transmitting. An occupied operation stays occupied.
func (s *Service) Inspect(ctx context.Context, b Binding, fees FeeEnvelope, seed *GasSeedPlan, preparedBy string) (*Proposal, error) {
	if ctx == nil {
		return nil, refused("context is required")
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	if err := fees.Validate(); err != nil {
		return nil, err
	}
	if err := seed.validate(b); err != nil {
		return nil, err
	}
	if !identity(preparedBy) {
		return nil, refused("preparer identity is required")
	}
	existing, err := s.deps.Store.FindByOperation(ctx, b.OperationID)
	if err != nil && !errors.Is(err, ErrNotFound) && !errors.Is(err, models.ErrRepositoryNotFound) {
		return nil, err
	}
	if existing != nil {
		return nil, ErrOccupied
	}
	observation, err := s.deps.Observer.Observe(ctx, b, "")
	if err != nil {
		return nil, err
	}
	now := s.deps.Now()
	fee, err := observation.validate(b, fees, now)
	if err != nil {
		return nil, err
	}
	if err := requireFunding(observation, fee, seed); err != nil {
		return nil, err
	}
	return &Proposal{Binding: b, Fees: fees, GasSeed: seed, Observation: *observation, PreparedBy: preparedBy, PreparedAt: now, ExpiresAt: now.Add(MaxReviewAge), CodeRevision: s.deps.CodeRevision}, nil
}

func requireFunding(observation *Observation, fee *big.Int, seed *GasSeedPlan) error {
	native, _ := integer(observation.SourceNativeBalanceWei, false)
	if native.Cmp(fee) >= 0 {
		return nil
	}
	if seed == nil {
		return ErrGasNotReady
	}
	funding, _ := integer(seed.AmountWei, true)
	if new(big.Int).Add(native, funding).Cmp(fee) < 0 {
		return ErrGasNotReady
	}
	return nil
}

// Apply is explicit. Its durable claim is committed before every key or gas
// action. A replay returns the existing attempt and cannot create another one.
func (s *Service) Apply(ctx context.Context, p Proposal, review Review, operator, passphrase string) (*Result, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if ctx == nil {
		return nil, refused("context is required")
	}
	if err := validateReview(p, review, operator, s.deps.CodeRevision, s.deps.Now()); err != nil {
		return nil, err
	}
	if s.deps.Signer == nil || s.deps.Sealer == nil || s.deps.Sender == nil {
		return nil, refused("execution adapters are required")
	}
	current, err := s.deps.Observer.Observe(ctx, p.Binding, "")
	if err != nil {
		return nil, err
	}
	fee, err := current.validate(p.Binding, p.Fees, s.deps.Now())
	if err != nil {
		return nil, err
	}
	if fundingErr := requireFunding(current, fee, p.GasSeed); fundingErr != nil {
		return nil, fundingErr
	}
	if p.GasSeed != nil && s.deps.Gas == nil {
		return nil, refused("durable gas preparation adapter is required")
	}
	bindingJSON, _ := Canonical(p.Binding)
	bindingHash, _ := Hash(p.Binding)
	proposalJSON, _ := Canonical(p)
	proposalHash, _ := Hash(p)
	reviewJSON, _ := Canonical(review)
	row := &models.WithdrawalRecoveryAttempt{ID: uuid.New(), OperationID: p.Binding.OperationID, OriginalTransactionID: p.Binding.OriginalTransactionID, AccountID: p.Binding.AccountID, WalletID: p.Binding.WalletID, SourceAddressID: p.Binding.SourceAddressID, NetworkID: p.Binding.NetworkID, Sender: strings.ToLower(p.Binding.SourceAddress), SenderNonce: fmt.Sprint(*p.Binding.Nonce), BindingHash: bindingHash, BindingJSON: bindingJSON, ProposalHash: proposalHash, ProposalJSON: proposalJSON, ReviewJSON: reviewJSON, Operator: operator, State: models.RecoveryClaimed, CallJSON: "{}", GasResultJSON: "{}", ObservationJSON: "{}", CreatedAt: s.deps.Now(), UpdatedAt: s.deps.Now()}
	won, err := s.deps.Store.Claim(ctx, row)
	if err != nil {
		return nil, err
	}
	if !won {
		existing, findErr := s.deps.Store.FindByOperation(ctx, p.Binding.OperationID)
		if findErr != nil {
			return nil, findErr
		}
		return result(existing, true), ErrOccupied
	}
	return s.continueAttempt(ctx, row, p, passphrase)
}

// Lookup is always read-only and safe after a timeout or expired review.
func (s *Service) Lookup(ctx context.Context, operationID uuid.UUID) (*Result, error) {
	row, err := s.deps.Store.FindByOperation(ctx, operationID)
	if err != nil {
		return nil, err
	}
	return result(row, true), nil
}

// Resume finds this attempt, not a new plan. Before a candidate is persisted it
// may sign only the already-frozen exact call; after persistence it can only
// observe or replay those sealed bytes. It never raises the nonce.
func (s *Service) Resume(ctx context.Context, operationID uuid.UUID, operator, passphrase string) (*Result, error) {
	defer mpcshare.DiscardPassphrase(&passphrase)
	if ctx == nil {
		return nil, refused("context is required")
	}
	row, err := s.deps.Store.FindByOperation(ctx, operationID)
	if err != nil {
		return nil, err
	}
	if row.State == models.RecoveryBroadcast || row.State == models.RecoveryNeedsReconciliation {
		return result(row, true), nil
	}
	var p Proposal
	var review Review
	if json.Unmarshal([]byte(row.ProposalJSON), &p) != nil || json.Unmarshal([]byte(row.ReviewJSON), &review) != nil {
		return result(row, true), refused("stored review cannot be decoded")
	}
	if row.Operator != operator {
		return result(row, true), refused("operator changed")
	}
	if err := validateReview(p, review, operator, s.deps.CodeRevision, s.deps.Now()); err != nil {
		return result(row, true), err
	}
	hash, _ := Hash(p.Binding)
	proposalHash, _ := Hash(p)
	if hash != row.BindingHash || proposalHash != row.ProposalHash || p.Binding.OperationID != row.OperationID {
		return result(row, true), refused("stored immutable binding changed")
	}
	if s.deps.Signer == nil || s.deps.Sealer == nil || s.deps.Sender == nil {
		return result(row, true), refused("execution adapters are required")
	}
	return s.continueAttempt(ctx, row, p, passphrase)
}

func (s *Service) continueAttempt(ctx context.Context, row *models.WithdrawalRecoveryAttempt, p Proposal, passphrase string) (*Result, error) {
	if row.State == models.RecoverySigned || row.State == models.RecoverySendUnknown {
		return s.sendStored(ctx, row, p)
	}
	if row.State == models.RecoverySendClaimed {
		// A crash may happen immediately before or after the claimed send.
		// Absence does not tell which occurred. An explicit Resume may replay
		// only this same sealed hash after a fresh nonce/binding observation;
		// it cannot sign another transaction or conclude the old send failed.
		if _, err := s.observeEligible(ctx, row, p); err != nil {
			return result(row, true), err
		}
		safe := `{"outcome":"unknown_after_interruption","same_bytes_only":true,"final_failure_claimed":false}`
		if err := s.transition(ctx, row, models.RecoverySendUnknown, models.WithdrawalRecoveryUpdate{ObservationJSON: &safe}); err != nil {
			return result(row, true), err
		}
		return s.sendStored(ctx, row, p)
	}
	if row.State == models.RecoveryGasClaimed {
		if s.deps.Gas == nil || p.GasSeed == nil {
			return result(row, true), ErrNeedsReconciliation
		}
		// Re-read the original and its exact nonce before any continuation.
		if _, err := s.observeEligible(ctx, row, p); err != nil {
			return result(row, true), err
		}
		proof, observeErr := s.deps.Gas.Observe(ctx, row)
		if observeErr != nil || proof == nil || !proof.Confirmed {
			resumer, ok := s.deps.Gas.(GasPreparationResumer)
			if !ok {
				if observeErr != nil {
					return result(row, true), observeErr
				}
				return result(row, true), ErrNeedsReconciliation
			}
			// Observe may have taken time or seen the old transaction become
			// valid. Its failure alone never grants a new gas transfer.
			if _, err := s.observeEligible(ctx, row, p); err != nil {
				return result(row, true), err
			}
			if err := s.ensureAttemptReview(row, p); err != nil {
				return result(row, true), err
			}
			var err error
			proof, err = resumer.Resume(ctx, row, p.Binding, *p.GasSeed, passphrase)
			if err != nil {
				return result(row, true), err
			}
		}
		if err := s.finishGas(ctx, row, p, proof); err != nil {
			return result(row, true), err
		}
	}
	if row.State == models.RecoveryClaimed {
		observation, err := s.observeEligible(ctx, row, p)
		if err != nil {
			return result(row, true), err
		}
		fee, _ := observation.Quote.fee(p.Fees)
		native, _ := integer(observation.SourceNativeBalanceWei, false)
		if native.Cmp(fee) < 0 {
			if p.GasSeed == nil || s.deps.Gas == nil {
				return result(row, true), ErrGasNotReady
			}
			if err := s.transition(ctx, row, models.RecoveryGasClaimed, models.WithdrawalRecoveryUpdate{}); err != nil {
				return result(row, true), err
			}
			if err := s.ensureAttemptReview(row, p); err != nil {
				return result(row, true), err
			}
			proof, err := s.deps.Gas.Prepare(ctx, row, p.Binding, *p.GasSeed, passphrase)
			if err != nil {
				return result(row, true), err
			}
			if err := s.finishGas(ctx, row, p, proof); err != nil {
				return result(row, true), err
			}
		}
	}
	if row.State != models.RecoveryClaimed && row.State != models.RecoveryGasReady && row.State != models.RecoverySigning {
		return result(row, true), ErrNeedsReconciliation
	}
	observation, err := s.observeEligible(ctx, row, p)
	if err != nil {
		return result(row, true), err
	}
	var frozen frozenCall
	if row.State == models.RecoverySigning {
		if json.Unmarshal([]byte(row.CallJSON), &frozen) != nil {
			return result(row, true), refused("stored exact call cannot be decoded")
		}
	} else {
		frozen = makeCall(p.Binding, observation.Quote)
		callJSON, _ := Canonical(frozen)
		if transitionErr := s.transition(ctx, row, models.RecoverySigning, models.WithdrawalRecoveryUpdate{CallJSON: &callJSON}); transitionErr != nil {
			return result(row, true), transitionErr
		}
	}
	call, fee, err := frozen.validate(p.Binding, p.Fees)
	if err != nil {
		return result(row, true), err
	}
	native, _ := integer(observation.SourceNativeBalanceWei, false)
	if native.Cmp(fee) < 0 {
		return result(row, true), ErrGasNotReady
	}
	// Signing cannot transmit. A concurrent/crashed signer can produce another
	// signature of this same frozen nonce/call, but only one candidate CAS wins.
	if reviewErr := s.ensureAttemptReview(row, p); reviewErr != nil {
		return result(row, true), reviewErr
	}
	signed, err := s.deps.Signer.SignEVMRecoveryCall(ctx, p.Binding.WalletID, p.Binding.SourceAddressID, p.Binding.NetworkID, call, passphrase)
	if err != nil {
		return result(row, true), err
	}
	if signed == nil {
		return result(row, true), refused("signer returned no transaction")
	}
	defer clear(signed.RawBytes)
	hash, err := verifySigned(signed.RawBytes, p.Binding, frozen, p.Fees)
	if err != nil {
		return result(row, true), err
	}
	if signed.TxHash != "" && !strings.EqualFold(signed.TxHash, hash) {
		return result(row, true), refused("signer hash does not match signed bytes")
	}
	if _, err = s.observeEligible(ctx, row, p); err != nil {
		return result(row, true), err
	}
	sealed, err := s.deps.Sealer.Seal(hex.EncodeToString(signed.RawBytes))
	if err != nil || sealed == "" {
		return result(row, true), refused("signed transaction could not be sealed")
	}
	tx := candidateTransaction(row, p.Binding, hash, sealed, s.deps.Now())
	won, err := s.deps.Store.RecordCandidate(ctx, row, tx, sealed)
	if err != nil {
		return result(row, true), err
	}
	if !won {
		return result(row, true), ErrConcurrent
	}
	row.State = models.RecoverySigned
	row.CandidateTransactionID = &tx.ID
	row.CandidateHash = hash
	row.SealedSignedTx = sealed
	return s.sendStored(ctx, row, p)
}

func (s *Service) ensureAttemptReview(row *models.WithdrawalRecoveryAttempt, p Proposal) error {
	var review Review
	if json.Unmarshal([]byte(row.ReviewJSON), &review) != nil {
		return refused("stored review cannot be decoded")
	}
	proposalHash, _ := Hash(p)
	if row.ProposalHash != proposalHash {
		return refused("stored proposal changed")
	}
	return validateReview(p, review, row.Operator, s.deps.CodeRevision, s.deps.Now())
}

func (s *Service) observeEligible(ctx context.Context, row *models.WithdrawalRecoveryAttempt, p Proposal) (*Observation, error) {
	observation, err := s.deps.Observer.Observe(ctx, p.Binding, row.CandidateHash)
	if err != nil {
		return nil, err
	}
	_, err = observation.validate(p.Binding, p.Fees, s.deps.Now())
	if errors.Is(err, ErrNeedsReconciliation) {
		safe := `{"reason":"original_or_candidate_known_or_nonce_changed","final_failure_claimed":false}`
		if transitionErr := s.transition(ctx, row, models.RecoveryNeedsReconciliation, models.WithdrawalRecoveryUpdate{ObservationJSON: &safe}); transitionErr != nil {
			return nil, transitionErr
		}
	}
	return observation, err
}

func (s *Service) finishGas(ctx context.Context, row *models.WithdrawalRecoveryAttempt, p Proposal, proof *GasResult) error {
	if p.GasSeed == nil || proof == nil || !proof.Confirmed || proof.TransactionID == uuid.Nil || !txDigest.MatchString(proof.TxHash) || proof.NetworkID != p.Binding.NetworkID || !strings.EqualFold(proof.From, p.GasSeed.BaseAddress) || !strings.EqualFold(proof.To, p.Binding.SourceAddress) || proof.Nonce == nil || *proof.Nonce != *p.GasSeed.Nonce || proof.AmountWei != p.GasSeed.AmountWei {
		return ErrNeedsReconciliation
	}
	fee, err := integer(proof.FeeWei, false)
	if err != nil {
		return err
	}
	maxFee, _ := integer(p.GasSeed.MaxFeeWei, true)
	if fee.Cmp(maxFee) > 0 {
		return refused("gas prerequisite fee exceeded review")
	}
	data, _ := Canonical(proof)
	return s.transition(ctx, row, models.RecoveryGasReady, models.WithdrawalRecoveryUpdate{GasResultJSON: &data})
}

func (s *Service) sendStored(ctx context.Context, row *models.WithdrawalRecoveryAttempt, p Proposal) (*Result, error) {
	if row.CandidateTransactionID == nil || row.CandidateHash == "" || row.SealedSignedTx == "" {
		return result(row, true), refused("stored candidate is incomplete")
	}
	observation, err := s.observeEligible(ctx, row, p)
	if err != nil {
		return result(row, true), err
	}
	encoded, err := s.deps.Sealer.Open(row.SealedSignedTx)
	if err != nil {
		return result(row, true), refused("stored signed bytes could not be opened")
	}
	raw, err := hex.DecodeString(encoded)
	if err != nil {
		return result(row, true), refused("stored signed bytes are invalid")
	}
	defer clear(raw)
	var frozen frozenCall
	if json.Unmarshal([]byte(row.CallJSON), &frozen) != nil {
		return result(row, true), refused("stored exact call cannot be decoded")
	}
	_, fee, err := frozen.validate(p.Binding, p.Fees)
	if err != nil {
		return result(row, true), err
	}
	native, _ := integer(observation.SourceNativeBalanceWei, false)
	if native.Cmp(fee) < 0 {
		return result(row, true), ErrGasNotReady
	}
	hash, err := verifySigned(raw, p.Binding, frozen, p.Fees)
	if err != nil {
		return result(row, true), err
	}
	if !strings.EqualFold(hash, row.CandidateHash) {
		return result(row, true), refused("stored signed bytes hash changed")
	}
	// This committed CAS is the transmission claim. Unknown replies retain the
	// candidate and sealed bytes; Resume never invokes Signer for this state.
	if err := s.ensureAttemptReview(row, p); err != nil {
		return result(row, true), err
	}
	if err := s.transition(ctx, row, models.RecoverySendClaimed, models.WithdrawalRecoveryUpdate{}); err != nil {
		return result(row, true), err
	}
	returned, sendErr := s.deps.Sender.SendExact(ctx, p.Binding, raw)
	state := models.RecoveryBroadcast
	safe := `{"outcome":"accepted"}`
	if sendErr != nil || !strings.EqualFold(returned, hash) {
		state = models.RecoverySendUnknown
		safe = `{"outcome":"unknown","sealed_bytes_retained":true}`
	}
	if err := s.transition(ctx, row, state, models.WithdrawalRecoveryUpdate{ObservationJSON: &safe}); err != nil {
		return result(row, true), err
	}
	if sendErr != nil {
		return result(row, true), ErrNeedsReconciliation
	}
	if state == models.RecoverySendUnknown {
		return result(row, true), ErrNeedsReconciliation
	}
	return result(row, false), nil
}

func (s *Service) transition(ctx context.Context, row *models.WithdrawalRecoveryAttempt, to string, update models.WithdrawalRecoveryUpdate) error {
	won, err := s.deps.Store.Transition(ctx, row.ID, row.State, to, update)
	if err != nil {
		return err
	}
	if !won {
		return ErrConcurrent
	}
	row.State = to
	if update.CallJSON != nil {
		row.CallJSON = *update.CallJSON
	}
	if update.GasResultJSON != nil {
		row.GasResultJSON = *update.GasResultJSON
	}
	if update.ObservationJSON != nil {
		row.ObservationJSON = *update.ObservationJSON
	}
	return nil
}

func result(row *models.WithdrawalRecoveryAttempt, existing bool) *Result {
	return &Result{OperationID: row.OperationID, AttemptID: row.ID, State: row.State, CandidateHash: row.CandidateHash, Existing: existing}
}

func candidateTransaction(row *models.WithdrawalRecoveryAttempt, b Binding, hash, sealed string, now time.Time) *models.Transaction {
	op := b.OperationID.String()
	parent := b.OriginalTransactionID
	address := b.SourceAddressID
	raw, _ := Canonical(map[string]any{chain.BroadcastOutcomePayloadKey: chain.BroadcastOutcomePending, chain.SenderNoncePayloadKey: *b.Nonce, chain.SenderPayloadKey: b.SourceAddress, chain.SignedTxPayloadKey: sealed, "recovery_attempt_id": row.ID.String(), "recovery_predecessor_transaction_id": b.OriginalTransactionID.String(), "recovery_binding_hash": row.BindingHash, "recovery_operation_id": b.OperationID.String()})
	return &models.Transaction{ID: uuid.New(), AddressID: &address, WalletID: b.WalletID, ExternalUserID: b.SourceExternalUserID, Chain: b.Chain, TxType: models.TxTypeWithdrawal, TxHash: hash, LogIndex: models.TransactionLogIndexNone, FromAddress: b.SourceAddress, ToAddress: b.Recipient, Amount: b.AmountRaw, Asset: b.Asset, TokenContract: b.TokenContract, RequiredConfs: b.RequiredConfirmations, Status: "pending", IdempotencyKey: &op, Direction: models.TxDirectionOutbound, Source: models.TxSourceWithdrawalFlow, ParentTransactionID: &parent, Origin: models.TxOriginUserRequest, RawPayload: raw, Finality: b.Finality}
}
