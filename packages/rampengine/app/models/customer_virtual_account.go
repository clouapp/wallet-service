package models

import (
	"time"

	"github.com/goravel/framework/database/orm"
)

const (
	VirtualAccountStatusActive       = "active"
	VirtualAccountStatusInactive     = "inactive"
	VirtualAccountStatusProvisioning = "provisioning"
)

// CustomerVirtualAccount is a customer's standing deposit account at one
// provider: a permanent, amount-less payment target funded whenever its holder
// likes.
//
// It inverts the order lifecycle. A transfer-shaped on-ramp creates an order and
// then collects for it; here the account exists first and each deposit into it
// creates its own transaction, so this row is closer to a profile field than to
// a checkout.
//
// Both uniques on the table are partial. `(provider, external_account_id)` is
// how the deposit webhook resolves the account, and
// `(customer_id, provider, source_currency)` is restricted to live rows so it
// mirrors the provider's own one-per-customer-per-currency rule while leaving
// deactivate-and-reprovision available. Both exclude soft-deleted rows for the
// reason `CustomerProviderIntegration` documents: a plain Postgres unique ignores
// deleted_at and would block the pair forever.
type CustomerVirtualAccount struct {
	orm.Model
	orm.SoftDeletes
	// CustomerID is who holds the money that arrives here.
	CustomerID uint `gorm:"not null;index" json:"customer_id"`
	// UserID is the member who provisioned the account: a deposit webhook
	// records them as the order's actor (spec §18.6). For a person it is
	// their customer's own id.
	UserID   uint   `gorm:"not null;index" json:"user_id"`
	Provider string `gorm:"size:32;not null;index" json:"provider"`
	// ExternalAccountID is the provider's id for the account. It never reaches
	// the client: which provider serves a rail is an XIP routing decision, not
	// part of the API contract.
	ExternalAccountID string `gorm:"size:64;not null" json:"external_account_id"`
	// SourceCurrency is the fiat collected, lowercase (`brl`).
	SourceCurrency      string `gorm:"size:8;not null" json:"source_currency"`
	Status              string `gorm:"size:16;not null" json:"status"`
	DestinationCurrency string `gorm:"size:16;not null" json:"destination_currency"`
	DestinationChain    string `gorm:"size:32;not null" json:"destination_chain"`
	DestinationAddress  string `gorm:"size:64;not null" json:"destination_address"`
	// FeePercent is XIP's cut, mirrored from the rail setting onto the account.
	// Unlike a transfer's, it is a property of the account and applies to every
	// deposit until the account is updated. Nullable because "no fee configured"
	// is not the same statement as "zero percent".
	FeePercent *string `gorm:"type:numeric(5,2)" json:"fee_percent,omitempty"`
	// DepositInstructions is what the user pays into — for BRL/PIX the br_code
	// and the account holder name — as jsonb-compatible text. It is permanent
	// and carries no amount, so it must be shown as a saved payment target.
	DepositInstructions string `gorm:"type:jsonb;not null" json:"-"`
	// RawPayload is stored as string (not []byte) so pgx encodes it with the
	// text OID and Postgres casts text → jsonb. A raw []byte makes pgx use the
	// bytea OID, which Postgres refuses to cast, and the insert fails at
	// runtime — the failure `Transaction` documents.
	RawPayload string `gorm:"type:jsonb;not null" json:"-"`
	// LastSyncedAt is when a provider read last changed this row —
	// `ramps:sync-virtual-accounts` or the account webhook. It is stamped on a
	// write, not on a comparison: the sweep writes only when something diverged,
	// so an account matching the provider costs no UPDATE and this timestamp
	// stays where the last real change left it. Nil means nothing has moved since
	// provisioning.
	LastSyncedAt *time.Time `json:"last_synced_at,omitempty"`
	User         *User      `gorm:"foreignKey:UserID" json:"user,omitempty"`
}

// IsActive reports whether the account still collects. A deactivated account is
// history: funds sent to it are returned to the sender, so it must never be
// handed back as "your deposit account".
func (a *CustomerVirtualAccount) IsActive() bool { return a.Status == VirtualAccountStatusActive }
