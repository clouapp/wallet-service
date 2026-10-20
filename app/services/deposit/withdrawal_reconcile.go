package deposit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// Verdicts of ReconcileWithdrawal.
const (
	// ReconcileSettled is a withdrawal whose transaction is already confirmed or failed.
	ReconcileSettled = "settled"
	// ReconcileLanded is a transaction in a block: the tracker counts its confirmations.
	ReconcileLanded = "landed"
	// ReconcileFailed is a transaction proven failed: on chain, not_landed or expired.
	ReconcileFailed = "failed"
	// ReconcileUnknown is a transaction nothing proves landed or failed yet.
	ReconcileUnknown = "unknown"
)

// WithdrawalFinder finds a withdrawal row and links it to its transaction (the
// withdrawal repository).
type WithdrawalFinder interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Withdrawal, error)
	LinkTransaction(ctx context.Context, id, transactionID uuid.UUID) error
}

// TransactionFinder finds a transaction row by id or idempotency key (the
// transaction repository).
type TransactionFinder interface {
	FindByID(ctx context.Context, id uuid.UUID) (*models.Transaction, error)
	FindByIdempotencyKey(ctx context.Context, key string) (*models.Transaction, error)
}

// SetWithdrawalReconciliation wires the rows withdrawals:reconcile reads.
func (s *Service) SetWithdrawalReconciliation(withdrawals WithdrawalFinder, transactions TransactionFinder) {
	s.reconcileWithdrawals, s.reconcileTransactions = withdrawals, transactions
}

// WithdrawalReconciliation asks ReconcileWithdrawal about one withdrawal, or one
// outbound transaction (a sweep, a gas seed, a row no withdrawal points at), by its
// id; Apply records what it proves.
type WithdrawalReconciliation struct {
	ID    uuid.UUID
	Apply bool
}

// WithdrawalReconcileReport is what the chain says about a withdrawal's transaction.
// WithdrawalID is nil for a transaction reconciled by its own id.
type WithdrawalReconcileReport struct {
	WithdrawalID  uuid.UUID
	TransactionID uuid.UUID
	Chain         string
	TxHash        string
	Status        string
	Verdict       string
	Block         uint64
	FailureCode   string
	// Reason says why the verdict is unknown, or what proved it.
	Reason  string
	Applied bool
}

// ReconcileWithdrawal settles a withdrawal whose transaction nothing settled (or an
// outbound transaction named by its own id; see reconcileTarget, which also finds
// the transfer of a withdrawal sent but never linked, and links it with Apply): it
// reads the transaction by its hash on every endpoint (chain.TransactionSearcher),
// then, for an EVM row with its sender nonce, the transaction that spent the nonce,
// or, for a Bitcoin-family row with its inputs, the transactions that spent them
// (positive proofs, whatever the broadcast answered), then an expiry the chain can
// prove (XRP, Solana). Landed records its block (the tracker then confirms it by its
// confirmations); failed fails the transaction and its withdrawal and sends
// withdrawal.failed once; unknown says why and changes nothing. Nothing is written
// without Apply.
func (s *Service) ReconcileWithdrawal(ctx context.Context, in WithdrawalReconciliation) (WithdrawalReconcileReport, error) {
	report := WithdrawalReconcileReport{}
	if s.reconcileWithdrawals == nil || s.reconcileTransactions == nil {
		return report, fmt.Errorf("withdrawal reconciliation is not configured")
	}
	tx, link, err := s.reconcileTarget(ctx, in.ID, &report)
	if err != nil {
		return report, err
	}
	report.TransactionID, report.Chain, report.TxHash, report.Status = tx.ID, tx.Chain, tx.TxHash, tx.Status
	switch {
	case tx.Status == string(types.TxStatusConfirmed) || tx.Status == string(types.TxStatusFailed):
		report.Verdict, report.Reason = ReconcileSettled, "the transaction is already "+tx.Status
		return report, nil
	case tx.BlockNumber > 0:
		report.Verdict, report.Block = ReconcileLanded, uint64(tx.BlockNumber)
		report.Reason = "the block is recorded; the tracker counts its confirmations"
		return report, nil
	}
	adapter, err := s.registry.Chain(tx.Chain)
	if err != nil {
		return report, err
	}
	s.judge(ctx, adapter, *tx, &report, in.Apply)
	if link {
		report.Reason += "; the withdrawal is not linked to this transaction (sent under its key, never marked)"
	}
	if in.Apply {
		if link {
			if err := s.reconcileWithdrawals.LinkTransaction(ctx, report.WithdrawalID, tx.ID); err != nil {
				return report, fmt.Errorf("link withdrawal %s to transaction %s: %w", report.WithdrawalID, tx.ID, err)
			}
		}
		s.applyReconciliation(ctx, adapter, *tx, &report)
	}
	return report, nil
}

// reconcileTarget is the transaction id names: a withdrawal's (the one it points at,
// or, when it points at none, the one recorded under its key: sent, never marked,
// and link is true), else an outbound transaction of that id.
func (s *Service) reconcileTarget(ctx context.Context, id uuid.UUID, report *WithdrawalReconcileReport) (*models.Transaction, bool, error) {
	withdrawal, err := s.reconcileWithdrawals.FindByID(ctx, id)
	if err == nil && withdrawal != nil {
		report.WithdrawalID = withdrawal.ID
		if withdrawal.TransactionID != nil {
			tx, findErr := s.reconcileTransactions.FindByID(ctx, *withdrawal.TransactionID)
			if findErr != nil || tx == nil {
				return nil, false, fmt.Errorf("transaction %s of withdrawal %s: %w", *withdrawal.TransactionID, withdrawal.ID, errors.Join(models.ErrRepositoryNotFound, findErr))
			}
			return tx, false, nil
		}
		tx, findErr := s.reconcileTransactions.FindByIdempotencyKey(ctx, withdrawal.ID.String())
		if findErr != nil || tx == nil {
			return nil, false, fmt.Errorf("withdrawal %s (%s) has no transaction: nothing was sent", withdrawal.ID, withdrawal.Status)
		}
		return tx, true, nil
	}
	tx, findErr := s.reconcileTransactions.FindByID(ctx, id)
	if findErr != nil || tx == nil {
		return nil, false, fmt.Errorf("no withdrawal or transaction %s: %w", id, errors.Join(models.ErrRepositoryNotFound, err, findErr))
	}
	if !isOutboundTxType(tx.TxType) {
		return nil, false, fmt.Errorf("transaction %s is a %s, not an outbound transfer", tx.ID, tx.TxType)
	}
	return tx, false, nil
}

// judge reads what the chain proves about tx into report; record keeps what a
// proof needs between two looks (a first sight of a Solana expiry).
func (s *Service) judge(ctx context.Context, adapter types.Chain, tx models.Transaction, report *WithdrawalReconcileReport, record bool) {
	block, err := searchTransactionBlock(ctx, adapter, tx.TxHash)
	var onChain *chain.TransactionFailedError
	switch {
	case errors.As(err, &onChain):
		report.Verdict, report.Block, report.FailureCode = ReconcileFailed, onChain.Block, onChainFailureCode(onChain.Result)
		report.Reason = "mined with a failed receipt"
		return
	case err != nil:
		report.Verdict, report.Reason = ReconcileUnknown, "the transaction could not be read: "+chain.ClientText(err)
		return
	case block > 0:
		report.Verdict, report.Block, report.Reason = ReconcileLanded, block, "an endpoint has its receipt"
		return
	}
	if reader, ok := adapter.(chain.NonceSpendReader); ok {
		if proof, ok := nonceProofOf(tx); ok {
			spend, spendErr := reader.NonceSpend(ctx, tx.TxHash, tx.FromAddress, proof.nonce, proof.head)
			switch {
			case spendErr != nil:
				report.Verdict, report.Reason = ReconcileUnknown, "its nonce spend cannot be proven: "+spendErr.Error()
				return
			case spend.Spent && !spend.Ours:
				report.Verdict, report.Block, report.FailureCode = ReconcileFailed, spend.Block, models.WithdrawalFailureNotLanded
				report.Reason = fmt.Sprintf("another transaction of the sender spent nonce %d in block %d", proof.nonce, spend.Block)
				return
			case spend.Spent && spend.Result != "":
				report.Verdict, report.Block, report.FailureCode = ReconcileFailed, spend.Block, onChainFailureCode(spend.Result)
				report.Reason = fmt.Sprintf("it spent nonce %d in block %d with a failed receipt", proof.nonce, spend.Block)
				return
			case spend.Spent:
				report.Verdict, report.Block = ReconcileLanded, spend.Block
				report.Reason = fmt.Sprintf("it spent nonce %d in block %d", proof.nonce, spend.Block)
				return
			}
		}
	}
	if reader, ok := adapter.(chain.InputsSpendReader); ok {
		if inputs, ok := spentInputsOf(tx); ok {
			judgeInputsSpend(ctx, reader, tx, inputs, report)
			return
		}
	}
	expired, err := s.expiryOf(ctx, adapter, tx, false, record)
	switch {
	case err != nil:
		report.Verdict, report.Reason = ReconcileUnknown, "its expiry cannot be checked: "+chain.ClientText(err)
	case expired:
		report.Verdict, report.FailureCode = ReconcileFailed, models.WithdrawalFailureExpired
		report.Reason = "past its last valid block and unknown to every endpoint"
	default:
		report.Verdict, report.Reason = ReconcileUnknown, "no endpoint has the transaction and nothing proves yet that it cannot land"
	}
}

// judgeInputsSpend reads who spent the inputs of a Bitcoin-family transfer into
// report: every one spent by other transactions paying someone else, at the
// double-spend depth, fails it not_landed; spent by itself or by a copy paying the
// same in a block, it landed.
func judgeInputsSpend(ctx context.Context, reader chain.InputsSpendReader, tx models.Transaction, inputs []string, report *WithdrawalReconcileReport) {
	spend, err := reader.InputsSpend(ctx, inputsQueryOf(tx, inputs))
	switch {
	case err != nil:
		report.Verdict, report.Reason = ReconcileUnknown, "who spent its inputs cannot be read: "+chain.ClientText(err)
	case spend.Conflicted:
		report.Verdict, report.Block, report.FailureCode = ReconcileFailed, spend.Block, models.WithdrawalFailureNotLanded
		report.Reason = fmt.Sprintf("other transactions paying someone else spent all %d of its inputs, the last in block %d", len(inputs), spend.Block)
	case spend.Ours && spend.Block > 0 && spend.TxHash != "" && !strings.EqualFold(spend.TxHash, tx.TxHash):
		report.Verdict, report.Block = ReconcileLanded, spend.Block
		report.Reason = fmt.Sprintf("a copy paying the same (%s) spent its inputs in block %d", spend.TxHash, spend.Block)
	case spend.Ours && spend.Block > 0:
		report.Verdict, report.Block = ReconcileLanded, spend.Block
		report.Reason = fmt.Sprintf("it spent its inputs in block %d", spend.Block)
	case spend.Ours:
		report.Verdict, report.Reason = ReconcileUnknown, "it spent its inputs in the mempool, not in a block yet"
	default:
		report.Verdict, report.Reason = ReconcileUnknown, "no endpoint has the transaction and its inputs are not all spent by another one at the confirmation depth"
	}
}

// applyReconciliation records a landed or failed verdict.
func (s *Service) applyReconciliation(ctx context.Context, adapter types.Chain, tx models.Transaction, report *WithdrawalReconcileReport) {
	switch report.Verdict {
	case ReconcileLanded:
		if err := s.txRepo.SetBlockNumber(ctx, tx.ID, report.Block); err != nil {
			report.Reason += "; the block could not be recorded: " + err.Error()
			return
		}
		report.Applied = true
	case ReconcileFailed:
		block := report.Block
		if report.FailureCode == models.WithdrawalFailureNotLanded || report.FailureCode == models.WithdrawalFailureExpired {
			block = 0
		}
		report.Applied = s.failOnChain(ctx, adapter, tx, report.FailureCode, block)
		if !report.Applied {
			report.Reason += "; the failure could not be recorded (see the log)"
		}
	}
}

// searchTransactionBlock is the transaction's block from every endpoint when the
// adapter can ask them all, else from the failover order.
func searchTransactionBlock(ctx context.Context, adapter types.Chain, txHash string) (uint64, error) {
	if searcher, ok := adapter.(chain.TransactionSearcher); ok {
		return searcher.SearchTransactionBlock(ctx, txHash)
	}
	return adapter.GetTransactionBlock(ctx, txHash)
}

// nonceProofOf reads the sender nonce of any EVM row, and the head before its send
// when it was kept (0: searched from the chain's first block).
func nonceProofOf(tx models.Transaction) (nonceProof, bool) {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tx.RawPayload), &payload); err != nil {
		return nonceProof{}, false
	}
	var proof nonceProof
	if json.Unmarshal(payload[chain.SenderNoncePayloadKey], &proof.nonce) != nil {
		return nonceProof{}, false
	}
	_ = json.Unmarshal(payload[chain.BroadcastHeadPayloadKey], &proof.head)
	return proof, true
}
