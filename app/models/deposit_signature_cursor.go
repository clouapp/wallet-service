package models

import "time"

// DepositSignatureCursor is where the signature sync of one account of a chain
// resumes (Solana: an owner address or one of its token accounts): the newest
// signature of its history whose deposits were all recorded, and its slot.
// Signature is empty while none was: Slot is then the floor the history is read
// from. SyncedFromSlot is the slot the account's history was first read from (0: its
// whole history), which tells the deposits of which older blocks the sync covers.
type DepositSignatureCursor struct {
	Chain          string    `gorm:"column:chain;primaryKey"`
	Account        string    `gorm:"column:account;primaryKey"`
	Signature      string    `gorm:"column:signature"`
	Slot           int64     `gorm:"column:slot"`
	SyncedFromSlot int64     `gorm:"column:synced_from_slot"`
	CreatedAt      time.Time `gorm:"column:created_at"`
	UpdatedAt      time.Time `gorm:"column:updated_at"`
}

func (DepositSignatureCursor) TableName() string { return "deposit_signature_cursors" }
