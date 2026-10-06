package repositories

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	contractsorm "github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/app/models"
	"github.com/macrowallets/waas/app/repositories/internal/db"
)

const (
	maxActivityActionLength = 64
	maxActivityTargetLength = 64
)

// AccountActivityRepository appends and lists account_activity rows.
type AccountActivityRepository struct {
	db.Base
}

// NewAccountActivityRepository wraps an orm.Query. Pass nil for a fresh query per call.
func NewAccountActivityRepository(query contractsorm.Query) *AccountActivityRepository {
	return &AccountActivityRepository{Base: db.NewBase(query)}
}

// Within runs fn inside one transaction. Queries made with the callback
// context join that transaction, including settings and feature writes.
func (r *AccountActivityRepository) Within(ctx context.Context, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("account activity transaction: callback is required")
	}
	return r.Transaction(ctx, func(tx contractsorm.Query) error {
		return fn(db.WithTx(ctx, tx))
	})
}

// Append inserts one row. Metadata is encoded through the allowlist, so a
// secret value cannot be stored under another key. A nil account id is a
// platform row.
func (r *AccountActivityRepository) Append(ctx context.Context, row models.AccountActivity) error {
	if row.ActorUserID == uuid.Nil {
		return fmt.Errorf("append activity: actor is required")
	}
	action := strings.TrimSpace(row.Action)
	targetType := strings.TrimSpace(row.TargetType)
	targetID := strings.TrimSpace(row.TargetID)
	if action == "" || len(action) > maxActivityActionLength {
		return fmt.Errorf("append activity: action is required")
	}
	if targetType == "" || len(targetType) > maxActivityTargetLength {
		return fmt.Errorf("append activity: target type is required")
	}
	if targetID == "" || len(targetID) > maxActivityTargetLength {
		return fmt.Errorf("append activity: target id is required")
	}
	metadata, err := row.Metadata.Encode()
	if err != nil {
		return fmt.Errorf("append activity: %w", err)
	}
	var accountID any
	if row.AccountID != nil {
		if *row.AccountID == uuid.Nil {
			return fmt.Errorf("append activity: account id is empty")
		}
		accountID = *row.AccountID
	}
	id := row.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	_, err = r.Query(ctx).Exec(
		`INSERT INTO account_activity
			(id, account_id, actor_user_id, action, target_type, target_id, metadata, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, CAST(? AS jsonb), NOW())`,
		id, accountID, row.ActorUserID, action, targetType, targetID, metadata,
	)
	if err != nil {
		return fmt.Errorf("append activity: %w", err)
	}
	return nil
}

// List returns one account's rows, newest first. Platform rows (null
// account_id) are not included.
func (r *AccountActivityRepository) List(ctx context.Context, accountID uuid.UUID, limit, offset int) ([]models.AccountActivity, int64, error) {
	if accountID == uuid.Nil {
		return nil, 0, fmt.Errorf("list activity: account id is required")
	}
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list activity: limit and offset are invalid")
	}
	total, err := r.Query(ctx).Model(&models.AccountActivity{}).Where("account_id = ?", accountID).Count()
	if err != nil {
		return nil, 0, fmt.Errorf("list activity: %w", err)
	}
	rows := []models.AccountActivity{}
	err = r.Query(ctx).
		Where("account_id = ?", accountID).
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list activity: %w", err)
	}
	return rows, total, nil
}

// Find returns one row when it belongs to accountID. Another account, a
// platform row (null account_id), and an unknown id are ErrRepositoryNotFound.
func (r *AccountActivityRepository) Find(ctx context.Context, accountID, id uuid.UUID) (*models.AccountActivity, error) {
	if accountID == uuid.Nil {
		return nil, fmt.Errorf("find activity: account id is required")
	}
	if id == uuid.Nil {
		return nil, models.ErrRepositoryNotFound
	}
	var row models.AccountActivity
	if err := r.Query(ctx).Where("id = ? AND account_id = ?", id, accountID).First(&row); err != nil {
		return nil, fmt.Errorf("find activity: %w", err)
	}
	if row.ID == uuid.Nil || row.AccountID == nil || *row.AccountID != accountID {
		return nil, models.ErrRepositoryNotFound
	}
	return &row, nil
}

// ListPlatform returns platform rows, newest first. A row with an account id
// is not included.
func (r *AccountActivityRepository) ListPlatform(ctx context.Context, limit, offset int) ([]models.AccountActivity, int64, error) {
	if limit <= 0 || offset < 0 {
		return nil, 0, fmt.Errorf("list platform activity: limit and offset are invalid")
	}
	total, err := r.Query(ctx).Model(&models.AccountActivity{}).Where("account_id IS NULL").Count()
	if err != nil {
		return nil, 0, fmt.Errorf("list platform activity: %w", err)
	}
	rows := []models.AccountActivity{}
	err = r.Query(ctx).
		Where("account_id IS NULL").
		Order("created_at DESC, id DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows)
	if err != nil {
		return nil, 0, fmt.Errorf("list platform activity: %w", err)
	}
	return rows, total, nil
}

// DeleteOlderThan removes one batch of account_activity rows older than days.
func (r *AccountActivityRepository) DeleteOlderThan(ctx context.Context, days, limit int) (int64, error) {
	if r == nil {
		return 0, fmt.Errorf("prune account_activity: repository is required")
	}
	result, err := r.Query(ctx).Exec(
		`DELETE FROM account_activity WHERE id IN (
			SELECT id FROM account_activity WHERE created_at < NOW() - (? * INTERVAL '1 day')
			ORDER BY created_at LIMIT ?
		)`,
		days, limit,
	)
	if err != nil {
		return 0, fmt.Errorf("prune account_activity: %w", err)
	}
	return result.RowsAffected, nil
}
