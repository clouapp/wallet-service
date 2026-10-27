package withdraw

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/app/models"
)

type refusedRecoveryRows struct {
	memWithdrawalRows
	required bool
	err      error
}

func (r *refusedRecoveryRows) RequiresRefusedRecovery(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return r.required, r.err
}

func TestCreate_RefusedSignedInstructionNeverReentersTheGenericRetryPlanner(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, checkFails := range []bool{false, true} {
			walletID, operationID, txID := uuid.New(), uuid.New(), uuid.New()
			old := &models.Withdrawal{ID: operationID, WalletID: walletID, TransactionID: &txID, Status: models.WithdrawalStatusFailed, Amount: "6.1", DestinationAddress: "0xoriginal"}
			rows := &refusedRecoveryRows{memWithdrawalRows: memWithdrawalRows{byID: map[uuid.UUID]*models.Withdrawal{operationID: old}}, required: true}
			if checkFails {
				rows.err = errors.New("database unavailable")
			}
			input := CreateInput{Wallet: &models.Wallet{ID: walletID}, Amount: "6.1", DestinationAddress: "0xoriginal"}
			if changed {
				input.Amount = "9"
				input.DestinationAddress = "0xchanged"
			}
			svc := &Service{createRows: rows}
			if _, err := svc.persistCreate(context.Background(), input, operationID, operationID.String(), "0", nil); err == nil {
				t.Fatal("allowed a signed refused instruction through ordinary retry")
			}
			if rows.retries != 0 || rows.creates != 0 || rows.withins != 0 || old.Amount != "6.1" || old.DestinationAddress != "0xoriginal" || old.Status != models.WithdrawalStatusFailed {
				t.Fatal("refused recovery check mutated the original withdrawal")
			}
		}
	}
}
