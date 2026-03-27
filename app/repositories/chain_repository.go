package repositories

import (
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type ChainRepository interface {
	FindAll() ([]models.Chain, error)
	FindActive() ([]models.Chain, error)
	FindByID(id string) (*models.Chain, error)
	FindByTestnet(isTestnet bool) ([]models.Chain, error)
	Create(chain *models.Chain) error
}

type chainRepository struct{}

func NewChainRepository() ChainRepository { return &chainRepository{} }

func (r *chainRepository) FindAll() ([]models.Chain, error) {
	var chains []models.Chain
	err := facades.Orm().Query().Order("display_order ASC").Find(&chains)
	return chains, err
}

func (r *chainRepository) FindActive() ([]models.Chain, error) {
	var chains []models.Chain
	err := facades.Orm().Query().Where("status", "active").Order("display_order ASC").Find(&chains)
	return chains, err
}

func (r *chainRepository) FindByID(id string) (*models.Chain, error) {
	var chain models.Chain
	err := facades.Orm().Query().Where("id", id).First(&chain)
	if chain.ID == "" {
		return nil, err
	}
	return &chain, err
}

func (r *chainRepository) FindByTestnet(isTestnet bool) ([]models.Chain, error) {
	var chains []models.Chain
	err := facades.Orm().Query().Where("status", "active").Where("is_testnet", isTestnet).Order("display_order ASC").Find(&chains)
	return chains, err
}

func (r *chainRepository) Create(chain *models.Chain) error {
	return facades.Orm().Query().Create(chain)
}
