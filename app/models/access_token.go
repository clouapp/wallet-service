package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type AccessToken struct {
	orm.Model
	ID           uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	AccountID    uuid.UUID  `gorm:"type:uuid;not null;index" json:"account_id"`
	CreatedBy    *uuid.UUID `gorm:"type:uuid" json:"created_by,omitempty"`
	Name         string     `gorm:"type:varchar(255);not null" json:"name"`
	Permissions  string     `gorm:"type:text" json:"permissions,omitempty"`
	IpCidr       string     `gorm:"type:text" json:"ip_cidr,omitempty"`
	SpendingLimit string    `gorm:"type:jsonb" json:"spending_limit,omitempty"`
	ValidUntil   *time.Time `json:"valid_until,omitempty"`
}

func (a *AccessToken) TableName() string { return "access_tokens" }
