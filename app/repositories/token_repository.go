package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type TokenRepository interface {
	FindByChainID(chainID string) ([]models.Token, error)
	FindActive() ([]models.Token, error)
	FindByID(id uuid.UUID) (*models.Token, error)
	Create(token *models.Token) error
}

type tokenRepository struct{}

func NewTokenRepository() TokenRepository { return &tokenRepository{} }

func (r *tokenRepository) FindByChainID(chainID string) ([]models.Token, error) {
	var tokens []models.Token
	err := facades.Orm().Query().Where("chain_id", chainID).Where("status", "active").Find(&tokens)
	return tokens, err
}

func (r *tokenRepository) FindActive() ([]models.Token, error) {
	var tokens []models.Token
	err := facades.Orm().Query().Where("status", "active").Find(&tokens)
	return tokens, err
}

func (r *tokenRepository) FindByID(id uuid.UUID) (*models.Token, error) {
	var token models.Token
	err := facades.Orm().Query().Where("id", id).First(&token)
	if token.ID == uuid.Nil {
		return nil, err
	}
	return &token, err
}

func (r *tokenRepository) Create(token *models.Token) error {
	return facades.Orm().Query().Create(token)
}
