package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type AccountUser struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	AccountID uuid.UUID  `gorm:"type:uuid;not null;index" json:"account_id"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"user_id"`
	Role      string     `gorm:"type:varchar(20);not null" json:"role"` // owner|admin|auditor|user
	Status    string     `gorm:"type:varchar(20);default:active" json:"status"`
	AddedBy   *uuid.UUID `gorm:"type:uuid" json:"added_by,omitempty"`
	DeletedAt *time.Time `gorm:"index" json:"deleted_at,omitempty"`
	User      *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

func (a *AccountUser) TableName() string { return "account_users" }
