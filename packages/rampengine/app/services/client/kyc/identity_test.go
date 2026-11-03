package kyc_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// retention is the retained profile and the uniqueness index as a harness's
// profileRepo answers them for user 7. Every application now reads both
// before anything else; the zero value — nothing retained, nothing taken — is
// what every test not about uniqueness needs, and the ones about it set these.
type retention struct {
	profile *ramp.Profile
	readErr error
	taken   error
}

func wireRetention(repo *mocks.MockCustomerIndividualProfileRepository) *retention {
	r := &retention{}
	repo.EXPECT().Find(uint(7)).
		RunAndReturn(func(uint) (*ramp.Profile, error) { return r.profile, r.readErr }).Maybe()
	repo.EXPECT().CheckIdentityAvailable(uint(7), testifymock.Anything).
		RunAndReturn(func(uint, ramp.Profile) error { return r.taken }).Maybe()
	return r
}

// entrances are the two application entrances. Both check before anything:
// the single-provider route saves no profile, but it still sends the CPF.
var entrances = map[string]func(h *harness, input dto.ApplicationDTO) error{
	"fan-out": func(h *harness, input dto.ApplicationDTO) error {
		outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), input)
		if err != nil && len(outcomes) > 0 {
			return fmt.Errorf("refused per provider, not before the fan-out: %w", err)
		}
		return err
	},
	"single provider": func(h *harness, input dto.ApplicationDTO) error {
		_, err := h.svc.SubmitApplication(context.Background(), h.user, support.IndividualCustomer(7), models.ProviderBridge, input)
		return err
	},
}

// submitters are the entrances fed an installed build's application declaring
// taxID and app's phone.
var submitters = map[string]func(h *harness, app ramp.Profile, taxID string) error{
	"fan-out": func(h *harness, app ramp.Profile, taxID string) error {
		return entrances["fan-out"](h, applicationDeclaring(taxID, app.Phone))
	},
	"single provider": func(h *harness, app ramp.Profile, taxID string) error {
		return entrances["single provider"](h, applicationDeclaring(taxID, app.Phone))
	},
}

func applicationDeclaring(taxID, phone string) (input dto.ApplicationDTO) {
	input = fullApplication()
	input.TaxID = taxID
	input.Phone = phone
	input.Country = "BRA"
	return input
}

// Nothing is written and nothing reaches a provider: no integration opened,
// no profile retained, nothing staged or queued. A CPF another account holds
// is the refusal nothing fixes; a phone is a field the user changes.
func TestAnApplicationDeclaringAnotherAccountsIdentityIsRefusedBeforeAnything(t *testing.T) {
	for _, taken := range []error{kyc.ErrTaxIDInUse, kyc.ErrPhoneInUse} {
		for name, submit := range submitters {
			t.Run(fmt.Sprintf("%v, %s", taken, name), func(t *testing.T) {
				h := newHarness(t)
				h.retention.taken = taken

				err := submit(h, ramp.Profile{Phone: "11987654321"}, "390.533.447-05")

				require.ErrorIs(t, err, taken)
				h.profileRepo.AssertCalled(t, "CheckIdentityAvailable", uint(7),
					ramp.Profile{TaxID: "390.533.447-05", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5511987654321"})
				h.profileRepo.AssertNotCalled(t, "Upsert", testifymock.Anything, testifymock.Anything)
				h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
				h.kycRepo.AssertNotCalled(t, "FindByCustomerAndProvider", testifymock.Anything, testifymock.Anything)
				h.kycRepo.AssertNotCalled(t, "ListByUser", testifymock.Anything)
				h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
				h.dispatcher.AssertNotCalled(t, "Dispatch", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			})
		}
	}
}

// What is checked is what the submission stands for: the CPF and phone it
// carries, or — when it carries none — the ones the retained profile already
// declares. A submission that omits the CPF is still, to every provider that
// has it, the person with that CPF. The tax id travels with its type and the
// country that issued it: the request's, or the ones retained with it —
// derived, on a profile older than them, from the country it was declared
// under (the address's, on one older still).
func TestTheIdentityCheckedIsTheOneTheSubmissionStandsFor(t *testing.T) {
	retained := &ramp.Profile{TaxID: "39053344705", TaxIDCountry: "BRA", Phone: "+5511987654321",
		Address: ramp.Address{Country: "PRT"}}
	legacy := &ramp.Profile{TaxID: "39053344705", Phone: "+5511987654321", Address: ramp.Address{Country: "BRA"}}
	foreign := &ramp.Profile{TaxID: "X1234567Z", TaxIDCountry: "ESP", Phone: "+5511987654321",
		Address: ramp.Address{Country: "ESP"}}
	for _, tc := range []struct {
		name         string
		retained     *ramp.Profile
		taxID, phone string
		want         ramp.Profile
	}{
		{"carried", retained, "11122233396", "21999998888",
			ramp.Profile{TaxID: "11122233396", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5521999998888"}},
		// Declared in Brazil and carried to an address in Portugal: still a CPF.
		{"retained", retained, "", "",
			ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5511987654321"}},
		{"cpf retained, phone carried", retained, "", "21999998888",
			ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5521999998888"}},
		{"retained before the country was", legacy, "", "",
			ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA", Phone: "+5511987654321"}},
		{"a foreign id retained before the type was", foreign, "", "",
			ramp.Profile{TaxID: "X1234567Z", TaxIDType: "passport", TaxIDCountry: "ESP", Phone: "+5511987654321"}},
	} {
		for name, submit := range submitters {
			t.Run(tc.name+", "+name, func(t *testing.T) {
				h := newHarness(t)
				h.retention.profile = tc.retained
				h.retention.taken = kyc.ErrTaxIDInUse

				err := submit(h, ramp.Profile{Phone: tc.phone}, tc.taxID)

				require.ErrorIs(t, err, kyc.ErrTaxIDInUse)
				h.profileRepo.AssertCalled(t, "CheckIdentityAvailable", uint(7), tc.want)
			})
		}
	}
}

// The retained declaration keeps the CPF and phone a submission omits, so the
// digests the index holds never drop a CPF the providers still know the user
// by — which would free it for another account.
func TestSubmitApplicationToEnabledKeepsTheRetainedIdentityASubmissionOmits(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.retention.profile = &ramp.Profile{TaxID: "39053344705", TaxIDCountry: "BRA", Phone: "+5511987654321"}
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.MatchedBy(func(profile ramp.Profile) bool {
		return profile.TaxID == "39053344705" && profile.TaxIDType == "cpf" && profile.TaxIDCountry == "BRA" &&
			profile.Phone == "+5511987654321" && profile.FirstName == "Luiza"
	})).Return(nil).Once()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), fullApplication())

	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
}

// Two accounts can both pass the check; the unique index refuses the later
// retention, and that refusal is the same answer — the fan-out never runs, so
// the CPF the check could not see in time reaches no provider either.
func TestSubmitApplicationToEnabledStopsWhenTheIndexRefusesTheRetention(t *testing.T) {
	for _, taken := range []error{kyc.ErrTaxIDInUse, kyc.ErrPhoneInUse} {
		t.Run(taken.Error(), func(t *testing.T) {
			h := newHarness(t)
			h.profileRepo.EXPECT().Upsert(uint(7), testifymock.Anything).
				Return(fmt.Errorf("user kyc profile repo: create: %w", taken)).Once()

			outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), applicationDeclaring("39053344705", "11987654321"))

			require.ErrorIs(t, err, taken)
			require.Empty(t, outcomes)
			h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
			h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
		})
	}
}

// Failing to check is not passing the check: the one thing the check exists
// for is keeping a CPF from reaching a provider twice. That holds for reading
// the retained declaration a submission falls back on, too.
func TestAnApplicationWhoseIdentityCannotBeCheckedIsNotSent(t *testing.T) {
	broken := errors.New("user kyc profile repo: connection reset")
	for failure, fail := range map[string]func(*retention){
		"check":            func(r *retention) { r.taken = broken },
		"retained profile": func(r *retention) { r.readErr = broken },
	} {
		for name, submit := range submitters {
			t.Run(failure+", "+name, func(t *testing.T) {
				h := newHarness(t)
				fail(h.retention)

				err := submit(h, ramp.Profile{}, "")

				require.ErrorIs(t, err, broken)
				h.linker.AssertNotCalled(t, "Start", testifymock.Anything, testifymock.Anything, testifymock.Anything)
				h.staging.AssertNotCalled(t, "Put", testifymock.Anything)
			})
		}
	}
}

// applicationWithIdentity is fullApplication declaring a tax id of the given
// type and issuing country, by someone living in residence.
func applicationWithIdentity(taxID, taxIDType, taxIDCountry, residence string) dto.ApplicationDTO {
	input := fullApplication()
	input.TaxID, input.TaxIDType, input.TaxIDCountry, input.Country = taxID, taxIDType, taxIDCountry, residence
	return input
}

// The tax id is checked as what it is — its type and the country that issued
// it — never by where the person lives. An installed build sends neither and
// always BRA, and is read as a CPF, as before; a CPF declared by someone living
// in Portugal is still a CPF, which is what reserves it against an account
// declaring the same CPF from Brazil.
func TestTheIdentityCheckedIsTheDeclaredOneNotTheResidence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input dto.ApplicationDTO
		want  ramp.Profile
	}{
		{"an installed build", applicationWithIdentity("39053344705", "", "", "BRA"),
			ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA"}},
		{"a cpf living abroad", applicationWithIdentity("39053344705", "cpf", "", "GBR"),
			ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA"}},
		{"a passport", applicationWithIdentity("AB123456", "passport", "ARG", "GBR"),
			ramp.Profile{TaxID: "AB123456", TaxIDType: "passport", TaxIDCountry: "ARG"}},
	} {
		for name, submit := range entrances {
			t.Run(tc.name+", "+name, func(t *testing.T) {
				h := newHarness(t)
				h.retention.taken = kyc.ErrTaxIDInUse

				err := submit(h, tc.input)

				require.ErrorIs(t, err, kyc.ErrTaxIDInUse)
				h.profileRepo.AssertCalled(t, "CheckIdentityAvailable", uint(7), tc.want)
			})
		}
	}
}

// What the providers receive and what is retained are the same identity the
// check reserved: the document typed by it, and the profile keeping its type
// and issuing country apart from the address.
func TestSubmitApplicationToEnabledSendsAndRetainsTheDeclaredIdentity(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    dto.ApplicationDTO
		document ramp.IdentifyingDocument
		retained func(ramp.Profile) bool
	}{
		{"an installed build", applicationWithIdentity("39053344705", "", "", "BRA"),
			ramp.IdentifyingDocument{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"},
			func(p ramp.Profile) bool {
				return p.TaxID == "39053344705" && p.TaxIDType == "cpf" && p.TaxIDCountry == "BRA" && p.Address.Country == "BRA"
			}},
		{"a cpf living abroad", applicationWithIdentity("39053344705", "cpf", "", "GBR"),
			ramp.IdentifyingDocument{Type: "cpf", IssuingCountry: "BRA", Number: "39053344705"},
			func(p ramp.Profile) bool {
				return p.TaxID == "39053344705" && p.TaxIDType == "cpf" && p.TaxIDCountry == "BRA" && p.Address.Country == "GBR"
			}},
		{"a passport", applicationWithIdentity("AB123456", "passport", "ARG", "GBR"),
			ramp.IdentifyingDocument{Type: "passport", IssuingCountry: "ARG", Number: "AB123456"},
			func(p ramp.Profile) bool {
				return p.TaxID == "AB123456" && p.TaxIDType == "passport" && p.TaxIDCountry == "ARG" && p.Address.Country == "GBR"
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newFanOutHarness(t, models.ProviderBridge)
			h.seedProviderStates()
			h.expectAccountEmail()
			h.profileRepo.EXPECT().Upsert(uint(7), testifymock.MatchedBy(tc.retained)).Return(nil).Once()
			h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

			outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7), tc.input)

			require.NoError(t, err)
			require.True(t, outcomes[0].Accepted())
			require.Equal(t, []ramp.IdentifyingDocument{tc.document}, stagedApplicationOf(t, h.staging).IdentifyingInformation)
		})
	}
}

// No tax_id_locked (spec §3.3): an account that declared a CPF may declare a
// passport in a new application, and the other way round — passport to CPF is
// the path to deposits. The new identity is the one checked, sent and
// retained.
func TestARetainedCPFDoesNotLockTheNextApplicationToIt(t *testing.T) {
	h := newFanOutHarness(t, models.ProviderBridge)
	h.seedProviderStates()
	h.expectAccountEmail()
	h.retention.profile = &ramp.Profile{TaxID: "39053344705", TaxIDType: "cpf", TaxIDCountry: "BRA",
		Phone: "+5511987654321", Address: ramp.Address{Country: "BRA"}}
	h.profileRepo.EXPECT().Upsert(uint(7), testifymock.MatchedBy(func(p ramp.Profile) bool {
		return p.TaxID == "AB123456" && p.TaxIDType == "passport" && p.TaxIDCountry == "ARG"
	})).Return(nil).Once()
	h.expectAcceptedSubmit(models.ProviderBridge, "stg_bridge")

	outcomes, err := h.svc.SubmitApplicationToEnabled(context.Background(), h.user, support.IndividualCustomer(7),
		applicationWithIdentity("AB123456", "passport", "ARG", "GBR"))

	require.NoError(t, err)
	require.True(t, outcomes[0].Accepted())
	h.profileRepo.AssertCalled(t, "CheckIdentityAvailable", uint(7), ramp.Profile{
		TaxID: "AB123456", TaxIDType: "passport", TaxIDCountry: "ARG", Phone: "+5511987654321"})
	require.Equal(t, []ramp.IdentifyingDocument{{Type: "passport", IssuingCountry: "ARG", Number: "AB123456"}},
		stagedApplicationOf(t, h.staging).IdentifyingInformation)
}

// stagedApplicationOf is the application the one staged submission carries —
// what the worker hands the provider.
func stagedApplicationOf(t *testing.T, staging *mocks.MockStaging) ramp.Application {
	t.Helper()
	var staged []kyc.StagedSubmission
	for _, call := range staging.Calls {
		if call.Method == "Put" {
			staged = append(staged, call.Arguments.Get(0).(kyc.StagedSubmission))
		}
	}
	require.Len(t, staged, 1)
	require.NotNil(t, staged[0].Application)
	return *staged[0].Application
}
