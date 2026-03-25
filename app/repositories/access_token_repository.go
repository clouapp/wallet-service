package repositories

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AccessTokenRepository interface {
	Create(token *models.AccessToken) error
	FindByAccountID(accountID uuid.UUID) ([]models.AccessToken, error)
	PaginateByAccountID(accountID uuid.UUID, limit, offset int) ([]models.AccessToken, int64, error)
	FindByIDAndAccount(tokenID, accountID uuid.UUID) (*models.AccessToken, error)
	Delete(token *models.AccessToken) error
}

type accessTokenRepository struct{}

func NewAccessTokenRepository() AccessTokenRepository {
	return &accessTokenRepository{}
}

func (r *accessTokenRepository) Create(token *models.AccessToken) error {
	return facades.Orm().Query().Create(token)
}

func (r *accessTokenRepository) FindByAccountID(accountID uuid.UUID) ([]models.AccessToken, error) {
	var tokens []models.AccessToken
	err := facades.Orm().Query().
		Where("account_id = ?", accountID).
		Find(&tokens)
	return tokens, err
}

func (r *accessTokenRepository) PaginateByAccountID(accountID uuid.UUID, limit, offset int) ([]models.AccessToken, int64, error) {
	var tokens []models.AccessToken
	var total int64
	total, err := facades.Orm().Query().
		Model(&models.AccessToken{}).
		Where("account_id = ?", accountID).
		Count()
	if err != nil {
		return nil, 0, err
	}
	err = facades.Orm().Query().
		Where("account_id = ?", accountID).
		Offset(offset).Limit(limit).
		Find(&tokens)
	return tokens, total, err
}

func (r *accessTokenRepository) FindByIDAndAccount(tokenID, accountID uuid.UUID) (*models.AccessToken, error) {
	var token models.AccessToken
	err := facades.Orm().Query().
		Where("id = ? AND account_id = ?", tokenID, accountID).
		First(&token)
	if err != nil {
		return nil, err
	}
	if token.ID == uuid.Nil {
		return nil, nil
	}
	return &token, nil
}

func (r *accessTokenRepository) Delete(token *models.AccessToken) error {
	_, err := facades.Orm().Query().Delete(token)
	return err
}
