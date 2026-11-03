package bank

// PayoutBeneficiaryDTO is a saved beneficiary as a bank payout links to it:
// the record the client sees and the row id the order's foreign key
// references. It goes from the bank service to the ramps service only; no
// resource renders it, so the internal id never reaches a response.
type PayoutBeneficiaryDTO struct {
	ID uint
	BeneficiaryRecordDTO
} //	@name	ClientBankPayoutBeneficiaryDTO
