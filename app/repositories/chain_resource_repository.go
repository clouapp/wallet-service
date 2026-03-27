package repositories

import (
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type ChainResourceRepository interface {
	FindByChainID(chainID string) ([]models.ChainResource, error)
	FindByChainAndType(chainID, resourceType string) ([]models.ChainResource, error)
	Create(resource *models.ChainResource) error
}

type chainResourceRepository struct{}

func NewChainResourceRepository() ChainResourceRepository { return &chainResourceRepository{} }

func (r *chainResourceRepository) FindByChainID(chainID string) ([]models.ChainResource, error) {
	var resources []models.ChainResource
	err := facades.Orm().Query().Where("chain_id", chainID).Where("status", "active").Order("display_order ASC").Find(&resources)
	return resources, err
}

func (r *chainResourceRepository) FindByChainAndType(chainID, resourceType string) ([]models.ChainResource, error) {
	var resources []models.ChainResource
	err := facades.Orm().Query().Where("chain_id", chainID).Where("type", resourceType).Where("status", "active").Order("display_order ASC").Find(&resources)
	return resources, err
}

func (r *chainResourceRepository) Create(resource *models.ChainResource) error {
	return facades.Orm().Query().Create(resource)
}
