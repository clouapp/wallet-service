package deposit

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/services/chain"
	"github.com/macrowallets/waas/pkg/types"
)

// accountRentBackfillStore lists the confirmed outbound rows whose account rent was
// never recorded (the transaction repository).
type accountRentBackfillStore interface {
	FindConfirmedOutboundWithoutAccountRent(ctx context.Context, chainID string, limit int) ([]models.Transaction, error)
}

// recordAccountRent keeps in raw_payload (chain.AccountRentPayloadKey) what an
// outbound transfer paid, beside its fee, for the accounts it created, when the
// adapter can tell (chain.AccountRentReader: Solana's associated token accounts). A
// read or a write that fails is logged and confirms the transfer all the same:
// transactions:backfill-fees records it later. It returns the row as written.
func (s *Service) recordAccountRent(ctx context.Context, adapter types.Chain, tx models.Transaction) models.Transaction {
	reader, ok := adapter.(chain.AccountRentReader)
	if !ok || tx.TxHash == "" || hasPayloadKey(tx, chain.AccountRentPayloadKey) {
		return tx
	}
	rent, err := reader.TransactionAccountRent(ctx, tx.TxHash)
	if err != nil {
		slog.Warn("read account rent", "tx_id", tx.ID, "tx_hash", tx.TxHash, "chain", tx.Chain, "error", err)
		return tx
	}
	raw, err := withPayloadValue(tx.RawPayload, chain.AccountRentPayloadKey, rent.String())
	if err == nil {
		err = s.txRepo.SetRawPayload(ctx, tx.ID, raw)
	}
	if err != nil {
		slog.Error("record account rent", "tx_id", tx.ID, "error", err)
		return tx
	}
	tx.RawPayload = raw
	return tx
}

// backfillAccountRent reads and, with apply, records the account rent of the
// confirmed outbound rows of chainID that have none.
func (s *Service) backfillAccountRent(ctx context.Context, chainID string, reader chain.AccountRentReader, apply bool, report *FeeBackfillReport) error {
	store, ok := s.txRepo.(accountRentBackfillStore)
	if !ok {
		return fmt.Errorf("account rent backfill of %s: the store cannot list the rows", chainID)
	}
	rows, err := store.FindConfirmedOutboundWithoutAccountRent(ctx, chainID, feeBackfillMaxRows)
	if err != nil {
		return fmt.Errorf("account rent backfill of %s: %w", chainID, err)
	}
	for _, tx := range rows {
		row := FeeBackfillRow{Chain: chainID, TxType: tx.TxType, TxHash: tx.TxHash, Rent: true}
		rent, err := reader.TransactionAccountRent(ctx, tx.TxHash)
		if err != nil {
			row.Err = err
			report.Rows = append(report.Rows, row)
			continue
		}
		row.AccountRent = rent.String()
		if apply {
			raw, err := withPayloadValue(tx.RawPayload, chain.AccountRentPayloadKey, row.AccountRent)
			if err != nil {
				return fmt.Errorf("account rent of %s: %w", tx.ID, err)
			}
			if err := s.txRepo.SetRawPayload(ctx, tx.ID, raw); err != nil {
				return fmt.Errorf("write account rent of %s: %w", tx.ID, err)
			}
			report.Written++
		}
		report.Rows = append(report.Rows, row)
	}
	return nil
}

// hasPayloadKey reports whether the row's raw_payload holds key.
func hasPayloadKey(tx models.Transaction, key string) bool {
	var payload map[string]json.RawMessage
	if json.Unmarshal([]byte(tx.RawPayload), &payload) != nil {
		return false
	}
	_, found := payload[key]
	return found
}

// withPayloadValue is raw (a JSON object; empty or unreadable is {}) with key set to
// value; the other keys keep their bytes.
func withPayloadValue(raw, key string, value any) (string, error) {
	payload := map[string]json.RawMessage{}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &payload)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	payload[key] = encoded
	out, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
