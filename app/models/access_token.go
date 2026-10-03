package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type AccessToken struct {
	orm.Model
	ID            uuid.UUID  `gorm:"type:uuid;primary_key"`
	AccountID     uuid.UUID  `gorm:"type:uuid;not null;index"`
	CreatedBy     *uuid.UUID `gorm:"type:uuid"`
	Name          string     `gorm:"type:varchar(255);not null"`
	TokenHash     string     `gorm:"type:text;not null"`
	Permissions   string     `gorm:"type:text"`
	IpCidr        string     `gorm:"type:text"`
	SpendingLimit string     `gorm:"type:jsonb"`
	ValidUntil    *time.Time `gorm:"type:timestamptz"`
}

func (a *AccessToken) TableName() string { return "access_tokens" }
