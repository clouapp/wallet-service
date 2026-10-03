package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/goravel/framework/database/orm"
)

const (
	// MembershipStatusActive is a member who may pass the account membership check.
	MembershipStatusActive = "active"
	// MembershipStatusSuspended is a member who stays on the account but fails that check.
	MembershipStatusSuspended = "suspended"
)

// MembershipGrantsAccess reports whether a stored membership status may use the
// account. An empty status is the historical row written before status was set;
// the column default is active. Anything else, including suspended, is refused.
func MembershipGrantsAccess(status string) bool {
	switch status {
	case "", MembershipStatusActive:
		return true
	default:
		return false
	}
}

type AccountUser struct {
	orm.Model
	ID        uuid.UUID  `gorm:"type:uuid;primary_key"`
	AccountID uuid.UUID  `gorm:"type:uuid;not null;index"`
	UserID    uuid.UUID  `gorm:"type:uuid;not null;index"`
	Role      string     `gorm:"type:varchar(20);not null"` // owner|admin|auditor|user
	Status    string     `gorm:"type:varchar(20);default:active"`
	AddedBy   *uuid.UUID `gorm:"type:uuid"`
	DeletedAt *time.Time `gorm:"index"`
	User      *User      `gorm:"foreignKey:UserID"`
}

func (a *AccountUser) TableName() string { return "account_users" }
