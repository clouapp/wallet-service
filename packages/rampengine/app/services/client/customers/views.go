package customers

import (
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
)

// ListView is GET /api/customers: the user's customers and whether they may
// open a company.
type ListView struct {
	Customers       []CustomerView
	CanOpenBusiness bool
}

// CustomerView is one customer as the app sees it (API contract §2.1).
type CustomerView struct {
	Customer models.Customer
	// Business is what a business declared; nil for an individual.
	Business *BusinessView
	// Kyc is the rows the customer holds at the providers serving it.
	Kyc     []models.CustomerProviderKyc
	Wallets []models.CustomerWallet
}

// BusinessView is a company as the app may show it: never its whole document.
// An empty name is one nobody declared yet.
type BusinessView struct {
	LegalName            string
	DisplayName          string
	IncorporationCountry string
	TaxIDType            string
	// TaxIDMasked is a CNPJ in the Pix key's mask, any other document four
	// asterisks and its last four characters.
	TaxIDMasked string
}

// views reads what the views of customers show, in their order: the
// businesses' declarations and every customer's wallets in one read each,
// and each customer's KYC rows.
func (d *Directory) views(customers []models.Customer) ([]CustomerView, error) {
	ids := make([]uint, 0, len(customers))
	businesses := make([]uint, 0, len(customers))
	for i := range customers {
		ids = append(ids, customers[i].ID)
		if customers[i].Type == models.CustomerTypeBusiness {
			businesses = append(businesses, customers[i].ID)
		}
	}
	profiles, err := d.profiles.FindByCustomers(businesses)
	if err != nil {
		return nil, fmt.Errorf("customers: views: %w", err)
	}
	wallets, err := d.wallets.ListByCustomers(ids)
	if err != nil {
		return nil, fmt.Errorf("customers: views: %w", err)
	}
	views := make([]CustomerView, 0, len(customers))
	for i := range customers {
		view := CustomerView{Customer: customers[i], Wallets: walletsOf(wallets, customers[i].ID)}
		if profile, ok := profiles[customers[i].ID]; ok {
			view.Business = businessView(profile)
		}
		if view.Kyc, err = d.kyc.CustomerKyc(customers[i].ID); err != nil {
			return nil, fmt.Errorf("customers: views: %w", err)
		}
		views = append(views, view)
	}
	return views, nil
}

func walletsOf(wallets []models.CustomerWallet, customerID uint) []models.CustomerWallet {
	out := []models.CustomerWallet{}
	for i := range wallets {
		if wallets[i].CustomerID == customerID {
			out = append(out, wallets[i])
		}
	}
	return out
}

func businessView(profile models.BusinessProfile) *BusinessView {
	return &BusinessView{
		LegalName:            profile.LegalName,
		DisplayName:          profile.DisplayName(),
		IncorporationCountry: profile.TaxIdentity.Country,
		TaxIDType:            profile.TaxIdentity.Type,
		TaxIDMasked:          MaskTaxID(profile.TaxIdentity.Type, profile.TaxID),
	}
}

// MaskTaxID shows a document as the app and the admin may: a CNPJ in the Pix
// key's mask, so the app tells the company's key by it; any other document by
// its last four characters.
func MaskTaxID(taxIDType, taxID string) string {
	if taxIDType == models.TaxIDTypeCNPJ {
		return pixkeys.Mask(taxID, taxIDType)
	}
	if len(taxID) <= 4 {
		return "****"
	}
	return "****" + taxID[len(taxID)-4:]
}
