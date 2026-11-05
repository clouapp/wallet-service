package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

// The states of a payout destination's registration at a provider. A row older
// than the state is DestinationReady: Bridge and Ozav write it once the
// destination exists. A provider that registers the account as an object of
// its own walks the row through the others.
const (
	DestinationReady     = "ready"
	DestinationPending   = "pending"
	DestinationVerifying = "verifying"
	DestinationRejected  = "rejected"
	DestinationFailed    = "failed"
)

// OfframpDestination is a Pix key a provider has registered as a payout
// destination for a customer, and the provider's id for it. The next off-ramp
// to the key has to name that id: Bridge refuses to register one key twice on
// the same customer of its own — which is the customer's, not its member's: a
// person and their company are two customers there.
type OfframpDestination struct {
	orm.Model
	CustomerID uint   `gorm:"not null" json:"customer_id"`
	Provider   string `gorm:"size:32;not null" json:"provider"`
	// PixKeyHash is pixkeys.HashKey of the key — the value user_pix_keys
	// deduplicates on — so the key typed with or without punctuation or +55 is
	// one destination.
	PixKeyHash string `gorm:"size:64;not null" json:"-"`
	// DocumentHash is pixkeys.DestinationDocumentHash: the holder document the
	// key was registered under, or empty for a key that is a document itself.
	DocumentHash   string `gorm:"size:64;not null" json:"-"`
	DestinationRef string `gorm:"size:64;not null;default:''" json:"destination_ref"`

	// Registration state, for a provider that registers the account itself.
	// Status is one of the Destination* constants. Name is the deterministic
	// name the account is found by when an answer was lost; the idempotency
	// key and request hash are those of the attempt in flight (the same bytes
	// are replayed under the same key, within RetryDeadline). ProviderCustomerID
	// is the provider-side customer the account was registered on.
	Status                     string     `gorm:"size:16;not null;default:'ready'" json:"-"`
	Name                       string     `gorm:"size:64;not null;default:''" json:"-"`
	RegistrationIdempotencyKey string     `gorm:"size:255;not null;default:''" json:"-"`
	RegistrationRequestHash    string     `gorm:"size:64;not null;default:''" json:"-"`
	RegistrationAttempt        int        `gorm:"not null;default:1" json:"-"`
	RetryDeadline              *time.Time `json:"-"`
	FailureCode                *string    `gorm:"size:64" json:"-"`
	ProviderCustomerID         string     `gorm:"size:64;not null;default:''" json:"-"`
}
