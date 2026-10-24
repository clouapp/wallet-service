package models

import "time"

// DepositSignatureCursor is how far the signature sync of one account of a chain got
// (Solana: an owner address or one of its token accounts). The account's processed
// signatures are one contiguous stretch of its history, newest first: from Signature
// (the newest whose transfers were all recorded, at Slot) down to BackfillBefore (the
// oldest one). New signatures are read above Signature; the backfill goes on below
// BackfillBefore down to SyncedFromSlot, the floor fixed when the account was first
// read, and Covered says it got there: every signature from SyncedFromSlot up to
// Signature is recorded. While Signature is empty (nothing recorded yet), Slot is the
// slot the account is known empty up to.
type DepositSignatureCursor struct {
	Chain          string    `gorm:"column:chain;primaryKey"`
	Account        string    `gorm:"column:account;primaryKey"`
	Signature      string    `gorm:"column:signature"`
	Slot           int64     `gorm:"column:slot"`
	BackfillBefore string    `gorm:"column:backfill_before"`
	SyncedFromSlot int64     `gorm:"column:synced_from_slot"`
	Covered        bool      `gorm:"column:covered"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (DepositSignatureCursor) TableName() string { return "deposit_signature_cursors" }
