package sweep

import (
	"context"
	"errors"
	"math/big"

	"github.com/google/uuid"
)

// ErrInvalidAmount is Preview's refusal of an amount that is not a whole number
// of base units.
var ErrInvalidAmount = errors.New("invalid amount")

// PreviewInput is a withdrawal preview as a client wrote it: the amount in base
// units, as text.
type PreviewInput struct {
	WalletID        uuid.UUID
	Asset           string
	Amount          string
	CallerAccountID uuid.UUID
}

// previewHasNoDestination: a preview plans the sweeps without a recipient.
const previewHasNoDestination = ""

// Preview plans the withdrawal of an amount in base units without executing
// anything. An amount that is not a base-10 integer is ErrInvalidAmount.
func Preview(ctx context.Context, sweeps Service, in PreviewInput) (*Plan, error) {
	amount, ok := new(big.Int).SetString(in.Amount, 10)
	if !ok {
		return nil, ErrInvalidAmount
	}
	return sweeps.PlanForWithdrawal(ctx, in.WalletID, in.Asset, amount, previewHasNoDestination, in.CallerAccountID)
}
