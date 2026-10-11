package models

import "time"

// DepositScanCheckpoint is where the deposit scan of one chain resumes: the last
// block whose deposits were all recorded or saved as pending. Redis caches it; this
// row is the one that survives a lost cache. A chain without a row was never scanned.
type DepositScanCheckpoint struct {
	Chain       string    `gorm:"column:chain;primaryKey"`
	BlockNumber int64     `gorm:"column:block_number"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (DepositScanCheckpoint) TableName() string { return "deposit_scan_checkpoints" }
