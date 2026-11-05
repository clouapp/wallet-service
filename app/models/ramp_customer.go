package models

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

const (
	RampAccountHolder = "account_holder"
	RampEndCustomer   = "end_customer"
)

// RampCustomer is the tenant boundary for the ported fiat engine. Engine
// numeric contact IDs never authenticate a caller and are not public IDs.
type RampCustomer struct {
	ID               uuid.UUID `gorm:"type:uuid;primaryKey"`
	AccountID        uuid.UUID `gorm:"type:uuid;not null;index"`
	ExternalID       string    `gorm:"size:255;not null"`
	Relationship     string    `gorm:"size:24;not null"`
	EntityType       string    `gorm:"size:16;not null"`
	Name             string    `gorm:"size:255;not null"`
	EngineCustomerID uint      `gorm:"not null;uniqueIndex"`
	EngineContactID  uint      `gorm:"not null;uniqueIndex"`
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

func (*RampCustomer) TableName() string { return "ramp_customers" }

// RampCustomerDraft contains the holder's own contact data. Representative
// email is separate from a company's email, as the provider KYB requires.
type RampCustomerDraft struct {
	ExternalID, Relationship, EntityType, Name, Email, RepresentativeEmail string
	TaxID, TaxIDType, IncorporationCountry                                 string
}

var ErrRampCustomerNotFound = errors.New("ramp customer not found")
var ErrRampCustomerConflict = errors.New("ramp customer already exists with different data")

type RampFeature struct {
	Feature string
	Enabled bool
}
type RampTask struct {
	ID            uuid.UUID
	AccountID     *uuid.UUID
	Kind, Payload string
	Attempts      int
}
type RampOutboxEvent struct {
	ID, AccountID, CustomerID uuid.UUID
	EventType, Payload        string
	CreatedAt                 time.Time
}
type RampReconciliationCustomer struct {
	ID, AccountID    uuid.UUID
	EngineCustomerID uint
	Environment      string
}
