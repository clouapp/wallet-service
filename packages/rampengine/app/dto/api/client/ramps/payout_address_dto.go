package ramps

// ProvisionPayoutAddressDTO asks for the standing address that pays one of
// the user's US bank accounts over one rail. Every deposit into the address is
// a payout; Amount, when the app is about to send one, is checked against the
// rail's minimum first.
type ProvisionPayoutAddressDTO struct {
	// Rail is usd_ach_payout or usd_wire_payout.
	Rail          string
	BankAccountID uint
	Token         string
	Chain         string
	// Wallet is the user's own address, where a failed payout is returned.
	Wallet string
	// Reference is the ACH reference or the wire message. nil (omitted) keeps
	// the address's own; "" asks for none.
	Reference *string
	// Amount is the token amount the user is about to send, or "" for a read
	// that sends nothing. Below the rail's minimum after the fee, the address
	// is still kept and the answer is amount_below_minimum.
	Amount string
} //	@name	ClientProvisionPayoutAddressDTO

// SimulatePayoutDepositDTO names the address a sandbox deposit goes into and
// how much the user pretends to send.
type SimulatePayoutDepositDTO struct {
	Rail          string
	BankAccountID uint
	Token         string
	Chain         string
	Amount        string
} //	@name	ClientSimulatePayoutDepositDTO
