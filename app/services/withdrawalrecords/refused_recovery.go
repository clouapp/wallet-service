package withdrawalrecords

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
)

// RequiresRefusedRecovery keeps a signed, refused EVM instruction out of the
// generic retry planner. Its bytes may become valid after gas arrives, so a
// retry must preserve its original sender and nonce through the explicit path.
// The database recovery guard separately closes the claim/update race.
func (s *Records) RequiresRefusedRecovery(ctx context.Context, operationID, walletID uuid.UUID) (bool, error) {
	if err := s.ready(ctx, "check refused withdrawal recovery"); err != nil {
		return false, err
	}
	w, err := s.store.FindByIDAndWallet(ctx, operationID, walletID)
	if err != nil {
		return false, err
	}
	if w == nil || w.ID != operationID || w.WalletID != walletID {
		return false, ErrNotFound
	}
	if w.Status != models.WithdrawalStatusFailed || w.TransactionID == nil {
		return false, nil
	}
	if s.txs == nil {
		return false, fmt.Errorf("check refused withdrawal recovery: transaction reader is required")
	}
	tx, err := s.txs.FindByID(ctx, *w.TransactionID)
	if err != nil {
		return false, err
	}
	if tx == nil || tx.ID != *w.TransactionID || tx.WalletID != walletID {
		return false, fmt.Errorf("check refused withdrawal recovery: linked transaction unavailable")
	}
	if tx.TxType != models.TxTypeWithdrawal || tx.Status != "failed" || tx.ErrorMessage != "broadcast_refused" {
		return false, nil
	}
	if !common.IsHexAddress(tx.FromAddress) || !common.IsHexAddress(tx.ToAddress) {
		return false, nil
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(tx.RawPayload), &payload) != nil {
		return false, fmt.Errorf("check refused withdrawal recovery: invalid broadcast evidence")
	}
	var outcome string
	if json.Unmarshal(payload[chain.BroadcastOutcomePayloadKey], &outcome) != nil {
		return false, fmt.Errorf("check refused withdrawal recovery: missing broadcast outcome")
	}
	// A recorded sender nonce identifies an EVM transfer; zero is not missing.
	if _, present := payload[chain.SenderNoncePayloadKey]; !present {
		return false, fmt.Errorf("check refused withdrawal recovery: missing EVM sender nonce")
	}
	var nonce *uint64
	if json.Unmarshal(payload[chain.SenderNoncePayloadKey], &nonce) != nil || nonce == nil {
		return false, fmt.Errorf("check refused withdrawal recovery: invalid sender nonce")
	}
	return outcome == chain.BroadcastOutcomeRefused, nil
}
