// Package bank contains DTOs for the client saved US bank account API.
package bank

import "time"

// BeneficiaryDTO is a beneficiary as the client sends it, before the service
// normalizes and validates it.
type BeneficiaryDTO struct {
	Label, AccountClass, AccountType, Relationship          string
	BeneficiaryName, RoutingNumber, AccountNumber           string
	AddressLine1, AddressLine2, City, StateCode, PostalCode string
	BusinessIndustry                                        string
} //	@name	ClientBankBeneficiaryDTO

// BeneficiaryRecordDTO is a saved beneficiary as the service hands it back.
// The numbers are the last four digits only: the full ones never leave the
// repository on a read.
type BeneficiaryRecordDTO struct {
	ExternalID                                              string
	Revision                                                int
	CreatedAt, UpdatedAt                                    time.Time
	Label, AccountClass, AccountType, Relationship          string
	AccountLast4, RoutingLast4                              string
	BeneficiaryName                                         string
	AddressLine1, AddressLine2, City, StateCode, PostalCode string
	BusinessIndustry                                        string
} //	@name	ClientBankBeneficiaryRecordDTO
