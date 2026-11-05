package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// Status of a provider bank account mapping row.
const (
	ProviderAccountPending    = "pending"
	ProviderAccountVerifying  = "verifying"
	ProviderAccountApproved   = "approved"
	ProviderAccountRejected   = "rejected"
	ProviderAccountFailed     = "failed"
	ProviderAccountSuperseded = "superseded"
)

// CustomerBankBeneficiaryProviderAccount maps a beneficiary revision to the
// bank account registered at a provider, per rail (spec §5.5). A superseded
// row is history; there is one live row per (beneficiary, provider, rail).
type CustomerBankBeneficiaryProviderAccount struct {
	orm.Model
	ExternalID                 string     `gorm:"size:36;not null" json:"-"`
	BeneficiaryID              uint       `gorm:"not null" json:"-"`
	BeneficiaryRevision        int        `gorm:"not null" json:"-"`
	Provider                   string     `gorm:"size:32;not null" json:"-"`
	Rail                       string     `gorm:"size:32;not null" json:"-"`
	CustomerExternalID         string     `gorm:"size:64;not null" json:"-"`
	ExternalAccountID          *string    `gorm:"size:64" json:"-"`
	Status                     string     `gorm:"size:16;not null" json:"-"`
	RegistrationIdempotencyKey string     `gorm:"size:255;not null" json:"-"`
	RegistrationRequestHash    string     `gorm:"size:64;not null" json:"-"`
	RegistrationAttempt        int        `gorm:"not null;default:1" json:"-"`
	RegistrationAttemptedAt    *time.Time `json:"-"`
	RetryDeadline              *time.Time `json:"-"`
	LastCheckedAt              *time.Time `json:"-"`
	FailureCode                *string    `gorm:"size:64" json:"-"`
}
