package repositories

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
)

const InvalidAmoyUSDTContract = "0xBDE550eCd4C18B3A3C522E1298DC6B1530710B13"

var ErrTokenRetirementStale = errors.New("token retirement preview is stale; refresh before applying")

// RetireInvalidAmoyUSDT changes only the exact previewed active legacy row.
// Locking its physical chain through commit prevents a concurrent network edit;
// the conditional token update rejects concurrent metadata/status changes.
func (r *TokenRepository) RetireInvalidAmoyUSDT(ctx context.Context, id uuid.UUID, chainID, storedContract string) error {
	if ctx == nil || id == uuid.Nil || (chainID != models.ChainPolygon && chainID != models.ChainTPolygon) || !strings.EqualFold(storedContract, InvalidAmoyUSDTContract) {
		return ErrTokenRetirementStale
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		var chain models.Chain
		if readErr := tx.Select("id", "adapter_type", "network_id", "is_testnet").LockForUpdate().Where("id = ?", chainID).FirstOrFail(&chain); readErr != nil {
			return fmt.Errorf("%w: physical chain unavailable: %w", ErrTokenRetirementStale, readErr)
		}
		if chain.ID != chainID || chain.AdapterType != models.AdapterTypeEVM || !chain.IsTestnet || chain.NetworkID == nil || *chain.NetworkID != models.EVMNetworkIDPolygonAmoy {
			return ErrTokenRetirementStale
		}
		result, updateErr := tx.Model(&models.Token{}).Where("id = ? AND chain_id = ? AND symbol = ? AND contract_address = ? AND status = ?", id, chainID, models.SymbolUSDT, storedContract, models.TokenStatusActive).Update("status", models.TokenStatusInactive)
		if updateErr != nil {
			return fmt.Errorf("retire invalid Amoy token: %w", updateErr)
		}
		if result == nil || result.RowsAffected != 1 {
			return ErrTokenRetirementStale
		}
		return nil
	})
}
