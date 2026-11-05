package models

import "github.com/goravel/framework/database/orm"

// UserPixKey is a user-registered Pix key for reuse at withdraw time (encrypted at rest; see migration).
type UserPixKey struct {
	orm.Model
	UserID     uint   `gorm:"not null" json:"user_id"`
	Label      string `gorm:"type:text;not null" json:"-"`
	PixKey     string `gorm:"type:text;not null" json:"-"`
	PixKeyHash string `gorm:"size:64;not null" json:"-"`
	PixKeyType string `gorm:"size:16;not null" json:"pix_key_type"`
	// RecipientName is who the Pix key belongs to.
	RecipientName string `gorm:"type:text;not null" json:"-"`
	// RecipientDocument is required for phone/email/evp keys, optional for
	// cpf/cnpj (see pixrules.RecipientDocumentRule).
	RecipientDocument *string `gorm:"type:text" json:"-"`
}
