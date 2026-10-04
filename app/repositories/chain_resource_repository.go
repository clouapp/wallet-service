package repositories

import (
	"context"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

// ChainResourceRepository persists explorer, faucet, and docs links for a chain.
type ChainResourceRepository struct {
	db.Base
}

// NewChainResourceRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewChainResourceRepository(query orm.Query) *ChainResourceRepository {
	return &ChainResourceRepository{Base: db.NewBase(query)}
}

// FindByChainID returns the active resources of a chain ordered by display_order.
func (r *ChainResourceRepository) FindByChainID(ctx context.Context, chainID string) ([]models.ChainResource, error) {
	var resources []models.ChainResource
	if err := r.Query(ctx).Where("chain_id", chainID).Where("status", "active").Order("display_order ASC").Find(&resources); err != nil {
		return nil, fmt.Errorf("list chain resources: %w", err)
	}
	return resources, nil
}

// FindByChainAndType returns the active resources of one type on a chain.
func (r *ChainResourceRepository) FindByChainAndType(ctx context.Context, chainID, resourceType string) ([]models.ChainResource, error) {
	var resources []models.ChainResource
	if err := r.Query(ctx).Where("chain_id", chainID).Where("type", resourceType).Where("status", "active").Order("display_order ASC").Find(&resources); err != nil {
		return nil, fmt.Errorf("list chain resources by type: %w", err)
	}
	return resources, nil
}

// Create inserts a chain resource.
func (r *ChainResourceRepository) Create(ctx context.Context, resource *models.ChainResource) error {
	if resource == nil {
		return fmt.Errorf("create chain resource: resource is nil")
	}
	if err := r.Query(ctx).Create(resource); err != nil {
		return fmt.Errorf("create chain resource: %w", err)
	}
	return nil
}
