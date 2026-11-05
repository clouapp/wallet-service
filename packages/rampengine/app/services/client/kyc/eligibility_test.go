package kyc_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	kycflags "github.com/macrowallets/waas/packages/rampengine/app/featureflags/kyc"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/packages/featureflag/contracts"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// policed is a mock adapter that declares a jurisdiction policy, the way the
// real Bridge and Ozav adapters do.
type policed struct {
	*mocks.MockProviderAdapter
	rails func(ramp.TaxIdentity, string) []ramp.Rail
}

func (a policed) RailsFor(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	return a.rails(identity, residence)
}

// policyOf is Bridge's or Ozav's policy in miniature (countries_test.go):
// Bridge serves a CPF or the passport the residence issued anywhere but CUB
// and USA, Ozav only a CPF holder living in Brazil.
func policyOf(code string) func(ramp.TaxIdentity, string) []ramp.Rail {
	if code == models.ProviderOzav {
		return ozavLike().rails
	}
	return bridgeLike().rails
}

// policedAll wraps each mock adapter in its provider's policy.
func policedAll(adapters []ramp.ProviderAdapter) []ramp.ProviderAdapter {
	out := make([]ramp.ProviderAdapter, 0, len(adapters))
	for _, adapter := range adapters {
		m := adapter.(*mocks.MockProviderAdapter)
		out = append(out, policed{m, policyOf(m.Code())})
	}
	return out
}

// withPolicies rebuilds the service over the same collaborators with every
// registered adapter under its provider's policy.
func (h *fanOutHarness) withPolicies() {
	h.registered = policedAll(h.registered)
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.registered...), Config: h.config, Linker: h.linker,
		Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Members: h.members, Integrations: h.integRepo,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
	})
}

// withPolicies registers the harness's adapter as Bridge and a fresh mock as
// Ozav, each under its policy, and answers the Ozav mock.
func (h *harness) withPolicies(t *testing.T) *mocks.MockProviderAdapter {
	t.Helper()
	ozav := mocks.NewMockProviderAdapter(t)
	ozav.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(policed{h.adapter, policyOf(models.ProviderBridge)}, policed{ozav, policyOf(models.ProviderOzav)}),
		Config:  ramp.NewConfig(enabledSettings(t, models.ProviderBridge, models.ProviderOzav)),
		Linker:  h.linker, Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Members: h.members, Integrations: h.integRepo,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
	})
	return ozav
}

// enabledSettings is a settings source with codes enabled in sandbox and
// every other provider off, answering however often it is asked: the
// single-provider routes refuse a provider that is switched off.
func enabledSettings(t *testing.T, codes ...string) *mocks.MockSettingsSource {
	t.Helper()
	settings := mocks.NewMockSettingsSource(t)
	for _, code := range ramp.AllProviderCodes() {
		group := ramp.ProviderGroup(code)
		settings.EXPECT().Bool(group, ramp.SettingEnabled).Return(slices.Contains(codes, code), nil).Maybe()
		settings.EXPECT().String(group, ramp.SettingMode).Return(models.ProviderModeSandbox, nil).Maybe()
	}
	return settings
}

// An Argentine passport held in Argentina, a CPF holder living in Brazil
// (an installed build's profile: no type, no issuing country) and a resident
// of Cuba, as the retained profile declares them.
var (
	retainedPassport = &ramp.Profile{TaxID: "AB123456", TaxIDType: models.TaxIDTypePassport, TaxIDCountry: "ARG",
		Address: ramp.Address{Country: "ARG"}}
	retainedInstalledBuild = &ramp.Profile{TaxID: "39053344705", Address: ramp.Address{Country: "BRA"}}
	retainedCuba           = &ramp.Profile{TaxID: "AB123456", TaxIDType: models.TaxIDTypePassport, TaxIDCountry: "CUB",
		Address: ramp.Address{Country: "CUB"}}
)

func providersOf(rows []models.CustomerProviderKyc) []string {
	out := []string{}
	for _, row := range rows {
		out = append(out, row.Provider)
	}
	return out
}

// --- the application fan-out -------------------------------------------------

// A provider takes only the people it can serve: Ozav serves a CPF holder
// living in Brazil, so a passport never reaches it — not as a refusal it
// would answer upstream after the documents, but as no call at all.
func TestSubmitApplicationToEnabledReachesOnlyTheProvidersThatServeTheApplicant(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7),
		applicationWithIdentity("AB123456", "passport", "ARG", "ARG"))

	require.NoError(t, err)
	require.Len(t, outcomes, 1, "a provider that does not serve the applicant is no outcome at all: %+v", outcomes)
	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	require.True(t, outcomes[0].Accepted())
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, uint(7), models.ProviderOzav)
}

// An installed build's application — no tax_id_type, country BRA — reaches
// both, as it always has.
func TestSubmitApplicationToEnabledStillReachesBothForAnInstalledBuild(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")
	h.expectAcceptedSubmit(models.ProviderOzav, "stg_ozav")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7),
		applicationWithIdentity("39053344705", "", "", "BRA"))

	require.NoError(t, err)
	require.Len(t, outcomes, 2)
}

// Nobody enabled serves the applicant: refused before anything — no profile
// retained, no integration opened, nothing staged, not even the providers'
// rows read.
func TestSubmitApplicationToEnabledRefusesAnApplicantNoProviderServesBeforeAnything(t *testing.T) {
	for name, input := range map[string]dto.ApplicationDTO{
		"a passport, living in cuba": applicationWithIdentity("AB123456", "passport", "CUB", "CUB"),
		"a cpf, living in cuba":      applicationWithIdentity("39053344705", "cpf", "BRA", "CUB"),
	} {
		t.Run(name, func(t *testing.T) {
			h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
			h.withPolicies()
			h.expectAccountEmail()

			outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), input)

			require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed)
			require.Empty(t, outcomes)
			h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
			h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			h.kycRepo.AssertNotCalled(t, "ListByUser", testifymock.Anything)
			h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
		})
	}
}

// With nothing enabled the answer stays no_provider_available: nobody is
// refusing the applicant, there is simply nobody.
func TestSubmitApplicationToEnabledWithNothingEnabledIsStillNoProviderAvailable(t *testing.T) {
	h := newFanOutHarness(t)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.expectProfileUpsert()

	_, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7),
		applicationWithIdentity("AB123456", "passport", "CUB", "CUB"))

	require.ErrorIs(t, err, ramp.ErrNoProviderAvailable)
}

// --- the documents fan-out -----------------------------------------------------

// The documents carry no identity: who they are for is the retained profile.
func TestSubmitDocumentsToEnabledReachesOnlyTheProvidersThatServeTheRetainedPerson(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.retention.profile = retainedPassport
	h.seedSnapshot(nil, models.ProviderBridge, models.ProviderOzav)
	h.expectAcceptedDocumentsSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())

	require.NoError(t, err)
	require.Len(t, outcomes, 1)
	require.Equal(t, models.ProviderBridge, outcomes[0].Provider)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, uint(7), models.ProviderOzav)
}

func TestSubmitDocumentsToEnabledRefusesAPersonNoProviderServes(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.retention.profile = retainedCuba

	outcomes, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())

	require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed)
	require.Empty(t, outcomes)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
}

// Failing to read who the documents are for is not passing the check: the
// passport would reach Ozav.
func TestSubmitDocumentsToEnabledFailsWhenTheRetainedProfileCannotBeRead(t *testing.T) {
	h := newHarness(t)
	broken := errors.New("user kyc profile repo: connection reset")
	h.retention.readErr = broken

	_, err := h.svc.SubmitDocumentsToEnabled(context.Background(), h.user, 7, fullDocuments())

	require.ErrorIs(t, err, broken)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// --- the terms fan-out -------------------------------------------------------

// The terms come before the application, so who they are for is what the
// request declares: a passport is offered Bridge's terms, never Ozav's.
func TestStartTosForEnabledOffersOnlyTheTermsOfProvidersServingTheDeclaredPerson(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.seedProviderStates()
	h.expectTosSession(models.ProviderBridge, &ramp.TosSession{URL: "https://bridge.example/tos"}, nil)

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{
		Modes: []string{kyc.TosModeHosted}, Country: "ARG", TaxIDType: "passport", TaxIDCountry: "ARG",
	})

	require.NoError(t, err)
	require.Equal(t, []kyc.TosSessionView{
		{Provider: models.ProviderBridge, URL: "https://bridge.example/tos", Mode: kyc.TosModeHosted},
	}, sessions)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, uint(7), models.ProviderOzav)
}

// An installed build declares no one, and is offered every provider's terms
// as before.
func TestStartTosForEnabledWithoutADeclarationOffersEveryProvidersTerms(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.retention.profile = retainedPassport
	h.seedProviderStates()
	h.expectTosSession(models.ProviderBridge, &ramp.TosSession{URL: "https://bridge.example/tos"}, nil)
	h.expectTosSession(models.ProviderOzav, &ramp.TosSession{URL: "https://ozav.example/tos"}, nil)

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{})

	require.NoError(t, err)
	require.Len(t, sessions, 2, "not the retained profile: the request is what declares who the terms are for")
}

func TestStartTosForEnabledRefusesADeclaredPersonNoProviderServes(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()

	sessions, err := h.svc.StartTosForEnabled(context.Background(), 7, dto.StartTosDTO{Country: "CUB"})

	require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed)
	require.Empty(t, sessions)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.kycRepo.AssertNotCalled(t, "ListByUser", testifymock.Anything)
}

// --- the single-provider routes ------------------------------------------------

var errStopHere = errors.New("past the eligibility check")

// The named provider passes the same test the fan-out applies: Ozav refuses a
// passport before an integration is opened; Bridge takes it on.
func TestSubmitApplicationRefusesANamedProviderThatDoesNotServeTheApplicant(t *testing.T) {
	h := newHarness(t)
	h.withPolicies(t)
	passport := applicationWithIdentity("AB123456", "passport", "ARG", "ARG")

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderOzav, passport)

	require.ErrorIs(t, err, kyc.ErrJurisdictionNotServed)
	h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
	h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(nil, errStopHere).Once()

	_, err = h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, passport)
	require.ErrorIs(t, err, errStopHere, "Bridge serves the passport")
}

// The routes that carry no identity — documents, terms, a hosted link,
// endorsements — read who they are for from the retained profile: each opens
// state at the provider, so a provider that does not serve the person is
// refused before it is asked. Without a profile the route reads as it did.
func TestTheSingleProviderRoutesWithoutAnIdentityReadTheRetainedPerson(t *testing.T) {
	routes := map[string]func(h *harness) error{
		"documents": func(h *harness) error {
			_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderOzav, fullDocuments())
			return err
		},
		"terms": func(h *harness) error {
			_, err := h.svc.StartTos(context.Background(), 7, models.ProviderOzav, "xip://kyc/tos")
			return err
		},
		"hosted link": func(h *harness) error {
			_, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderOzav, dto.HostedLinkDTO{FullName: "Luiza Souza"})
			return err
		},
		"endorsements": func(h *harness) error {
			_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderOzav, []string{models.EndorsementPix})
			return err
		},
	}
	for name, call := range routes {
		t.Run(name+", a retained passport", func(t *testing.T) {
			h := newHarness(t)
			ozav := h.withPolicies(t)
			h.retention.profile = retainedPassport

			require.ErrorIs(t, call(h), kyc.ErrJurisdictionNotServed)
			h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			h.integRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
			ozav.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
		t.Run(name+", no retained profile", func(t *testing.T) {
			h := newHarness(t)
			ozav := h.withPolicies(t)
			ozav.EXPECT().RequiresTos().Return(true).Maybe()
			h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderOzav).Return(nil, nil).Maybe()
			// Past the check, each route's first step toward the provider.
			h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderOzav).Return(nil, errStopHere).Maybe()
			h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderOzav).Return(nil, errStopHere).Maybe()

			require.ErrorIs(t, call(h), errStopHere)
		})
	}
}

// retainedUS is a resident of California who declared an SSN.
var retainedUS = &ramp.Profile{TaxID: "287654321", TaxIDType: models.TaxIDTypeSSN, TaxIDCountry: "USA",
	Address: ramp.Address{Country: "USA", Subdivision: "CA"}}

// servesUS is Bridge's policy with the United States opened, so what refuses
// a US resident below is the flag, not the provider.
func servesUS(identity ramp.TaxIdentity, residence string) []ramp.Rail {
	return append(bridgeLike().rails(identity, residence), usLike().rails(identity, residence)...)
}

// withUSResidence registers the harness's adapter as a Bridge serving the
// United States, reading features.
func (h *harness) withUSResidence(t *testing.T, features contracts.Feature) {
	t.Helper()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(policed{h.adapter, servesUS}),
		Config:  ramp.NewConfig(bridgeAloneEnabled(t)),
		Linker:  h.linker, Kyc: h.kycRepo, Profiles: h.profileRepo, Persons: h.persons, Members: h.members, Integrations: h.integRepo,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
		Features: features,
	})
}

// pastTheCheck lets each route take its first step toward Bridge, which stops
// it there.
func (h *harness) pastTheCheck() {
	h.adapter.EXPECT().RequiresTos().Return(true).Maybe()
	h.kycRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, nil).Maybe()
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderBridge).Return(nil, errStopHere).Maybe()
	h.integRepo.EXPECT().FindByCustomerAndProvider(uint(7), models.ProviderBridge).Return(nil, errStopHere).Maybe()
}

// While its flag is off, a retained US resident is refused on every
// single-provider route as the documents fan-out refuses them, before Bridge
// is asked — Bridge itself would serve the SSN. Decided (PR #115, C3): the
// switch closes the residence on every KYC write; reads and the rails of an
// approved customer never read it.
func TestTheSingleProviderRoutesRefuseARetainedUSResidentWhileItsFlagIsOff(t *testing.T) {
	routes := map[string]func(h *harness) error{
		"documents": func(h *harness) error {
			_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderBridge, fullDocuments())
			return err
		},
		"terms": func(h *harness) error {
			_, err := h.svc.StartTos(context.Background(), 7, models.ProviderBridge, "xip://kyc/tos")
			return err
		},
		"hosted link": func(h *harness) error {
			_, err := h.svc.HostedLink(context.Background(), h.user, 7, models.ProviderBridge, dto.HostedLinkDTO{FullName: "Jane Doe"})
			return err
		},
		"endorsements": func(h *harness) error {
			_, err := h.svc.RequestEndorsements(context.Background(), 7, models.ProviderBridge, []string{models.EndorsementPix})
			return err
		},
	}
	storeDown := func(t *testing.T) contracts.Feature {
		f := mocks.NewMockFeature(t)
		f.EXPECT().IsActive(testifymock.Anything, kycflags.USAResidenceEnabled, nil).Return(false, errors.New("store down"))
		return f
	}
	for name, call := range routes {
		for state, features := range map[string]func(t *testing.T) contracts.Feature{
			"flag off":            func(t *testing.T) contracts.Feature { return usaSwitch(t, false) },
			"wired without flags": func(*testing.T) contracts.Feature { return nil },
		} {
			t.Run(name+", "+state, func(t *testing.T) {
				h := newHarness(t)
				h.withUSResidence(t, features(t))
				h.retention.profile = retainedUS

				require.ErrorIs(t, call(h), kyc.ErrJurisdictionNotServed)
				h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
				h.integRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
				h.adapter.AssertNotCalled(t, "CreateTosSession", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			})
		}
		t.Run(name+", flag on", func(t *testing.T) {
			h := newHarness(t)
			h.withUSResidence(t, usaSwitch(t, true))
			h.retention.profile = retainedUS
			h.pastTheCheck()

			require.ErrorIs(t, call(h), errStopHere)
		})
		t.Run(name+", a flag store that cannot answer", func(t *testing.T) {
			h := newHarness(t)
			h.withUSResidence(t, storeDown(t))
			h.retention.profile = retainedUS

			err := call(h)

			require.Error(t, err)
			require.NotErrorIs(t, err, kyc.ErrJurisdictionNotServed)
			h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
		})
		t.Run(name+", a residence no flag holds reads no flag", func(t *testing.T) {
			h := newHarness(t)
			h.withUSResidence(t, mocks.NewMockFeature(t)) // any read fails the test
			h.retention.profile = retainedInstalledBuild
			h.pastTheCheck()

			require.ErrorIs(t, call(h), errStopHere)
		})
	}
}

// --- the listings --------------------------------------------------------------

// GET /api/kyc lists the providers the app builds its wizard from, so it
// lists only the ones that serve the person the retained profile declares —
// a step owed to a provider no submission reaches could never be satisfied.
// No profile yet, everyone enabled, as before.
func TestListShowsOnlyTheProvidersThatServeTheRetainedPerson(t *testing.T) {
	for name, tc := range map[string]struct {
		retained *ramp.Profile
		want     []string
	}{
		"no retained profile":       {nil, []string{models.ProviderBridge, models.ProviderOzav}},
		"an installed build's cpf":  {retainedInstalledBuild, []string{models.ProviderBridge, models.ProviderOzav}},
		"a passport":                {retainedPassport, []string{models.ProviderBridge}},
		"a residence nobody serves": {retainedCuba, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
			h.withPolicies()
			h.allowTos()
			h.retention.profile = tc.retained
			h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
				providerState(models.ProviderOzav, models.KycStatusIncomplete),
			}, nil).Once()
			h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Maybe()

			rows, err := h.svc.List(7)

			require.NoError(t, err)
			require.Equal(t, tc.want, providersOf(rows))
		})
	}
}

// The listings' rails_open agrees with the router: a rail the retained
// person's declaration does not open at a provider is marked on its row,
// whatever the endorsements clear. Bridge's umbrella pix clears a passport's
// deposit, and the router refuses it (rail_not_eligible) — an overview
// calling it open would send the app's gate back to the form after the 409.
func TestTheListingsMarkTheRailsTheRetainedPersonIsNotEligibleFor(t *testing.T) {
	approvedEverywhere := providerState(models.ProviderBridge, models.KycStatusApproved)
	require.NoError(t, approvedEverywhere.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
	}))
	entrances := map[string]func(*kyc.ProviderKycService) ([]models.CustomerProviderKyc, error){
		"List": func(svc *kyc.ProviderKycService) ([]models.CustomerProviderKyc, error) { return svc.List(7) },
		"Overview": func(svc *kyc.ProviderKycService) ([]models.CustomerProviderKyc, error) {
			view, err := svc.Overview(7)
			if err != nil {
				return nil, err
			}
			return view.Providers, nil
		},
	}
	for entrance, rowsOf := range entrances {
		for name, tc := range map[string]struct {
			retained *ramp.Profile
			want     []string
		}{
			"no retained profile": {nil, nil},
			// The miniature Bridge (bridgeLike) opens no USD rail, so the
			// listings mark every USD rail beside the Pix ones it refuses.
			"an installed build's cpf": {retainedInstalledBuild, models.USDRails()},
			"a passport":               {retainedPassport, append([]string{models.RailPixPayin}, models.USDRails()...)},
		} {
			t.Run(entrance+"/"+name, func(t *testing.T) {
				h := newFanOutHarness(t, models.ProviderBridge)
				h.withPolicies()
				h.allowTos()
				h.retention.profile = tc.retained
				h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{approvedEverywhere}, nil).Once()
				h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
					linkFor(models.ProviderBridge, "cus_bridge"),
				}, nil).Maybe()
				h.staging.EXPECT().InFlightKind(uint(7), testifymock.Anything).Return("").Maybe()

				rows, err := rowsOf(h.svc)

				require.NoError(t, err)
				require.Len(t, rows, 1)
				require.Equal(t, tc.want, rows[0].RailsNotEligible)
			})
		}
	}
}

func TestOverviewFoldsOnlyTheProvidersThatServeTheRetainedPerson(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.allowTos()
	h.retention.profile = retainedPassport
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
	h.inFlight(models.ProviderBridge, "")

	view, err := h.svc.Overview(7)

	require.NoError(t, err)
	require.Equal(t, []string{models.ProviderBridge}, providersOf(view.Providers))
}

// Decided (spec §4.3): the overview folds only the providers that serve the
// retained person, so an approved row at a provider that does not — Ozav, for
// someone who now declares a passport — does not make the user approved. They
// are asked for what Bridge, the provider that serves them, still owes.
func TestOverviewIsNotApprovedByAProviderThatDoesNotServeTheRetainedPerson(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	h.allowTos()
	h.retention.profile = retainedPassport
	h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderKyc{
		providerState(models.ProviderOzav, models.KycStatusApproved),
	}, nil).Once()
	h.integRepo.EXPECT().ListByCustomer(uint(7)).Return([]models.CustomerProviderIntegration{
		linkFor(models.ProviderOzav, "ozav_cus_1"),
	}, nil).Once()
	// Nothing in flight anywhere — asked of any provider, so that a leaked
	// Ozav row fails on the status below rather than on a mock.
	h.staging.EXPECT().InFlightKind(uint(7), testifymock.Anything).Return("").Maybe()

	view, err := h.svc.Overview(7)

	require.NoError(t, err)
	require.NotEqual(t, kyc.OverallApproved, view.Overall.Status, "the hidden approved Ozav row must not approve the user")
	require.Equal(t, []string{models.ProviderBridge}, providersOf(view.Providers))
	require.Equal(t, kyc.OverallActionRequired, view.Overall.Status)
}

func TestListFailsWhenTheRetainedProfileCannotBeRead(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
	h.withPolicies()
	broken := errors.New("user kyc profile repo: connection reset")
	h.retention.readErr = broken

	_, err := h.svc.List(7)

	require.ErrorIs(t, err, broken)
}

// The banner invites a tap toward a provider's wizard: never toward one that
// does not serve the user.
func TestPendingProvidersAreOnlyTheOnesServingTheRetainedPerson(t *testing.T) {
	for name, tc := range map[string]struct {
		retained *ramp.Profile
		want     []string
	}{
		"no retained profile": {nil, []string{models.ProviderBridge, models.ProviderOzav}},
		"a passport":          {retainedPassport, []string{models.ProviderBridge}},
	} {
		t.Run(name, func(t *testing.T) {
			h := newCapHarness(t)
			h.enable(models.ProviderBridge)
			h.enable(models.ProviderOzav)
			h.disableEveryUndeclaredProvider(models.ProviderBridge, models.ProviderOzav)
			h.adapters = policedAll(h.adapters)
			h.retained = tc.retained
			h.alwaysResolves(ramp.ErrIntegrationRequired)
			h.seedRows()

			view, err := h.service().Compute(context.Background(), 1, 1)

			require.NoError(t, err)
			got := []string{}
			for _, pending := range view.PendingProviders {
				got = append(got, pending.Provider)
			}
			require.Equal(t, tc.want, got)
		})
	}
}

// --- a provider that serves only bank rails -------------------------------

// bankOnly is a mock adapter that opens the bank rails to anyone and gates
// Pix on a declared residence, the way BlindPay does before it opens Pix: no
// declaration opens it a Pix rail, so it is never in the Pix wizard.
type bankOnly struct{ *mocks.MockProviderAdapter }

func (bankOnly) RailsFor(ramp.TaxIdentity, string) []ramp.Rail { return ramp.BankRails() }
func (bankOnly) PixNeedsDeclaredResidence() bool               { return true }

// withBankOnlyOzav registers the harness's adapter as Bridge (no policy) and a
// fresh mock as a bank-rails-only provider coded Ozav.
func (h *harness) withBankOnlyOzav(t *testing.T) {
	t.Helper()
	other := mocks.NewMockProviderAdapter(t)
	other.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	h.svc = kyc.NewProviderKycService(kyc.Deps{
		Factory: ramp.NewFactory(h.adapter, bankOnly{other}),
		Config:  ramp.NewConfig(enabledSettings(t, models.ProviderBridge, models.ProviderOzav)),
		Linker:  h.linker, Kyc: h.kycRepo, Profiles: h.profileRepo, Integrations: h.integRepo, Persons: h.persons, Members: h.members,
		Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
	})
}

// A bank-rails-only provider is not in the Pix wizard's listings, whoever the
// person is, while the Pix providers still are.
func TestListLeavesAProviderWithoutPixRailsOutOfThePixWizard(t *testing.T) {
	for name, retained := range map[string]*ramp.Profile{
		"no retained profile": nil,
		"a cpf holder":        retainedInstalledBuild,
	} {
		t.Run(name, func(t *testing.T) {
			h := newFanOutHarness(t, models.ProviderBridge, models.ProviderOzav)
			h.registered = []ramp.ProviderAdapter{h.registered[0], bankOnly{h.registered[1].(*mocks.MockProviderAdapter)}}
			h.svc = kyc.NewProviderKycService(kyc.Deps{
				Factory: ramp.NewFactory(h.registered...), Config: h.config, Linker: h.linker,
				Kyc: h.kycRepo, Profiles: h.profileRepo, Integrations: h.integRepo, Persons: h.persons, Members: h.members,
				Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
			})
			h.allowTos()
			h.retention.profile = retained
			h.kycRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Once()
			h.integRepo.EXPECT().ListByCustomer(uint(7)).Return(nil, nil).Maybe()

			rows, err := h.svc.List(7)

			require.NoError(t, err)
			require.Equal(t, []string{models.ProviderBridge}, providersOf(rows))
		})
	}
}

// The single-provider route still opens to it: its bank rails serve the person
// the path names, so refuseUnserved lets the call through.
func TestSubmitApplicationOpensToAProviderWithoutPixRailsNamedByThePath(t *testing.T) {
	h := newHarness(t)
	h.withBankOnlyOzav(t)
	h.linker.EXPECT().Start(testifymock.Anything, uint(7), models.ProviderOzav).Return(nil, errStopHere).Once()

	_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderOzav,
		applicationWithIdentity("39053344705", "cpf", "BRA", "BRA"))

	require.ErrorIs(t, err, errStopHere)
	require.NotErrorIs(t, err, kyc.ErrJurisdictionNotServed)
}

// A provider an operator switched off is refused up front by every route that
// names it, with the fan-out's no_provider_available, before anything is read
// or opened.
func TestTheSingleProviderRoutesRefuseASwitchedOffProvider(t *testing.T) {
	routes := map[string]func(h *harness) error{
		"terms": func(h *harness) error {
			_, err := h.svc.StartTos(context.Background(), 7, models.ProviderOzav, "xip://kyc/tos")
			return err
		},
		"application": func(h *harness) error {
			_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderOzav, fullApplication())
			return err
		},
		"documents": func(h *harness) error {
			_, err := h.svc.SubmitDocuments(context.Background(), h.user, 7, models.ProviderOzav, fullDocuments())
			return err
		},
	}
	for name, call := range routes {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.withPolicies(t)
			h.svc = kyc.NewProviderKycService(kyc.Deps{
				Factory: ramp.NewFactory(h.adapter, mocksOzav(t)),
				Config:  ramp.NewConfig(enabledSettings(t, models.ProviderBridge)),
				Linker:  h.linker, Kyc: h.kycRepo, Profiles: h.profileRepo, Integrations: h.integRepo, Persons: h.persons, Members: h.members,
				Staging: h.staging, Dispatcher: h.dispatcher, Locker: passthroughLocker{}, Broadcaster: h.broadcaster, Notifier: h.notifier,
			})

			require.ErrorIs(t, call(h), ramp.ErrNoProviderAvailable)
			h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
		})
	}
}

func mocksOzav(t *testing.T) *mocks.MockProviderAdapter {
	t.Helper()
	ozav := mocks.NewMockProviderAdapter(t)
	ozav.EXPECT().Code().Return(models.ProviderOzav).Maybe()
	return ozav
}
