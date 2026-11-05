package models

import "time"

// AdminAuditLog is one act an admin performed on an end user, or one read of
// their decrypted identity data: who, what, on whom, from where.
//
// Append-only, so it has neither soft deletes nor an updated_at. Metadata is
// jsonb text holding field names, ids, booleans and the operator's own
// suspension reason — never a CPF, phone, name, e-mail or Pix key.
type AdminAuditLog struct {
	ID      uint  `gorm:"primaryKey" json:"id"`
	AdminID *uint `json:"admin_id"`
	// Admin is loaded with its trashed rows included: the trail outlives the
	// operator's account.
	Admin *Admin `gorm:"foreignKey:AdminID" json:"-"`
	// Action is the act in the admin's vocabulary ("user.suspended"), listed
	// once in app/services/admin/audit.
	Action      string    `gorm:"size:64;not null" json:"action"`
	SubjectType *string   `gorm:"size:32" json:"subject_type"`
	SubjectID   *uint     `json:"subject_id"`
	Provider    *string   `gorm:"size:32" json:"provider"`
	Metadata    string    `gorm:"type:jsonb;not null" json:"-"`
	IP          string    `gorm:"column:ip;size:64;not null" json:"ip"`
	CreatedAt   time.Time `gorm:"not null" json:"created_at"`
}
