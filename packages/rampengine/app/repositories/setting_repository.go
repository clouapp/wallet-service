package repositories

import (
	"fmt"

	"github.com/goravel/framework/contracts/database/orm"

	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// SettingRepository is the persistence boundary for Setting records.
type SettingRepository interface {
	GetByGroup(group string) ([]models.Setting, error)
	UpsertMany(group string, values map[string]string) error
	// DeleteByGroup removes every stored value of a group for good. Not a soft
	// delete on purpose: (group, key) is unique across trashed rows too, so a
	// later save has to be able to insert the pair again.
	DeleteByGroup(group string) error
}

type settingRepository struct{}

func NewSettingRepository() SettingRepository {
	return &settingRepository{}
}

func (r *settingRepository) GetByGroup(group string) ([]models.Setting, error) {
	var rows []models.Setting
	if err := facades.Orm().Query().Where("group", group).Find(&rows); err != nil {
		return nil, fmt.Errorf("setting repo: get by group: %w", err)
	}
	return rows, nil
}

func (r *settingRepository) UpsertMany(group string, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	return facades.Orm().Transaction(func(tx orm.Query) error {
		for key, value := range values {
			existing, err := first[models.Setting](tx.Where("group", group).Where("key", key), "setting repo: upsert many find")
			if err != nil {
				return err
			}
			if existing != nil {
				existing.Value = value
				if err := tx.Save(existing); err != nil {
					return fmt.Errorf("setting repo: upsert many update: %w", err)
				}
				continue
			}
			row := &models.Setting{Group: group, Key: key, Value: value}
			if err := tx.Create(row); err != nil {
				return fmt.Errorf("setting repo: upsert many create: %w", err)
			}
		}
		return nil
	})
}

func (r *settingRepository) DeleteByGroup(group string) error {
	if _, err := facades.Orm().Query().Where("group", group).ForceDelete(&models.Setting{}); err != nil {
		return fmt.Errorf("setting repo: delete by group: %w", err)
	}
	return nil
}
