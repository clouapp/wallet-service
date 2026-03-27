package models

import (
	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

type User struct {
	orm.Model
	ID               uuid.UUID  `gorm:"type:uuid;primary_key" json:"id"`
	Email            string     `gorm:"type:varchar(255);uniqueIndex;not null" json:"email"`
	PasswordHash     string     `gorm:"type:text;not null" json:"-"`
	FullName         string     `gorm:"type:varchar(255)" json:"full_name,omitempty"`
	TotpSecret       string     `gorm:"type:text" json:"-"` // encrypted with facades.Crypt()
	TotpEnabled      bool       `gorm:"default:false" json:"totp_enabled"`
	Status           string     `gorm:"type:varchar(20);default:active" json:"status"`
	DefaultAccountID *uuid.UUID `gorm:"type:uuid" json:"default_account_id,omitempty"`
}

func (u *User) TableName() string { return "users" }
