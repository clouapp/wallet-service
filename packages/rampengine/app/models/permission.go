package models

import "github.com/goravel/framework/database/orm"

// PermissionMorphType is the canonical model_type string for the `permissions`
// table when referenced from polymorphic pivots.
const PermissionMorphType = "permissions"

// Permission represents a single capability an admin may have, either directly
// or via a Role.
type Permission struct {
	orm.Model
	Name        string `gorm:"not null" json:"name"`
	Guard       string `gorm:"not null;default:admin" json:"guard"`
	Description string `json:"description,omitempty"`
}
