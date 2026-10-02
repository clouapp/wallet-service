package repositories

import (
	"context"
	"fmt"

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
	if err := r.Query(ctx).Where("id", id).First(&chain); err != nil {
		return nil, fmt.Errorf("find chain: %w", err)
	}
	if chain.ID == "" {
		return nil, models.ErrRepositoryNotFound
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
