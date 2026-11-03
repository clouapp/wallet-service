package models

// The states of a user's liquidation address. Provisioning is the reservation
// held while the provider is called outside the advisory lock; active pays out;
// inactive is history (Bridge reports anything but `active` that way).
const (
	LiquidationAddressStatusProvisioning = "provisioning"
	LiquidationAddressStatusActive       = "active"
	LiquidationAddressStatusInactive     = "inactive"
)
