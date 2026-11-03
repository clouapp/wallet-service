package models

import "github.com/goravel/framework/database/orm"

// CustomerAssociatedPerson is someone a business application names: an owner,
// a controller, a signer or a director (spec §3.1). The app names each by
// LocalRef, stable for the life of the draft; the provider's id for the
// person lives in the provider row's KycState, not here.
//
// No SoftDeletes: every application replaces the list, and a person it no
// longer names is removed — at the provider too.
type CustomerAssociatedPerson struct {
	orm.Model
	CustomerID uint   `gorm:"not null;index" json:"customer_id"`
	LocalRef   string `gorm:"size:64;not null" json:"local_ref"`
	// Roles is the JSON array of the roles the person holds
	// (ramp.AssociatedPerson* values).
	Roles string `gorm:"type:jsonb;not null" json:"roles"`
	// OwnershipPercent is an owner's share, two decimals; nil for anyone who
	// owns nothing.
	OwnershipPercent *string `gorm:"type:numeric(5,2)" json:"ownership_percent"`
	// IndividualCustomerID is the person's own customer when they are one: the
	// representative's.
	IndividualCustomerID *uint `json:"individual_customer_id"`
	// Profile is the person's declaration — the KycProfile and the title —
	// JSON-encoded and encrypted at rest with facades.Crypt().
	Profile string `gorm:"type:text;not null" json:"-"`
}

// BusinessPerson is a person a business application names, as the KYB keeps
// them: what the columns hold and what the profile column encrypts.
type BusinessPerson struct {
	// ID is the person's row, set when read back; an application names a
	// person by LocalRef.
	ID       uint
	LocalRef string
	// Roles are the roles the person holds (owner, control, signer,
	// director).
	Roles []string
	// OwnershipPercent is an owner's share as a decimal string, "" for
	// anyone who owns nothing.
	OwnershipPercent string
	// IndividualCustomerID is the person's own customer, the
	// representative's.
	IndividualCustomerID *uint
	// Title is the position of a person with control.
	Title  string
	Person KycProfile
}

// TableName is the spec's (§3.1): the inflector would make the persons people.
func (CustomerAssociatedPerson) TableName() string { return "customer_associated_persons" }
