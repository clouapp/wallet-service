package bridge

// bankPaymentRail reads the bank network Bridge names on a deposit's source or
// a drain's destination into the vocabulary transactions.payment_rail stores:
// every ACH spelling is "ach", and a rail this side does not know is "",
// never a guess.
func bankPaymentRail(raw string) string {
	switch raw {
	case "ach", "ach_push", "ach_same_day":
		return "ach"
	case "wire":
		return "wire"
	case "fednow":
		return "fednow"
	case "pix":
		return "pix"
	}
	return ""
}
