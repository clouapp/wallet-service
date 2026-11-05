package repositories

import (
	"errors"
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrDuplicatePixKey is returned by Create or Update when the user already
// has a Pix key registered with the same normalized value.
var ErrDuplicatePixKey = errors.New("duplicate pix key")

// ErrPixKeyNotFound is returned by Update/Delete when the id does not exist
// or does not belong to the given user — the two cases are indistinguishable
// on purpose, so a caller always maps this to 404, never 403.
var ErrPixKeyNotFound = errors.New("pix key not found")

// UserPixKeyInput is what a caller supplies to create or update a key.
// PixKeyHash is set by the service layer (see migration for duplicate detection).
type UserPixKeyInput struct {
	Label             string
	PixKey            string
	PixKeyType        string
	RecipientName     string
	RecipientDocument *string
	PixKeyHash        string
}

// UserPixKeyView is the decrypted shape returned to callers — repositories
// never leak the encrypted row or the hash column.
type UserPixKeyView struct {
	ID                uint
	Label             string
	PixKey            string
	PixKeyType        string
	RecipientName     string
	RecipientDocument *string
}

// UserPixKeyRepository is the persistence boundary for user-registered Pix keys.
type UserPixKeyRepository interface {
	ListByUser(userID uint) ([]UserPixKeyView, error)
	Create(userID uint, input UserPixKeyInput) (*UserPixKeyView, error)
	Update(userID uint, id uint, input UserPixKeyInput) (*UserPixKeyView, error)
	Delete(userID uint, id uint) error
	DeleteByUser(userID uint) error
}

type userPixKeyRepository struct{}

// NewUserPixKeyRepository returns a UserPixKeyRepository backed by the
// default ORM connection.
func NewUserPixKeyRepository() UserPixKeyRepository {
	return &userPixKeyRepository{}
}

func encryptField(value string) (string, error) {
	return facades.Crypt().EncryptString(value)
}

func decryptField(value string) (string, error) {
	return facades.Crypt().DecryptString(value)
}

func encryptOptionalField(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	enc, err := encryptField(*value)
	if err != nil {
		return nil, err
	}
	return &enc, nil
}

func decryptOptionalField(value *string) (*string, error) {
	if value == nil {
		return nil, nil
	}
	dec, err := decryptField(*value)
	if err != nil {
		return nil, err
	}
	return &dec, nil
}

func pixKeyRowToView(row *models.UserPixKey) (*UserPixKeyView, error) {
	label, err := decryptField(row.Label)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: decrypt label: %w", err)
	}
	pixKey, err := decryptField(row.PixKey)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: decrypt pix_key: %w", err)
	}
	recipientName, err := decryptField(row.RecipientName)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: decrypt recipient_name: %w", err)
	}
	recipientDocument, err := decryptOptionalField(row.RecipientDocument)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: decrypt recipient_document: %w", err)
	}
	return &UserPixKeyView{
		ID: row.ID, Label: label, PixKey: pixKey, PixKeyType: row.PixKeyType,
		RecipientName: recipientName, RecipientDocument: recipientDocument,
	}, nil
}

func (r *userPixKeyRepository) toEncryptedRow(userID uint, input UserPixKeyInput) (*models.UserPixKey, error) {
	label, err := encryptField(input.Label)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: encrypt label: %w", err)
	}
	pixKey, err := encryptField(input.PixKey)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: encrypt pix_key: %w", err)
	}
	recipientName, err := encryptField(input.RecipientName)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: encrypt recipient_name: %w", err)
	}
	recipientDocument, err := encryptOptionalField(input.RecipientDocument)
	if err != nil {
		return nil, fmt.Errorf("user pix key repo: encrypt recipient_document: %w", err)
	}
	return &models.UserPixKey{
		UserID: userID, Label: label, PixKey: pixKey,
		PixKeyHash: input.PixKeyHash,
		PixKeyType: input.PixKeyType, RecipientName: recipientName,
		RecipientDocument: recipientDocument,
	}, nil
}

func (r *userPixKeyRepository) findOwned(userID, id uint) (*models.UserPixKey, error) {
	existing, err := first[models.UserPixKey](facades.Orm().Query().Where("user_id", userID).Where("id", id),
		"user pix key repo: find owned")
	if err == nil && existing == nil {
		return nil, ErrPixKeyNotFound
	}
	return existing, err
}

func (r *userPixKeyRepository) ListByUser(userID uint) ([]UserPixKeyView, error) {
	var rows []models.UserPixKey
	if err := facades.Orm().Query().Where("user_id", userID).
		Order("created_at asc").Find(&rows); err != nil {
		return nil, fmt.Errorf("user pix key repo: list: %w", err)
	}
	views := make([]UserPixKeyView, 0, len(rows))
	for i := range rows {
		view, err := pixKeyRowToView(&rows[i])
		if err != nil {
			return nil, err
		}
		views = append(views, *view)
	}
	return views, nil
}

func (r *userPixKeyRepository) Create(userID uint, input UserPixKeyInput) (*UserPixKeyView, error) {
	row, err := r.toEncryptedRow(userID, input)
	if err != nil {
		return nil, err
	}
	if err := facades.Orm().Query().Create(row); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicatePixKey
		}
		return nil, fmt.Errorf("user pix key repo: create: %w", err)
	}
	return pixKeyRowToView(row)
}

func (r *userPixKeyRepository) Update(userID uint, id uint, input UserPixKeyInput) (*UserPixKeyView, error) {
	existing, err := r.findOwned(userID, id)
	if err != nil {
		return nil, err
	}
	updated, err := r.toEncryptedRow(userID, input)
	if err != nil {
		return nil, err
	}
	existing.Label = updated.Label
	existing.PixKey = updated.PixKey
	existing.PixKeyHash = updated.PixKeyHash
	existing.PixKeyType = updated.PixKeyType
	existing.RecipientName = updated.RecipientName
	existing.RecipientDocument = updated.RecipientDocument
	if err := facades.Orm().Query().Save(existing); err != nil {
		if isUniqueViolation(err) {
			return nil, ErrDuplicatePixKey
		}
		return nil, fmt.Errorf("user pix key repo: update: save: %w", err)
	}
	return pixKeyRowToView(existing)
}

func (r *userPixKeyRepository) Delete(userID uint, id uint) error {
	existing, err := r.findOwned(userID, id)
	if err != nil {
		return err
	}
	if _, err := facades.Orm().Query().Delete(existing); err != nil {
		return fmt.Errorf("user pix key repo: delete: %w", err)
	}
	return nil
}

func (r *userPixKeyRepository) DeleteByUser(userID uint) error {
	return r.DeleteByUserTx(facades.Orm().Query(), userID)
}

// DeleteByUserTx is DeleteByUser through q, for the account deletion that
// removes the keys in its own transaction (userRepository.DeleteAudited).
func (r *userPixKeyRepository) DeleteByUserTx(q orm.Query, userID uint) error {
	if _, err := q.Where("user_id", userID).Delete(&models.UserPixKey{}); err != nil {
		return fmt.Errorf("user pix key repo: delete by user: %w", err)
	}
	return nil
}
