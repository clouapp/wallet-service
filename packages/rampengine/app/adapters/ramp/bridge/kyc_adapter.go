package bridge

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/support/iso3166"
	"github.com/macrowallets/waas/packages/rampengine/app/support/ustaxid"
)

// Bridge's customer and account owner types, which are XIP's own
// (models.CustomerType*).
const (
	customerTypeIndividual = models.CustomerTypeIndividual
	customerTypeBusiness   = models.CustomerTypeBusiness
)

// ClientFactory builds a Client from the provider's current settings. It runs
// per call because an admin can change the key or the mode at runtime, and a
// client captured at boot would keep the old ones until a restart.
type ClientFactory func(ctx context.Context) (*Client, error)

// Adapter is the Bridge provider adapter.
type Adapter struct {
	clients        ClientFactory
	webhookSecrets SecretFactory
}

// New returns the Bridge adapter. Both factories are resolved per call, so an
// admin rotating the API key or the webhook key needs no restart.
func New(clients ClientFactory, webhookSecrets SecretFactory) *Adapter {
	return &Adapter{clients: clients, webhookSecrets: webhookSecrets}
}

// Code identifies the provider.
func (a *Adapter) Code() ramp.ProviderCode { return ramp.ProviderBridge }

// PreferredKycMode is API documents: Bridge takes the documents and selfies on
// the customer, and the hosted link is the fallback.
func (a *Adapter) PreferredKycMode() ramp.KycMode { return ramp.KycModeAPIDocs }

// RequiresTos is true: Bridge refuses a customer create without a signed
// agreement id.
func (a *Adapter) RequiresTos() bool { return true }

// Supports declares both PIX directions and the three USD rails. The PIX payin is served by
// ProvisionVirtualAccount, not CreateOnramp: Bridge takes PIX only as a
// transfer destination (a `source.payment_rail: pix` transfer answers 500), so
// BRL is collected through a standing virtual account.
//
// The token must be one quotableCurrencies prices, or the router would pick a
// provider whose quote nothing can redeem. An unnamed token or chain means the
// caller did not ask: the sticky create path resolves on the rail alone.
func (a *Adapter) Supports(c ramp.Capability) bool {
	switch c.Rail {
	case ramp.RailPixPayin, ramp.RailPixPayout, ramp.RailUSDPayin, ramp.RailUSDACHPayout, ramp.RailUSDWirePayout:
	default:
		return false
	}
	if c.Token != "" && !quotableCurrencies[strings.ToUpper(c.Token)] {
		return false
	}
	return c.Chain == "" || movableChains[strings.ToLower(strings.TrimSpace(c.Chain))]
}

// CreateTosSession issues the hosted terms page. Bridge hands the signed
// agreement id over on the redirect after the user signs, so the session
// usually carries only a URL.
func (a *Adapter) CreateTosSession(ctx context.Context, _ ramp.CustomerRef, opts ramp.TosOpts) (*ramp.TosSession, error) {
	client, err := a.client(ctx)
	if err != nil {
		return nil, err
	}
	resp, raw, err := ramp.Call[tosLinkResponse](ctx, client.Do, Request{
		Method:         http.MethodPost,
		Path:           "/customers/tos_links",
		Body:           tosLinkRequest{RedirectURI: opts.RedirectURI},
		IdempotencyKey: opts.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	sessionURL, err := appendRedirectURI(resp.URL, opts.RedirectURI)
	if err != nil {
		return nil, err
	}
	return &ramp.TosSession{
		URL:               sessionURL,
		SignedAgreementID: resp.SignedAgreementID,
		AllowedHosts:      tosHostsFor(client.cfg.BaseURL, sessionURL),
		Raw:               raw,
	}, nil
}

// tosAllowedHosts is where Bridge's terms page may load from. Bridge issues it
// from `dashboard`, serves parts of the flow from `compliance` (with a sandbox
// host) and posts the result back over postMessage, so the client's origin
// check has to accept the whole set.
var tosAllowedHosts = []string{
	"dashboard.bridge.xyz",
	"compliance.bridge.xyz",
	"compliance.sandbox.bridge.xyz",
	"bridge.xyz",
}

// tosHostsFor is tosAllowedHosts against the real Bridge. Against anything else
// (cmd/devstub, reachable only outside production) the page is on neither
// bridge.xyz host, and a list naming only those closes the WebView on it, so
// the list names the configured base and the page the stub handed out.
func tosHostsFor(baseURL, pageURL string) []string {
	if baseURL == SandboxBaseURL || baseURL == ProductionBaseURL {
		return tosAllowedHosts
	}
	var hosts []string
	for _, raw := range []string{baseURL, pageURL} {
		parsed, err := url.Parse(raw)
		if err == nil && parsed.Hostname() != "" && !slices.Contains(hosts, parsed.Hostname()) {
			hosts = append(hosts, parsed.Hostname())
		}
	}
	return hosts
}

// SubmitApplication creates the Bridge customer, or updates it when one already
// exists: a resubmission after a rejection must never open a second customer
// for the same person.
func (a *Adapter) SubmitApplication(ctx context.Context, ref ramp.CustomerRef, app ramp.Application) (*ramp.KycState, error) {
	// Bridge would refuse it anyway; refusing here names the step the user owes.
	if app.SignedAgreementID == "" {
		return nil, ramp.ErrTosRequired
	}
	// The company makes it a business's, never the type a client sends.
	if app.Business != nil {
		return a.submitBusinessApplication(ctx, ref, app)
	}

	// LegalName and MotherName are dropped: Bridge's customer has no field for
	// either.
	body := customerRequest{
		Type:                   cmp.Or(app.Type, customerTypeIndividual),
		FirstName:              app.Profile.FirstName,
		LastName:               app.Profile.LastName,
		Email:                  app.Profile.Email,
		Phone:                  app.Profile.Phone,
		BirthDate:              app.Profile.BirthDate,
		SourceOfFunds:          sourceOfFundsValues[app.Profile.SourceOfFunds],
		AccountPurpose:         accountPurposeValues[app.Profile.AccountPurpose],
		AccountPurposeOther:    accountPurposeOther[app.Profile.AccountPurpose],
		ResidentialAddress:     addressFrom(app.Profile.Address),
		SignedAgreementID:      app.SignedAgreementID,
		Endorsements:           endorsementsFor(app),
		IdentifyingInformation: documentsFrom(app.IdentifyingInformation),
		LivenessCheckSelfies:   selfieImages(app.LivenessCheckSelfies),
	}

	var state *ramp.KycState
	var err error
	if ref.ExternalID != "" {
		state, err = a.putCustomer(ctx, ref.ExternalID, body)
	} else {
		state, err = a.customerState(ctx, Request{
			Method: http.MethodPost, Path: "/customers", Body: body, IdempotencyKey: app.IdempotencyKey,
		})
	}
	// Bridge refuses a second POST for the same e-mail, which happens when the
	// customer exists upstream without our link row (a sandbox reset). Updating
	// it beats leaving the worker stuck.
	if err != nil && ref.ExternalID == "" && app.Profile.Email != "" && isDuplicateEmailError(err) {
		if id, lookupErr := a.findCustomerIDByEmail(ctx, app.Profile.Email); lookupErr == nil {
			state, err = a.putCustomer(ctx, id, body)
		}
	}
	if err != nil {
		return nil, err
	}
	// Bridge does not echo the agreement back, so the submitted one is what
	// the service persists.
	state.SignedAgreementID = app.SignedAgreementID
	return state, nil
}

// endorsementsFor is what a customer is asked to be endorsed for. A resident
// of the United States asks the umbrella pix, or its deposit half, as
// pix_offramp: the umbrella is deprecated (01/07/2026) and Bridge grants
// pix_onramp to no non-Brazilian individual. Whoever lives where Bridge offers
// the USD rails also asks base, the endorsement that clears them. Bridge
// evaluates every endorsement of the account whatever is asked (sandbox,
// 14/10/2026), so this is the honest request, not a gate. An application that
// asks nothing still asks nothing: Bridge then attempts base on its own.
func endorsementsFor(app ramp.Application) []string {
	code, _ := iso3166.Alpha3(app.Profile.Address.Country)
	out := make([]string, 0, len(app.Endorsements)+1)
	for _, endorsement := range app.Endorsements {
		if code == "USA" && (endorsement == models.EndorsementPix || endorsement == models.EndorsementPixOnramp) {
			endorsement = models.EndorsementPixOfframp
		}
		if !slices.Contains(out, endorsement) {
			out = append(out, endorsement)
		}
	}
	if len(out) > 0 && servesUSD(code) && !slices.Contains(out, models.EndorsementBase) {
		out = append(out, models.EndorsementBase)
	}
	return out
}

// SubmitDocuments adds identity documents and a proof of address to an
// existing customer, and nothing else: restating the profile on a patch would
// overwrite corrections made elsewhere. docs.IdempotencyKey is not forwarded
// (see putCustomer).
func (a *Adapter) SubmitDocuments(ctx context.Context, ref ramp.CustomerRef, docs ramp.Documents) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	if len(docs.CompanyDocuments) > 0 || len(docs.PersonDocuments) > 0 {
		return a.submitBusinessDocuments(ctx, ref, docs)
	}
	patch := customerRequest{
		IdentifyingInformation: documentsFrom(docs.IdentifyingInformation),
		LivenessCheckSelfies:   selfieImages(docs.LivenessCheckSelfies),
		Documents:              supportingDocuments(docs),
	}
	// A bank statement is Ozav's, not Bridge's, and the fan-out can reach
	// Bridge with a submission carrying nothing else. An empty PUT changes
	// nothing and may read upstream as a resubmission; a read answers the
	// same question without one.
	if patch.IdentifyingInformation == nil && patch.LivenessCheckSelfies == nil && patch.Documents == nil {
		return a.GetKyc(ctx, ref)
	}
	state, err := a.putCustomer(ctx, ref.ExternalID, patch)
	return state, documentRefusal(err)
}

// documentRefusal marks Bridge refusing the parameters of a documents call --
// a field it cannot take, as it answered selfies for a customer with no Pix
// endorsement (19/10/2026) -- as the refused document it is
// (ramp.ErrDocumentRefused): the same files are refused again, so it is
// neither an outage nor a retry. Any other failure is left as it is.
func documentRefusal(err error) error {
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr.Code == "invalid_parameters" {
		return fmt.Errorf("%w: %w", err, ramp.ErrDocumentRefused)
	}
	return err
}

// GetKyc reads the current compliance snapshot.
func (a *Adapter) GetKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	return a.customerState(ctx, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
}

// RefreshKyc is GetKyc: Bridge's customer object is always current. The
// difference lives in the service, which persists on a refresh.
func (a *Adapter) RefreshKyc(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycState, error) {
	return a.GetKyc(ctx, ref)
}

// SimulateKycApproval is a sandbox-only Bridge shortcut that approves a
// customer without a real review. The admin service guards production.
func (a *Adapter) SimulateKycApproval(ctx context.Context, ref ramp.CustomerRef) error {
	if ref.ExternalID == "" {
		return ramp.ErrIntegrationRequired
	}
	return a.do(ctx, Request{
		Method:         http.MethodPost,
		Path:           customerPath(ref.ExternalID) + "/simulate_kyc_approval",
		IdempotencyKey: "simulate-kyc:" + ref.ExternalID,
	}, nil)
}

// GetRequirements reports what the user still owes, gathered from the customer
// and from each endorsement.
func (a *Adapter) GetRequirements(ctx context.Context, ref ramp.CustomerRef) (*ramp.KycRequirements, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
	if err != nil {
		return nil, err
	}
	return &ramp.KycRequirements{
		Status:       kycStatusFromBridge(resp.Status),
		Missing:      resp.requirements(),
		Endorsements: resp.endorsements(),
		Raw:          raw,
	}, nil
}

// RequestEndorsements asks for more capabilities through the customer update
// every resubmission already sends, rather than a second endpoint.
func (a *Adapter) RequestEndorsements(ctx context.Context, ref ramp.CustomerRef, endorsements []string) (*ramp.KycState, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	return a.putCustomer(ctx, ref.ExternalID, customerRequest{Endorsements: endorsements})
}

// CreateHostedKycLink issues the hosted verification page. With no customer
// yet the POST creates one, hence the name and e-mail; with a customer, the
// GET scopes the link to it.
func (a *Adapter) CreateHostedKycLink(ctx context.Context, ref ramp.CustomerRef, opts ramp.HostedLinkOpts) (*ramp.HostedKycLink, error) {
	var req Request
	if ref.ExternalID != "" {
		path, query := hostedLinkPath(ref.ExternalID, opts)
		req = Request{Method: http.MethodGet, Path: path, Query: query}
	} else {
		req = Request{Method: http.MethodPost, Path: "/kyc_links", IdempotencyKey: opts.IdempotencyKey,
			Body: kycLinkRequest{
				FullName:     opts.FullName,
				Email:        opts.Email,
				Type:         cmp.Or(opts.Type, customerTypeIndividual),
				Endorsements: opts.Endorsements,
				RedirectURI:  opts.RedirectURI,
			},
		}
	}

	resp, raw, err := ramp.Call[kycLinkResponse](ctx, a.do, req)
	if err != nil {
		return nil, err
	}
	return &ramp.HostedKycLink{
		LinkID: resp.ID,
		URL:    resp.link(),
		// The GET form answers with a bare url.
		ExternalCustomerID: cmp.Or(resp.CustomerID, ref.ExternalID),
		CollectionMode:     ramp.CollectionModeHostedFallback,
		Raw:                raw,
	}, nil
}

// StartIntegration makes no call before the customer exists: it is born at
// SubmitApplication or at the hosted link.
func (a *Adapter) StartIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	if ref.ExternalID == "" {
		return &ramp.Integration{Status: models.IntegrationStatusPending}, nil
	}
	return a.RefreshIntegration(ctx, ref)
}

// RefreshIntegration confirms the customer still exists upstream and reports
// whether Bridge has offboarded it.
func (a *Adapter) RefreshIntegration(ctx context.Context, ref ramp.CustomerRef) (*ramp.Integration, error) {
	if ref.ExternalID == "" {
		return nil, ramp.ErrIntegrationRequired
	}
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, Request{Method: http.MethodGet, Path: customerPath(ref.ExternalID)})
	if err != nil {
		return nil, err
	}

	// A rejected customer still has a working link; only an offboarded one is
	// unusable.
	status := models.IntegrationStatusActive
	if resp.Status == bridgeStatusOffboarded {
		status = models.IntegrationStatusDisabled
	}
	return &ramp.Integration{ExternalCustomerID: resp.ID, Status: status, Raw: raw}, nil
}

// client resolves the transport. A configuration that cannot produce one is a
// provably pre-send failure, so the router may fail over from it.
func (a *Adapter) client(ctx context.Context) (*Client, error) {
	if a.clients == nil {
		return nil, fmt.Errorf("bridge: no client factory configured: %w", ramp.ErrRequestNotSent)
	}
	client, err := a.clients(ctx)
	if err != nil {
		return nil, fmt.Errorf("bridge: resolve client: %w: %w", ramp.ErrRequestNotSent, err)
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

// customerState maps a customer-shaped response onto the canonical snapshot.
func (a *Adapter) customerState(ctx context.Context, req Request) (*ramp.KycState, error) {
	resp, raw, err := ramp.Call[customerResponse](ctx, a.do, req)
	if err != nil {
		return nil, err
	}
	return stateFrom(resp, raw), nil
}

// stateFrom is the canonical snapshot of a customer.
func stateFrom(resp customerResponse, raw json.RawMessage) *ramp.KycState {
	rejection := resp.loggedRejection()
	return &ramp.KycState{
		ExternalCustomerID:  resp.ID,
		CustomerEmail:       resp.Email,
		Status:              kycStatusFromBridge(resp.Status),
		TosStatus:           tosStatusFromBridge(resp.HasAcceptedTermsOfService),
		Endorsements:        resp.endorsements(),
		Requirements:        resp.requirements(),
		RejectionReasons:    resp.rejectionMessages(),
		RejectionCategories: rejection.categories,
		RejectionDetails:    rejection.details,
		Raw:                 raw,
	}
}

// putCustomer updates a customer. Bridge treats the PUT as a patch and allows
// an Idempotency-Key on POST only (a customer PUT carrying one answers 422),
// so none is sent; replaying a patch of staged fields is harmless anyway.
//
// A customer Bridge is still processing refuses the update with
// resource_state_conflict (measured 01/10/2026 on documents sent moments after
// the create). That is a refusal of the timing, not of the body, so it reaches
// the caller as retryable. Mapped here rather than in sentinelForUpstream: it
// was only measured on this call, and on a transfer the same code may mean a
// state no retry will leave.
func (a *Adapter) putCustomer(ctx context.Context, id string, body any) (*ramp.KycState, error) {
	state, err := a.customerState(ctx, Request{Method: http.MethodPut, Path: customerPath(id), Body: body})
	return state, retryableOnStateConflict(err)
}

// retryableOnStateConflict marks Bridge's resource_state_conflict on an
// update as the refusal of the timing it is (see putCustomer).
func retryableOnStateConflict(err error) error {
	var upstreamErr *UpstreamError
	if errors.As(err, &upstreamErr) && upstreamErr.Code == "resource_state_conflict" {
		return fmt.Errorf("%w: %w", err, ramp.ErrRetryableUpstream)
	}
	return err
}

func customerPath(externalID string) string { return "/customers/" + externalID }

// findCustomerIDByEmail looks up a customer created outside our link row, for
// the duplicate-email recovery. The e-mail must match: it stops XIP from
// adopting another person's Bridge customer.
func (a *Adapter) findCustomerIDByEmail(ctx context.Context, email string) (string, error) {
	var resp customerListResponse
	req := Request{Method: http.MethodGet, Path: "/customers", Query: url.Values{"email": {email}}}
	if err := a.do(ctx, req, &resp); err != nil {
		return "", err
	}
	if len(resp.Data) == 0 || resp.Data[0].ID == "" || !strings.EqualFold(resp.Data[0].Email, email) {
		return "", errors.New("bridge: no customer for email")
	}
	return resp.Data[0].ID, nil
}

func isDuplicateEmailError(err error) bool {
	var upstreamErr *UpstreamError
	return errors.As(err, &upstreamErr) && upstreamErr.Code == "duplicate_email"
}

// hostedLinkPath scopes a verification page to an existing customer. The GET
// form takes a single endorsement.
func hostedLinkPath(externalID string, opts ramp.HostedLinkOpts) (string, url.Values) {
	query := url.Values{}
	if len(opts.Endorsements) > 0 {
		query.Set("endorsement", opts.Endorsements[0])
	}
	if opts.RedirectURI != "" {
		query.Set("redirect_uri", opts.RedirectURI)
	}
	return customerPath(externalID) + "/kyc_link", query
}

// appendRedirectURI adds redirect_uri to a hosted ToS URL that lacks it. The
// sandbox often answers session_token only, and without redirect_uri the user
// stays on the success screen.
func appendRedirectURI(rawURL, redirectURI string) (string, error) {
	if redirectURI == "" {
		return rawURL, nil
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("bridge: parse tos url: %w", err)
	}
	q := parsed.Query()
	if q.Get("redirect_uri") != "" {
		return rawURL, nil
	}
	q.Set("redirect_uri", redirectURI)
	parsed.RawQuery = q.Encode()
	return parsed.String(), nil
}

// addressFrom returns nil for an empty address, so a documents-only patch does
// not blank the customer's address.
func addressFrom(a ramp.Address) *addressPayload {
	if a == (ramp.Address{}) {
		return nil
	}
	return &addressPayload{
		StreetLine1: a.StreetLine1,
		StreetLine2: a.StreetLine2,
		City:        a.City,
		Subdivision: normalizeSubdivision(a.Country, a.Subdivision),
		PostalCode:  a.PostalCode,
		Country:     a.Country,
	}
}

// documentTypePassport is one page: Bridge takes its photograph as
// image_front alone.
const documentTypePassport = "passport"

// dashedUSTaxID spells an SSN or an ITIN as Bridge's examples do,
// NNN-NN-NNNN; one that is not nine digits goes trimmed.
func dashedUSTaxID(id string) string {
	d, ok := ustaxid.Digits(id)
	if !ok {
		return strings.TrimSpace(id)
	}
	return d[0:3] + "-" + d[3:5] + "-" + d[5:9]
}

func documentsFrom(docs []ramp.IdentifyingDocument) []identifyingInformation {
	if len(docs) == 0 {
		return nil
	}
	out := make([]identifyingInformation, 0, len(docs))
	for _, d := range docs {
		imageBack := d.ImageBack
		if d.Type == documentTypePassport {
			imageBack = ""
		}
		number := d.Number
		if d.Type == models.TaxIDTypeSSN || d.Type == models.TaxIDTypeITIN {
			// Bridge's examples spell it NNN-NN-NNNN. The sandbox took bare
			// digits too (14/10/2026): the dashes are the documented form.
			number = dashedUSTaxID(d.Number)
		}
		// The controller builds one entry even for a proof-of-address-only
		// submission. With no number and no images it names no document, and
		// an empty object on a patch is noise.
		if d.Number == "" && d.ImageFront == "" && imageBack == "" {
			continue
		}
		// Lower-case alpha-3: the spelling Bridge has always received, from
		// the tax id and from installed builds' documents alike.
		out = append(out, identifyingInformation{
			Type:           d.Type,
			IssuingCountry: strings.ToLower(d.IssuingCountry),
			Number:         number,
			ImageFront:     d.ImageFront,
			ImageBack:      imageBack,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func selfieImages(selfies []ramp.Selfie) []livenessSelfie {
	if len(selfies) == 0 {
		return nil
	}
	out := make([]livenessSelfie, 0, len(selfies))
	for _, s := range selfies {
		out = append(out, livenessSelfie{Image: s.Image})
	}
	return out
}

const purposeProofOfAddress = "proof_of_address"

// supportingDocuments is nil without a proof of address, so the patch does not
// mention the key.
func supportingDocuments(docs ramp.Documents) []documentPayload {
	if docs.ProofOfAddress == "" {
		return nil
	}
	return []documentPayload{{Purposes: []string{purposeProofOfAddress}, File: docs.ProofOfAddress}}
}

var (
	_ ramp.ProviderAdapter = (*Adapter)(nil)
	_ ramp.KycSimulator    = (*Adapter)(nil)
)
