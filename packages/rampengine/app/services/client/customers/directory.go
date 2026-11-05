package customers

import (
	"context"
	"errors"
	"fmt"
	"strings"

	companyregistrydto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/companyregistry"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/customers"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	customersflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/customers"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
)

var (
	// ErrBusinessExists refuses a document a business past draft holds,
	// whoever holds it — the caller included — and never says whose: the
	// KYC's own refusal, which a submission gives when one took it since.
	ErrBusinessExists = kycsvc.ErrBusinessExists
	// ErrBusinessInactive refuses a company the Receita holds suspended,
	// unfit, closed or void.
	ErrBusinessInactive = errors.New("customers: business inactive")
	// ErrAccountDeleted is a draft opened for an account a deletion took
	// first: the request authenticated before the deletion committed.
	ErrAccountDeleted = repositories.ErrUserDeleted
	// ErrJurisdictionNotServed refuses a company no enabled provider serves:
	// the KYC's own refusal, the one a person gets.
	ErrJurisdictionNotServed = kycsvc.ErrJurisdictionNotServed
)

// KycSource is what the customers routes read of a customer's KYC.
// kycsvc.ProviderKycService implements it.
type KycSource interface {
	// CustomerKyc is the rows customerID holds at the providers serving it.
	CustomerKyc(customerID uint) ([]models.CustomerProviderKyc, error)
	// RefuseUnservedBusiness answers ErrJurisdictionNotServed for a company
	// no enabled provider serves.
	RefuseUnservedBusiness(ctx context.Context, identity models.TaxIdentity) error
}

// CompanyRegistry is the Receita's word on a company, read afresh.
// companyregistry.CompanyRegistryService implements it.
type CompanyRegistry interface {
	Standing(ctx context.Context, cnpj string) companyregistrydto.StandingDTO
}

// Deps is what a Directory is built from, filled by name where it is wired.
type Deps struct {
	Customers repositories.CustomerRepository
	Profiles  repositories.CustomerBusinessProfileRepository
	Wallets   repositories.CustomerWalletRepository
	Kyc       KycSource
	Registry  CompanyRegistry
	Features  contracts.Feature
	Locker    locking.Locker
}

// Directory is the customers a user is a member of, and where a business is
// opened for them (spec §6.1, API contract §2).
type Directory struct {
	customers repositories.CustomerRepository
	profiles  repositories.CustomerBusinessProfileRepository
	wallets   repositories.CustomerWalletRepository
	kyc       KycSource
	registry  CompanyRegistry
	features  contracts.Feature
	locker    locking.Locker
}

// NewDirectory wires the directory.
func NewDirectory(deps Deps) *Directory {
	return &Directory{
		customers: deps.Customers, profiles: deps.Profiles, wallets: deps.Wallets,
		kyc: deps.Kyc, registry: deps.Registry, features: deps.Features, locker: deps.Locker,
	}
}

// CreateBusiness opens a business draft for in's document, or answers the
// user's own draft of it again (created false), in the contract's order
// (§2.2): a country nobody serves companies in, then a document a business
// past draft holds (ErrBusinessExists), then the user's own draft, then — for
// a CNPJ — the Receita (ErrBusinessInactive; a registry that cannot say
// refuses nothing). The PF KYC is not required: the representative is
// verified as a partner of the company's own application (spec §6.1).
//
// The user's requests for one document are serialized, so two taps open one
// draft: drafts have no unique, since two users may each hold one of the
// same document until the first approval.
func (d *Directory) CreateBusiness(ctx context.Context, user *models.User, in dto.CreateBusinessDTO) (CustomerView, bool, error) {
	identity := models.TaxIdentity{Type: strings.ToLower(strings.TrimSpace(in.TaxIDType)), Country: incorporation(in.IncorporationCountry)}
	if err := d.kyc.RefuseUnservedBusiness(ctx, identity); err != nil {
		return CustomerView{}, false, fmt.Errorf("customers: create business: %w", err)
	}
	taxID := models.CanonicalBusinessTaxID(identity.Type, in.TaxID)
	digest := repositories.BusinessTaxIDDigest(identity, taxID)
	var customer *models.Customer
	var created bool
	err := d.locker.WithLock(ctx, draftLockKey(user.ID, digest), func(ctx context.Context) error {
		var err error
		customer, created, err = d.openDraft(ctx, user, models.BusinessProfile{TaxIdentity: identity, TaxID: taxID}, digest)
		return err
	})
	if err != nil {
		return CustomerView{}, false, fmt.Errorf("customers: create business: %w", err)
	}
	views, err := d.views([]models.Customer{*customer})
	if err != nil {
		return CustomerView{}, false, err
	}
	return views[0], created, nil
}

// openDraft is CreateBusiness under the lock, from the document's hold on.
func (d *Directory) openDraft(ctx context.Context, user *models.User, profile models.BusinessProfile, digest string) (*models.Customer, bool, error) {
	held, err := d.customers.TaxIDHeld(digest)
	if err != nil {
		return nil, false, err
	}
	if held {
		return nil, false, ErrBusinessExists
	}
	draft, err := d.customers.FindDraftByMember(user.ID, digest)
	if err != nil || draft != nil {
		return draft, false, err
	}
	if profile.TaxIdentity.Type == models.TaxIDTypeCNPJ {
		standing := d.registry.Standing(ctx, profile.TaxID)
		if standing.Inactive {
			return nil, false, ErrBusinessInactive
		}
		profile.LegalName, profile.TradeName = standing.LegalName, standing.TradeName
	}
	customer, err := d.customers.CreateBusinessDraft(user.ID, profile)
	if err != nil {
		return nil, false, err
	}
	return customer, true, nil
}

// draftLockKey serializes one user's drafts of one document.
func draftLockKey(userID uint, digest string) string {
	return fmt.Sprintf("customers:business-draft:%d:%s", userID, digest)
}

// incorporation is the country of incorporation as an alpha-3; the form
// request validated it is a country.
func incorporation(country string) string {
	if code, ok := iso3166.Alpha3(country); ok {
		return code
	}
	return strings.ToUpper(strings.TrimSpace(country))
}

// List is every customer user is a member of (ListByMember's order), each as
// GET /api/customers shows it, and whether the kyb flag lets them open a
// company. A flag store that cannot be read offers none, as the creation
// refuses then too.
func (d *Directory) List(ctx context.Context, user *models.User) (ListView, error) {
	customers, err := d.customers.ListByMember(user.ID)
	if err != nil {
		return ListView{}, err
	}
	views, err := d.views(customers)
	if err != nil {
		return ListView{}, err
	}
	canOpen, err := d.features.IsActive(ctx, customersflags.Kyb, user)
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("customers: flag %q for user %d: %v (offering no company)", customersflags.Kyb, user.ID, err)
		canOpen = false
	}
	return ListView{Customers: views, CanOpenBusiness: canOpen}, nil
}
