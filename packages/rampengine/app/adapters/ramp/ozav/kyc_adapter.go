package ozav

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
)

// typeIndividual is the only customer and account-holder type XIP submits.
const typeIndividual = "individual"

// ClientFactory builds a Client from the provider's current settings. It runs
// per call because an admin can rotate the key or the mode at runtime.
type ClientFactory func(ctx context.Context) (*Client, error)

// SubmitApplication creates the Ozav customer, or updates it when one
// already exists: a resubmission after a rejection must never open a second
// customer for the same person.
func (a *Adapter) SubmitApplication(ctx context.Context, ref ramp.CustomerRef, app ramp.Application) (*ramp.KycState, error) {
	creating := ref.ExternalID == ""
	body, err := customerRequestFrom(app, creating)
	if err != nil {
		return nil, err
	}

	req := Request{Method: http.MethodPost, Path: "/customers", Body: body, IdempotencyKey: app.IdempotencyKey}
	if !creating {
		req.Method, req.Path = http.MethodPatch, customerPath(ref.ExternalID)
	}

	state, err := a.customerState(ctx, req)
	if err == nil || !creating {
		return state, err
	}
	// A create that reached Ozav without our link row being written (a 500
	// after the record was made, 02/09) leaves an orphan every retry answers
	// with 409; see adoptExistingCustomer.
	adopted, adoptErr := a.adoptExistingCustomer(ctx, app, err)
	if adoptErr == nil {
		return adopted, nil
	}
	// A create refused over who the applicant is ends the matter at Ozav;
	// anything else is today's failure, which a retry may clear.
	if refusal := createRefusal(err, adoptErr); refusal != nil {
		return nil, refusal
	}
	return nil, err
}

// ApplicationDocuments is what Ozav asks every individual for: a proof of
// address and a recent bank statement, both listed in still_required for all
// of them. Declaring them puts them in the wizard pass that creates the
// customer (decided with Luiz on 28/09/2026 for the statement) instead of one
// round trip later. The proof of address was already in the first pass of a
// new user's wizard; a user whose Ozav customer is created from a saved
// profile skips that pass, and without it here Ozav was left waiting on the
// proof (measured on staging the same night).
func (a *Adapter) ApplicationDocuments() []string {
	return []string{docTypeProofOfAddress, docTypeBankStatement}
}

// GetKyc reads the current compliance snapshot.
func (a *Adapter) GetKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	state, err := a.customerState(ctx, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
	if err != nil {
		return nil, err
	}
	state.Requirements = uniqueNonEmpty(state.Requirements, a.documentsStillRequired(ctx, ref.ExternalID))
	return state, nil
}

// documentsStillRequired reads the files Ozav is still waiting on. The customer
// object never names them (missing_fields are profile paths), so without this
// a person Ozav could not verify reads as owing nothing, and the app reruns its
// whole wizard when what is missing is two photographs.
//
// It fails soft: losing the customer's status because the documents listing
// was down would take every screen down with it. ramp.LogUpstreamCall has
// already logged the failure, and the next refresh closes the gap.
func (a *Adapter) documentsStillRequired(ctx context.Context, externalID string) []string {
	var page customerDocumentsPage
	if a.do(ctx, Request{Method: http.MethodGet, Path: customerPath(externalID) + "/documents"}, &page) != nil {
		return nil
	}
	return page.StillRequired
}

// RefreshKyc is GetKyc: Ozav's customer object is always current. The
// difference lives in the service, which persists on a refresh.
func (a *Adapter) RefreshKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	return a.GetKyc(ctx, ref)
}

// GetRequirements reports what the user still owes, straight from the
// customer object: Ozav has no per-endorsement breakdown.
func (a *Adapter) GetRequirements(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycRequirements, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
	if err != nil {
		return nil, err
	}
	stillRequired := a.documentsStillRequired(ctx, ref.ExternalID)
	// This is the read the scoped wizard opens on, so it is where an ask
	// naming nothing would otherwise surface as an empty wizard.
	resp.logUnnamedRequest(stillRequired)
	return &ramp.KycRequirements{
		Status: customerStatus(resp),
		// The files too, as GetKyc reads them: the scoped wizard takes its
		// steps from here, and without them a customer Ozav is chasing for a
		// bank statement opened a wizard with nothing in it.
		Missing:      uniqueNonEmpty(requirementsFrom(resp.Requirements), stillRequired),
		Endorsements: []ramp.Endorsement{synthesizeEndorsement(resp.Status, resp.Verification.Mode)},
		Raw:          raw,
	}, nil
}

// client resolves the transport. A configuration that cannot produce one is a
// provably pre-send failure, so the router may fail over from it.
func (a *Adapter) client(ctx context.Context) (*Client, error) {
	if a.clients == nil {
		return nil, fmt.Errorf("ozav: no client factory configured: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.clients(ctx)
	if err != nil {
		return nil, fmt.Errorf("ozav: resolve client: %w: %w", ramp.ErrRequestNotSent, err)
	}
	return client, nil
}

func (a *Adapter) do(ctx context.Context, req Request, out any) error {
	client, err := a.client(ctx)
	if err != nil {
		return err
	}
	return client.Do(ctx, req, out)
}

// customerState maps a customer-shaped response (create, update and get all
// answer one) onto the canonical snapshot.
func (a *Adapter) customerState(ctx context.Context, req Request) (*ramp.KycState, error) {
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, req)
	if err != nil {
		return nil, err
	}
	rejection := resp.loggedRejection()
	return &ramp.KycState{
		ExternalCustomerID: resp.ID,
		CustomerEmail:      resp.Email,
		Status:             customerStatus(resp),
		// Empty, so the service keeps what the row holds: the customer does
		// not echo the terms session, and any value here would overwrite an
		// accepted one on every read.
		TosStatus:           "",
		Endorsements:        []ramp.Endorsement{synthesizeEndorsement(resp.Status, resp.Verification.Mode)},
		Requirements:        requirementsFrom(resp.Requirements),
		RejectionCategories: rejection.categories,
		RejectionDetails:    rejection.details,
		Raw:                 raw,
	}, nil
}

func customerPath(externalID string) string { return "/customers/" + externalID }

// requirementsFrom lists due items (established, not yet met) before
// missing_fields (never supplied), then the files compliance asked for by name
// and has not received.
func requirementsFrom(r customerRequirements) []string {
	return uniqueNonEmpty(r.Due, r.MissingFields, r.owedDocuments())
}

// uniqueNonEmpty concatenates the lists without blanks or repeats, in
// first-seen order: the result is rendered to the user as is, and Ozav can
// repeat an item across lists.
func uniqueNonEmpty(lists ...[]string) []string {
	seen := make(map[string]struct{})
	var out []string
	for _, list := range lists {
		for _, item := range list {
			if _, dup := seen[item]; item == "" || dup {
				continue
			}
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// customerRequestFrom maps the canonical Application onto Ozav's customer
// payload for the POST that creates the customer or the PATCH that updates
// it.
//
// type, custody_model and terms_session_id go on the create only: PATCH
// answers each with 400 INVALID_REQUEST "Unrecognized key(s)" (measured 01/09,
// 02/09), and none can change after creation. custody_model is named rather
// than left to the organization's default, which was self_custody and blocked
// every off-ramp before 28/08.
func customerRequestFrom(app ramp.Application, creating bool) (customerRequest, error) {
	address, err := addressFrom(app.Profile.Address)
	if err != nil {
		return customerRequest{}, err
	}
	phone, err := phoneFrom(app.Profile.Phone)
	if err != nil {
		return customerRequest{}, err
	}

	individual := &individualPayload{
		LegalName:                legalNameFrom(app.Profile),
		MotherName:               app.Profile.MotherName,
		DateOfBirth:              app.Profile.BirthDate,
		Phone:                    phone,
		TaxID:                    taxIDFrom(app.IdentifyingInformation),
		ResidentialAddress:       address,
		Occupation:               app.Profile.Occupation,
		MonthlyIncomeUSD:         app.Profile.MonthlyIncomeUSD,
		ExpectedMonthlyVolumeUSD: app.Profile.ExpectedMonthlyVolumeUSD,
		IsPEP:                    app.Profile.IsPEP,
	}
	if individual.Nationality, err = optionalAlpha2(app.Profile.Nationality); err != nil {
		return customerRequest{}, err
	}
	if individual.SourceOfFunds, err = sourceOfFundsLabel(app.Profile.SourceOfFunds); err != nil {
		return customerRequest{}, err
	}
	if individual.AccountPurpose, err = accountPurposeLabel(app.Profile.AccountPurpose); err != nil {
		return customerRequest{}, err
	}

	req := customerRequest{Email: app.Profile.Email, Individual: individual}
	if creating {
		req.Type = cmp.Or(app.Type, typeIndividual)
		req.CustodyModel = custodyManaged
		req.TermsSessionID = app.SignedAgreementID
	}
	return req, nil
}

// legalNameFrom prefers LegalName, which no caller fills today, over the
// joined first and last names.
func legalNameFrom(profile ramp.Profile) string {
	return cmp.Or(profile.LegalName, strings.TrimSpace(profile.FirstName+" "+profile.LastName))
}

// addressFrom returns nil for an empty address, so an application that has
// not collected one does not fail over a country it never supplied.
func addressFrom(addr ramp.Address) (*addressPayload, error) {
	if addr == (ramp.Address{}) {
		return nil, nil
	}
	country, err := countryAlpha2(addr.Country)
	if err != nil {
		return nil, err
	}
	return &addressPayload{
		Line1:        addr.StreetLine1,
		Number:       addr.Number,
		Neighborhood: addr.Neighborhood,
		City:         addr.City,
		State:        addr.Subdivision,
		PostalCode:   addr.PostalCode,
		Country:      country,
	}, nil
}

// taxIDFrom pulls the CPF out of the identifying documents: only the entry the
// KYC service types "cpf" qualifies.
func taxIDFrom(docs []ramp.IdentifyingDocument) *taxIDPayload {
	for _, doc := range docs {
		if doc.Type == taxIDTypeCPF && doc.Number != "" {
			return &taxIDPayload{Country: taxIDCountryBR, Type: taxIDTypeCPF, Value: doc.Number}
		}
	}
	return nil
}

// phoneFrom splits an E.164 phone into Ozav's country_code/number pair. An
// absent phone is nil (Ozav reports it in missing_fields); one that does not
// split fails here, rather than reaching Ozav with a blank country_code and a
// rejection naming a field the client never saw.
func phoneFrom(phone string) (*phonePayload, error) {
	if strings.TrimSpace(phone) == "" {
		return nil, nil
	}
	code, number, err := ramp.SplitPhone(phone)
	if err != nil {
		return nil, err
	}
	return &phonePayload{CountryCode: code, Number: number}, nil
}

// countryAlpha2 converts a country to the alpha-2 Ozav requires. An unknown
// code fails here rather than reaching Ozav wrong and leaving the customer
// pending with nothing to explain why.
func countryAlpha2(alpha3 string) (string, error) {
	code, ok := iso3166.Alpha2(alpha3)
	if !ok {
		return "", fmt.Errorf("ozav: unrecognized country code %q", alpha3)
	}
	return code, nil
}

// optionalAlpha2 is countryAlpha2 for a field the user may leave empty.
func optionalAlpha2(alpha3 string) (string, error) {
	if alpha3 == "" {
		return "", nil
	}
	return countryAlpha2(alpha3)
}
