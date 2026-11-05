package policies

const (
	PermRampCustomersRead   = "ramp_customers.read"
	PermRampCustomersWrite  = "ramp_customers.write"
	PermRampProfileRead     = "ramp_compliance.profile.read"
	PermRampComplianceRead  = "ramp_compliance.read"
	PermRampComplianceWrite = "ramp_compliance.write"
	PermRampOrdersRead      = "ramp_orders.read"
	PermRampOrdersCreate    = "ramp_orders.create"
)

func RampPermissions() []string {
	return []string{PermRampProfileRead, PermRampCustomersRead, PermRampCustomersWrite, PermRampComplianceRead, PermRampComplianceWrite, PermRampOrdersRead, PermRampOrdersCreate}
}

// MayOperateRamps keeps verification writes and fund movements with account
// owners/admins. An auditor can inspect; ordinary wallet users cannot inspect
// other people's identity documents through the new directory.
func MayOperateRamps(role, permission string) bool {
	if role == roleOwner || role == roleAdmin {
		return true
	}
	return role == roleAuditor && (permission == PermRampCustomersRead || permission == PermRampComplianceRead || permission == PermRampOrdersRead)
}
