package repositories

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/facades"

	"github.com/macrowallets/waas/app/models"
)

type AccountInviteRepository interface {
	Create(invite *models.AccountInvite) error
	FindPendingByAccountEmail(accountID uuid.UUID, email string) (*models.AccountInvite, error)
	FindPendingByTokenHash(tokenHash string, now time.Time) (*models.AccountInvite, error)
	Rotate(id uuid.UUID, tokenHash, role string, expiresAt time.Time) error
	MarkAccepted(id uuid.UUID, acceptedAt time.Time) error
}

type accountInviteRepository struct{}

func NewAccountInviteRepository() AccountInviteRepository {
	return &accountInviteRepository{}
}

func (r *accountInviteRepository) Create(invite *models.AccountInvite) error {
	return facades.Orm().Query().Create(invite)
}

func (r *accountInviteRepository) FindPendingByAccountEmail(accountID uuid.UUID, email string) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := facades.Orm().Query().
		Where("account_id = ? AND lower(email) = ? AND accepted_at IS NULL AND revoked_at IS NULL", accountID, strings.ToLower(strings.TrimSpace(email))).
		First(&invite)
	if err != nil {
		return nil, err
	}
	if invite.ID == uuid.Nil {
		return nil, nil
	}
	return &invite, nil
}

func (r *accountInviteRepository) FindPendingByTokenHash(tokenHash string, now time.Time) (*models.AccountInvite, error) {
	var invite models.AccountInvite
	err := facades.Orm().Query().
		Where("token_hash = ? AND accepted_at IS NULL AND revoked_at IS NULL AND expires_at > ?", tokenHash, now).
		First(&invite)
	if err != nil {
		return nil, err
	}
	if invite.ID == uuid.Nil {
		return nil, nil
	}
	return &invite, nil
}

func (r *accountInviteRepository) Rotate(id uuid.UUID, tokenHash, role string, expiresAt time.Time) error {
	_, err := facades.Orm().Query().Exec(
		`UPDATE account_invites SET token_hash = ?, role = ?, expires_at = ?, updated_at = NOW() WHERE id = ?`,
		tokenHash, role, expiresAt, id,
	)
	return err
}

func (r *accountInviteRepository) MarkAccepted(id uuid.UUID, acceptedAt time.Time) error {
	_, err := facades.Orm().Query().Model(&models.AccountInvite{}).Where("id = ?", id).Update("accepted_at", acceptedAt)
	return err
}
