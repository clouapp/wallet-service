// Package txkind names what a stored transaction is for the wallet that owns it.
// Amounts are stored unsigned; clients derive the display sign from Direction.
package txkind

import (
	"strings"

	"github.com/macrowallets/waas/app/models"
)

// Type is the business type a client shows and filters on.
const (
	TypeDeposit       = "deposit"
	TypeWithdrawal    = "withdrawal"
	TypeSweep         = "sweep"
	TypeConsolidation = "consolidation"
	TypeGasFunding    = "gas_funding"
	TypeTransfer      = "transfer"
	TypeFee           = "fee"
	TypeUnknown       = "unknown"
)

// Direction is how the movement affects the wallet: funds in, funds out, or moved
// between the wallet's own addresses.
const (
	DirectionIncoming = "incoming"
	DirectionOutgoing = "outgoing"
	DirectionInternal = "internal"
	DirectionUnknown  = "unknown"
)

// Kind is the display type and direction of one transaction.
type Kind struct {
	Type      string
	Direction string
}

// Classify derives the kind from the stored tx_type, origin and chain direction.
// tx_type decides; the chain direction only resolves generic transfers.
func Classify(txType, origin, chainDirection string) Kind {
	switch normalize(txType) {
	case models.TxTypeDeposit:
		return Kind{Type: TypeDeposit, Direction: DirectionIncoming}
	case models.TxTypeWithdrawal:
		return Kind{Type: TypeWithdrawal, Direction: DirectionOutgoing}
	case models.TxTypeSweep:
		if normalize(origin) == models.TxOriginManualConsolidation {
			return Kind{Type: TypeConsolidation, Direction: DirectionInternal}
		}
		return Kind{Type: TypeSweep, Direction: DirectionInternal}
	case models.TxTypeGasSeed:
		return Kind{Type: TypeGasFunding, Direction: DirectionInternal}
	case models.TxTypeFee:
		return Kind{Type: TypeFee, Direction: DirectionOutgoing}
	case models.TxTypeTransfer, models.TxTypeTokenTransfer:
		return Kind{Type: TypeTransfer, Direction: fromChainDirection(chainDirection)}
	default:
		return Kind{Type: TypeUnknown, Direction: fromChainDirection(chainDirection)}
	}
}

func fromChainDirection(chainDirection string) string {
	switch normalize(chainDirection) {
	case models.TxDirectionInbound:
		return DirectionIncoming
	case models.TxDirectionOutbound:
		return DirectionOutgoing
	case models.TxDirectionSelf:
		return DirectionInternal
	default:
		return DirectionUnknown
	}
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
