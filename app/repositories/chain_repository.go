package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// ChainRepository persists chain records.
type ChainRepository struct {
	db.Base
}

// NewChainRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewChainRepository(query orm.Query) *ChainRepository {
	return &ChainRepository{Base: db.NewBase(query)}
}

// FindAll returns every chain ordered by display_order.
func (r *ChainRepository) FindAll(ctx context.Context) ([]models.Chain, error) {
	var chains []models.Chain
	if err := r.Query(ctx).Order("display_order ASC").Find(&chains); err != nil {
		return nil, fmt.Errorf("list chains: %w", err)
	}
	return chains, nil
}

// FindActive returns active chains ordered by display_order.
func (r *ChainRepository) FindActive(ctx context.Context) ([]models.Chain, error) {
	var chains []models.Chain
	if err := r.Query(ctx).Where("status", "active").Order("display_order ASC").Find(&chains); err != nil {
		return nil, fmt.Errorf("list active chains: %w", err)
	}
	return chains, nil
}

// FindByID returns the chain, or ErrRepositoryNotFound.
func (r *ChainRepository) FindByID(ctx context.Context, id string) (*models.Chain, error) {
	if id == "" {
		return nil, models.ErrRepositoryNotFound
	}
	var chain models.Chain
	if err := r.Query(ctx).Where("id", id).FirstOrFail(&chain); err != nil {
		return nil, db.LookupError(err, "find chain")
	}
	return &chain, nil
}

// FindByTestnet returns active chains for the requested network kind, ordered by display_order.
func (r *ChainRepository) FindByTestnet(ctx context.Context, isTestnet bool) ([]models.Chain, error) {
	var chains []models.Chain
	if err := r.Query(ctx).Where("status", "active").Where("is_testnet", isTestnet).Order("display_order ASC").Find(&chains); err != nil {
		return nil, fmt.Errorf("list chains by testnet: %w", err)
	}
	return chains, nil
}

// UpdateThresholds writes only the threshold columns whose pointers are set.
// A nil pointer leaves that column as it is. The statement matches one chain.
func (r *ChainRepository) UpdateThresholds(ctx context.Context, id string, write models.ChainThresholdWrite) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("update chain thresholds: chain id is required")
	}
	assignments := make([]string, 0, 4)
	args := make([]any, 0, 4)
	if write.GasReadinessThresholdRaw != nil {
		assignments = append(assignments, "gas_readiness_threshold_raw = ?")
		args = append(args, *write.GasReadinessThresholdRaw)
	}
	if write.DustThresholdNativeRaw != nil {
		assignments = append(assignments, "dust_threshold_native_raw = ?")
		args = append(args, *write.DustThresholdNativeRaw)
	}
	if write.DustThresholdUSD != nil {
		assignments = append(assignments, "dust_threshold_usd = ?")
		args = append(args, *write.DustThresholdUSD)
	}
	if len(assignments) == 0 {
		return nil
	}
	assignments = append(assignments, "updated_at = NOW()")
	args = append(args, id)
	result, err := r.Query(ctx).Exec(
		"UPDATE chains SET "+strings.Join(assignments, ", ")+" WHERE id = ?",
		args...,
	)
	if err != nil {
		return fmt.Errorf("update chain thresholds: %w", err)
	}
	if err := db.RequireRow(result); err != nil {
		return err
	}
	return nil
}

// UpdateRPCURL writes the sealed rpc_url of one chain. An empty sealed value
// is refused so a bad call cannot wipe the current endpoint. The value is
// not included in the error.
func (r *ChainRepository) UpdateRPCURL(ctx context.Context, id, sealed string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return fmt.Errorf("update chain rpc: chain id is required")
	}
	if strings.TrimSpace(sealed) == "" {
		return fmt.Errorf("update chain rpc: sealed endpoint is required")
	}
	result, err := r.Query(ctx).Exec(
		"UPDATE chains SET rpc_url = ?, updated_at = NOW() WHERE id = ?",
		sealed, id,
	)
	if err != nil {
		return fmt.Errorf("update chain rpc: %w", err)
	}
	if err := db.RequireRow(result); err != nil {
		return err
	}
	return nil
}

// Create inserts a chain.
func (r *ChainRepository) Create(ctx context.Context, chain *models.Chain) error {
	if chain == nil {
		return fmt.Errorf("create chain: chain is nil")
	}
	if err := r.Query(ctx).Create(chain); err != nil {
		return fmt.Errorf("create chain: %w", err)
	}
	return nil
}
