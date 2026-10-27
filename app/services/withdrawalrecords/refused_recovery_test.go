package withdrawalrecords

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

func TestRefusedRecoveryGuard_RequiresExplicitRecoveryForAnEVMNonceZero(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		from    string
		missing bool
		wanted  bool
		wantErr bool
	}{
		{name: "nonce zero", payload: `{"broadcast_outcome":"refused","sender_nonce":0}`, wanted: true},
		{name: "nonzero nonce", payload: `{"broadcast_outcome":"refused","sender_nonce":42}`, wanted: true},
		{name: "missing nonce", payload: `{"broadcast_outcome":"refused"}`, wantErr: true},
		{name: "invalid nonce", payload: `{"broadcast_outcome":"refused","sender_nonce":null}`, wantErr: true},
		{name: "malformed evidence", payload: `{`, wantErr: true},
		{name: "missing linked transaction", missing: true, wantErr: true},
		{name: "other chain unchanged", from: "rXRPsender", payload: `{"broadcast_outcome":"refused","sender_nonce":1}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wallet, operation, txID := uuid.New(), uuid.New(), uuid.New()
			row := &models.Withdrawal{ID: operation, WalletID: wallet, TransactionID: &txID, Status: models.WithdrawalStatusFailed}
			tx := &models.Transaction{ID: txID, WalletID: wallet, TxType: models.TxTypeWithdrawal, Status: "failed", ErrorMessage: "broadcast_refused", RawPayload: tc.payload, FromAddress: "0x1111111111111111111111111111111111111111", ToAddress: "0x2222222222222222222222222222222222222222"}
			if tc.from != "" {
				tx.FromAddress = tc.from
			}
			if tc.missing {
				tx = nil
			}
			records := NewRecords(Deps{Store: &lookupStore{row: row}, Transactions: lookupTransactions{tx: tx}})
			got, err := records.RequiresRefusedRecovery(context.Background(), operation, wallet)
			if got != tc.wanted || (err != nil) != tc.wantErr {
				t.Fatalf("required=%v err=%v", got, err)
			}
			if _, err := records.RequiresRefusedRecovery(context.Background(), operation, uuid.New()); !errors.Is(err, ErrNotFound) {
				t.Fatalf("foreign wallet: %v", err)
			}
		})
	}
}
