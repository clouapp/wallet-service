package ramps

import (
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// BankAccountResource is a saved US bank account as its owner sees it: the
// routing and account numbers only by their last four digits, never whole.
type BankAccountResource struct {
	ID               uint                       `json:"id"`
	Label            string                     `json:"label"`
	AccountOwnerName string                     `json:"account_owner_name"`
	BankName         string                     `json:"bank_name"`
	AccountType      string                     `json:"account_type"`
	AccountLast4     string                     `json:"account_last4"`
	RoutingLast4     string                     `json:"routing_last4"`
	Address          BankAccountAddressResource `json:"address"`
} //	@name	ClientBankAccountResource

// BankAccountAddressResource is the holder's address. It mirrors
// models.BankAccountAddress because swag reads only the client tree;
// street_line_2 is always present, "" when empty, as the contract shows.
type BankAccountAddressResource struct {
	StreetLine1 string `json:"street_line_1"`
	StreetLine2 string `json:"street_line_2"`
	City        string `json:"city"`
	State       string `json:"state"`
	PostalCode  string `json:"postal_code"`
	Country     string `json:"country"`
} //	@name	ClientBankAccountAddressResource

// BankAccountCollection is GET /api/bank-accounts.
type BankAccountCollection struct {
	Data []BankAccountResource `json:"data"`
} //	@name	ClientBankAccountCollection

// NewBankAccountResource projects one saved account.
func NewBankAccountResource(view *clientramps.BankAccountView) BankAccountResource {
	return BankAccountResource{
		ID: view.ID, Label: view.Label, AccountOwnerName: view.OwnerName, BankName: view.BankName,
		AccountType: view.AccountType, AccountLast4: view.AccountLast4, RoutingLast4: view.RoutingLast4,
		Address: BankAccountAddressResource{
			StreetLine1: view.Address.StreetLine1, StreetLine2: view.Address.StreetLine2, City: view.Address.City,
			State: view.Address.State, PostalCode: view.Address.PostalCode, Country: view.Address.Country,
		},
	}
}

// NewBankAccountCollection projects the list; an empty list is [], never null.
func NewBankAccountCollection(views []clientramps.BankAccountView) BankAccountCollection {
	data := make([]BankAccountResource, 0, len(views))
	for i := range views {
		data = append(data, NewBankAccountResource(&views[i]))
	}
	return BankAccountCollection{Data: data}
}
