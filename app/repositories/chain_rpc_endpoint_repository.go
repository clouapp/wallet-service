package repositories

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// ChainRPCEndpointRepository persists the sealed fallback RPC endpoints of each
// chain (positions 2 and after; position 1 is chains.rpc_url). A list is always
// written whole, so its positions stay 2, 3, ... with no gap.
type ChainRPCEndpointRepository struct {
	db.Base
}

// NewChainRPCEndpointRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewChainRPCEndpointRepository(query orm.Query) *ChainRPCEndpointRepository {
	return &ChainRPCEndpointRepository{Base: db.NewBase(query)}
}

// Within runs fn in one transaction: the chain repository joins it through the
// context, so chains.rpc_url and the fallbacks change together.
func (r *ChainRPCEndpointRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("chain rpc endpoint transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// ListByChain returns the chain's fallbacks, enabled or not, in position order.
// A chain without fallbacks is an empty list.
func (r *ChainRPCEndpointRepository) ListByChain(ctx context.Context, chainID string) ([]models.ChainRPCEndpoint, error) {
	var rows []models.ChainRPCEndpoint
	if err := r.Query(ctx).Where("chain_id", strings.TrimSpace(chainID)).Order("position ASC").Find(&rows); err != nil {
		return nil, fmt.Errorf("list chain rpc endpoints: %w", err)
	}
	return rows, nil
}

// ListEnabled returns the enabled fallbacks of every chain, by chain and position.
func (r *ChainRPCEndpointRepository) ListEnabled(ctx context.Context) ([]models.ChainRPCEndpoint, error) {
	var rows []models.ChainRPCEndpoint
	if err := r.Query(ctx).Where("enabled", true).Order("chain_id ASC").Order("position ASC").Find(&rows); err != nil {
		return nil, fmt.Errorf("list enabled chain rpc endpoints: %w", err)
	}
	return rows, nil
}

// Replace writes endpoints as the whole fallback list of chainID, at their
// positions, in one transaction (joining one already on ctx). A position under 2,
// a repeated position or an unsealed URL is refused by the table and nothing
// changes. CreatedAt is kept when set. The URLs are not in any error.
func (r *ChainRPCEndpointRepository) Replace(ctx context.Context, chainID string, endpoints []models.ChainRPCEndpoint) error {
	chainID = strings.TrimSpace(chainID)
	if chainID == "" {
		return fmt.Errorf("replace chain rpc endpoints: chain id is required")
	}
	return r.Transaction(ctx, func(tx orm.Query) error {
		if _, err := tx.Exec(`DELETE FROM chain_rpc_endpoints WHERE chain_id = ?`, chainID); err != nil {
			return fmt.Errorf("replace chain rpc endpoints: delete: %w", err)
		}
		now := time.Now().UTC()
		for _, endpoint := range endpoints {
			created := endpoint.CreatedAt
			if created.IsZero() {
				created = now
			}
			if _, err := tx.Exec(
				`INSERT INTO chain_rpc_endpoints (chain_id, url, position, enabled, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?)`,
				chainID, endpoint.URL, endpoint.Position, endpoint.Enabled, created, now,
			); err != nil {
				return fmt.Errorf("replace chain rpc endpoints: insert position %d: %w", endpoint.Position, err)
			}
		}
		return nil
	})
}
