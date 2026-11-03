package kyc

import (
	"context"
	"errors"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// ErrJurisdictionNotServed refuses a person no enabled provider serves — by
// where they live, or by the tax identity they declare — and, on a route that
// names its provider, a provider that does not serve them. It is answered
// before anything is staged, a terms session opened or a provider called: a
// provider that cannot serve someone would only refuse them upstream, after
// their documents had reached it. One code, no field: the countries catalog
// already tells the app what each residence and identity opens.
var ErrJurisdictionNotServed = errors.New("kyc: jurisdiction not served")

// railsNotOpened is every rail adapter does not open to who, in
// ramp.AllRails order: what the router refuses as rail_not_eligible before it
// reads the row, asked the router's own way (ramp.Person.Opens). None for
// nobody in particular, whom the router narrows by nothing.
func railsNotOpened(who *ramp.Person, adapter ramp.ProviderAdapter) []string {
	var refused []string
	for _, rail := range ramp.AllRails() {
		if !who.Opens(adapter, rail) {
			refused = append(refused, rail.String())
		}
	}
	return refused
}

// MarkRailsNotEligible sets RailsNotEligible on rows the way list does for the
// client listings: the rails each row's provider does not open to the person
// customerID declared. It is how a surface that reads the rows
// itself (the admin's support view) shows the rails the router would actually
// let the user use, rather than whatever the endorsements clear — a US resident
// endorsed for Pix is still refused pix_payin as rail_not_eligible.
//
// A row whose provider is unknown or a stub is left unmarked: there is no
// policy to read for it, and the endorsements are all there is to show.
func (s *ProviderKycService) MarkRailsNotEligible(customerID uint, rows []models.CustomerProviderKyc) error {
	adapters := make(map[int]ramp.ProviderAdapter, len(rows))
	for i := range rows {
		if !s.factory.IsImplemented(rows[i].Provider) {
			continue
		}
		if adapter, err := s.factory.Get(rows[i].Provider); err == nil {
			adapters[i] = adapter
		}
	}
	// The profile is read only when there is a row to mark.
	if len(adapters) == 0 {
		return nil
	}
	who, err := s.persons.RetainedPerson(customerID)
	if err != nil {
		return fmt.Errorf("kyc: mark rails not eligible: %w", err)
	}
	for i, adapter := range adapters {
		rows[i].RailsNotEligible = railsNotOpened(who, adapter)
	}
	return nil
}

// applicant is who an application stands for: the identity checkIdentity
// settled on — the one it carries, else the retained one — and the address it
// declares. An installed build sends no type and always BRA, and is read as a
// CPF holder living in Brazil, whom every provider serves.
func applicant(input dto.ApplicationDTO, identity declaredIdentity) *ramp.Person {
	tax := identity.tax
	if tax == (models.TaxIdentity{}) {
		tax = taxIdentityOf(input)
	}
	return &ramp.Person{Identity: tax, Residence: input.Country}
}

// tosApplicant is who a terms request declares, nil when it names no
// residence — every policy is keyed on one, and a build that predates the
// fields sends none. The type and issuing country are derived as an
// application's are when omitted.
func tosApplicant(input dto.StartTosDTO) *ramp.Person {
	// Gated on country alone, by decision: clients send the three fields
	// together, and tax_id_type or tax_id_country without a residence filters
	// nothing.
	if input.Country == "" {
		return nil
	}
	return &ramp.Person{
		Identity:  models.DeriveTaxIdentity(input.TaxIDType, input.TaxIDCountry, input.Country),
		Residence: input.Country,
	}
}

// implementedProviders is every provider a KYC call may reach, in provider
// order: eligible (ramp.Config.EligibleProviders — enabled, and in the mode
// APP_ENV expects) and implemented. Every fan-out, listing and the countries
// catalog read their providers from here, so none of them can offer what a
// submission would not reach.
//
// A stub is passed over: it consumes nothing and refuses everything, and
// submitting identity documents to it would be a real side effect bought for
// no answer. onSkip, when non-nil, hears each provider passed over for either
// reason — a mode mismatch or a stub — both an operator's doing that nothing
// else would surface; a read run on every page load passes nil.
func implementedProviders(
	config *ramp.Config, factory *ramp.Factory, appEnv string, onSkip func(code ramp.ProviderCode, reason error),
) ([]ramp.ProviderAdapter, error) {
	codes, err := config.EligibleProviders(appEnv, onSkip)
	if err != nil {
		return nil, fmt.Errorf("kyc: enabled providers: %w", err)
	}
	adapters := make([]ramp.ProviderAdapter, 0, len(codes))
	for _, code := range codes {
		if !factory.IsImplemented(code) {
			if onSkip != nil {
				onSkip(code, ramp.ErrProviderNotImplemented)
			}
			continue
		}
		adapter, err := factory.Get(code)
		if err != nil {
			return nil, err
		}
		adapters = append(adapters, adapter)
	}
	return adapters, nil
}

// serving narrows adapters, in their order, to the ones that serve who: a
// provider is eligible for a person when its jurisdiction policy opens them
// some rail.
//
// pixListed answers whether some Pix rail's provider_order names a provider.
// It is asked only of a provider gated on a declared residence, and only once
// the person opens it a Pix rail, so a read that has no such provider never
// touches the rail settings.
func serving(
	adapters []ramp.ProviderAdapter, who *ramp.Person, pixListed func(ramp.ProviderCode) (bool, error),
) ([]ramp.ProviderAdapter, error) {
	out := make([]ramp.ProviderAdapter, 0, len(adapters))
	for _, adapter := range adapters {
		// The Pix wizard: a provider that serves Pix only to the people its
		// jurisdiction names joins once the person's declaration opens it a
		// Pix rail and an operator routes some Pix rail to it. Enabled alone
		// is not enough: a provider switched on for its bank rails would
		// otherwise collect every Brazilian's documents for a Pix it never
		// serves. Every other provider joins by whether it opens any rail.
		// The routes that name a provider are not narrowed here
		// (refuseUnserved).
		if ramp.NeedsDeclaredResidenceForPix(adapter) {
			if !who.OpensPix(adapter) {
				continue
			}
			listed, err := pixListed(adapter.Code())
			if err != nil {
				return nil, err
			}
			if listed {
				out = append(out, adapter)
			}
			continue
		}
		if who.OpensAny(adapter) {
			out = append(out, adapter)
		}
	}
	return out, nil
}

// pixRailLister is serving's pixListed over config: the Pix rails'
// provider_order, read once, on the first question.
func pixRailLister(config *ramp.Config) func(ramp.ProviderCode) (bool, error) {
	var listed map[ramp.ProviderCode]bool
	return func(code ramp.ProviderCode) (bool, error) {
		if listed == nil {
			listed = map[ramp.ProviderCode]bool{}
			for _, rail := range ramp.PixRails() {
				order, err := config.ProviderOrder(rail)
				if err != nil {
					listed = nil
					return false, fmt.Errorf("kyc: pix provider order: %w", err)
				}
				for _, listedCode := range order {
					listed[listedCode] = true
				}
			}
		}
		return listed[code], nil
	}
}

// servingRetained is what the listings show — GET /api/kyc, the overview, the
// capabilities banner: implementedProviders, read quietly, narrowed to the
// ones that serve who customerID declared itself to be (ramp.Persons), and
// that person (nil for an individual with no profile). With no profile it
// narrows nothing, as before; the declaration is read only when there is a
// provider to narrow.
//
// Narrowed to nothing is an empty list, not a refusal: a listing reports, and
// nobody serving the user is what it has to report.
func servingRetained(
	config *ramp.Config, factory *ramp.Factory, appEnv string,
	persons ramp.Persons, customerID uint,
) ([]ramp.ProviderAdapter, *ramp.Person, error) {
	adapters, err := implementedProviders(config, factory, appEnv, nil)
	if err != nil || len(adapters) == 0 {
		return adapters, nil, err
	}
	who, err := persons.RetainedPerson(customerID)
	if err != nil {
		return nil, nil, err
	}
	served, err := serving(adapters, who, pixRailLister(config))
	if err != nil {
		return nil, nil, err
	}
	return served, who, nil
}

// refuseUnserved is a single-provider route's half of eligibility per person:
// the provider the path names must serve who, the same test the fan-out
// applies, or the call is refused before anything is asked of it. A stub is
// left to the call, which refuses it for what it is.
func (s *ProviderKycService) refuseUnserved(adapter ramp.ProviderAdapter, who *ramp.Person) error {
	if !s.factory.IsImplemented(adapter.Code()) || who.OpensAny(adapter) {
		return nil
	}
	return ErrJurisdictionNotServed
}

// refuseUnservedRetained is refuseUnserved for a route whose body carries no
// identity — documents, terms, a hosted link, endorsements — read from the
// declaration the customer retained. It also refuses a residence a flag holds
// shut (refuseClosedResidence), as the fan-out does. An unknown provider or a
// stub is left to the call, before the declaration is read for it.
func (s *ProviderKycService) refuseUnservedRetained(ctx context.Context, customerID uint, provider string) error {
	if !s.factory.IsImplemented(provider) {
		return nil
	}
	adapter, err := s.factory.Get(provider)
	if err != nil {
		return err
	}
	who, err := s.persons.RetainedPerson(customerID)
	if err != nil {
		return err
	}
	// While its flag is off, a residence is closed on every route: the fan-out
	// (fanOutProviders) refuses the same person the same way.
	if err := s.refuseClosedResidence(ctx, who); err != nil {
		return err
	}
	return s.refuseUnserved(adapter, who)
}
