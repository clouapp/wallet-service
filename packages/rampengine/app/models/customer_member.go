package models

import "github.com/goravel/framework/database/orm"

// CustomerMemberMorphType is the subject_type a membership's roles are
// assigned under in the RBAC pivots. It is not an identity.SubjectType: those
// feed the OTP and MFA checks, and a membership never authenticates.
const CustomerMemberMorphType = "customer_members"

// CustomerMember links a user to a customer they act for. Its roles — today
// only owner — are assigned to the membership, not to the user, so what a user
// may do is always relative to one customer.
type CustomerMember struct {
	orm.Model
	CustomerID uint      `gorm:"not null" json:"customer_id"`
	UserID     uint      `gorm:"not null" json:"user_id"`
	Customer   *Customer `gorm:"foreignKey:CustomerID" json:"-"`
	// User is who the membership lets act; the admin loads it with deleted
	// accounts, which still are who held the money.
	User *User `gorm:"foreignKey:UserID" json:"-"`
}

// SubjectID is the membership's id: roles are held by it, not by the user.
func (m *CustomerMember) SubjectID() uint { return m.ID }

// SubjectType is CustomerMemberMorphType.
func (m *CustomerMember) SubjectType() string { return CustomerMemberMorphType }
