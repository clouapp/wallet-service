package ramps_test

import (
	"context"
	"strings"
	"testing"

	"github.com/goravel/framework/database/orm"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/support"
)

// businessID is a company the user is a member of: its id is in the business
// range, and no user holds it.
const businessID = uint(1_000_000_000_007)

// companyCNPJ is the company's own CNPJ, as its Pix key is written, and
// companyLegalName the name it declared.
const (
	companyCNPJ      = "12ABC34501DE35"
	companyLegalName = "ACME TECNOLOGIA LTDA"
)

// theCompany is the company in status, holding companyCNPJ. Built inside a
// test, after the harness bound APP_KEY, which the digest is keyed by.
func theCompany(status string) *models.Customer {
	digest := repositories.BusinessTaxIDDigest(models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, companyCNPJ)
	return &models.Customer{
		Model: orm.Model{ID: businessID}, Type: models.CustomerTypeBusiness, Status: status, TaxIDHash: &digest,
	}
}

// registeredWallet is the registration the registry holds of address, from
// registered (address → customer), or nil.
func registeredWallet(registered map[string]uint, chain, address string) *models.CustomerWallet {
	customerID, ok := registered[address]
	if !ok {
		return nil
	}
	return &models.CustomerWallet{CustomerID: customerID, Chain: chain, Address: address}
}

// companyOfframpInput pays the company's own CNPJ key from wallet.
func companyOfframpInput() dto.EstimateOfframpDTO {
	in := offrampInput()
	in.PixKey, in.PixKeyType = companyCNPJ, "cnpj"
	return in
}

// companyOfframpEstimate is the company's quote of companyOfframpInput, the
// company named as the key's holder.
func companyOfframpEstimate() *models.RampEstimate {
	stored := storedOfframpEstimate()
	stored.CustomerID = businessID
	stored.RawResponse = strings.NewReplacer(`"pix_key":"`+pixKey+`"`, `"pix_key":"`+companyCNPJ+`"`,
		`"pix_key_type":"cpf"`, `"pix_key_type":"cnpj"`,
		`"chain":"solana"`, `"chain":"solana","recipient_name":"`+companyLegalName+`"`).Replace(stored.RawResponse)
	return stored
}

// seedCompany links the company at every provider of the rail, approved, under
// a provider customer of its own: what the router reads for it.
func (h *harness) seedCompany(t *testing.T) {
	t.Helper()
	for _, code := range h.lastProviders {
		h.integ.EXPECT().FindByCustomerAndProvider(businessID, code).Return(&models.CustomerProviderIntegration{
			CustomerID: businessID, Provider: code, Status: models.IntegrationStatusActive,
			ExternalCustomerID: support.Ptr("cus_company"), Metadata: "{}",
		}, nil).Maybe()
		approved := &models.CustomerProviderKyc{CustomerID: businessID, Provider: code, KycStatus: models.KycStatusApproved}
		require.NoError(t, approved.SetEndorsements([]models.Endorsement{
			{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
		}))
		h.kycs.EXPECT().FindByCustomerAndProvider(businessID, code).Return(approved, nil).Maybe()
	}
}

// theCompanys matches the provider's customer of the company, never the
// member's own.
func theCompanys(ref ramp.CustomerRef) bool {
	return ref.CustomerID == businessID && ref.ExternalID == "cus_company"
}

// A member's quote for the company prices the company's order: the provider is
// asked about the company's own customer there, and the quote is the
// company's, asked for by the member (spec §3.2).
func TestAQuoteForTheCompanyIsTheCompanysAskedForByTheMember(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderBridge)
	h.seedCompany(t)
	h.adapter.EXPECT().EstimateOnramp(testifymock.Anything, testifymock.MatchedBy(theCompanys), testifymock.Anything).
		Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })

	h.registered[wallet] = businessID

	_, err := h.onramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusActive), onrampInput())

	require.NoError(t, err)
	require.Equal(t, businessID, stored.CustomerID)
	require.Equal(t, userID, stored.UserID)
}

// The company's off-ramp is placed on the company's quote, at the company's
// provider customer, recorded as the company's order placed by the member,
// and the destination it registers is the company's.
func TestAnOfframpForTheCompanyIsTheCompanysPlacedByTheMember(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.seedCompany(t)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	h.registered[wallet] = businessID
	stored := companyOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(businessID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.MatchedBy(theCompanys), testifymock.Anything).
		Return(&ramp.RampOrder{
			ProviderOrderID: "trf_company", Status: models.TxStatusAwaitingFunds, ProviderDestinationRef: "ext_company",
		}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()

	_, created, err := h.offramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
		dto.CreateOfframpDTO{EstimateOfframpDTO: companyOfframpInput(), EstimateID: estimateID})

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, businessID, persisted.CustomerID)
	require.Equal(t, userID, persisted.UserID)
	require.Len(t, h.destinationLookups, 1)
	require.Equal(t, businessID, h.destinationLookups[0].CustomerID)
	require.Len(t, h.upsertedDestinations, 1)
	require.Equal(t, businessID, h.upsertedDestinations[0].CustomerID)
}

// Reads are the customer's: the company's orders through its scope, never the
// member's own.
func TestTheCompanysOrdersAreReadByTheCompany(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.txRepo.EXPECT().FindByCustomerAndExternalID(businessID, "order-1").Return(nil, nil).Once()

	_, err := h.offramp.Show(context.Background(), businessID, "order-1")

	require.ErrorIs(t, err, ramps.ErrOrderNotFound)
}

// eligibleCompany makes the provider pass the whole chain for the company.
func (h *vaHarness) eligibleCompany(t *testing.T, code string) {
	t.Helper()
	h.integ.EXPECT().FindByCustomerAndProvider(businessID, code).Return(&models.CustomerProviderIntegration{
		CustomerID: businessID, Provider: code, Status: models.IntegrationStatusActive,
		ExternalCustomerID: support.Ptr("cus_company"), Metadata: "{}",
	}, nil).Maybe()
	approved := &models.CustomerProviderKyc{CustomerID: businessID, Provider: code, KycStatus: models.KycStatusApproved}
	require.NoError(t, approved.SetEndorsements([]models.Endorsement{
		{Name: models.EndorsementPix, Status: models.EndorsementStatusApproved},
	}))
	h.kycs.EXPECT().FindByCustomerAndProvider(businessID, code).Return(approved, nil).Maybe()
}

// The company's standing account is the company's, provisioned by the member:
// the reservation, the lock and the idempotency key are the company's, and the
// provider opens it on the company's own customer (spec §18.6).
func TestAStandingAccountForTheCompanyIsTheCompanysProvisionedByTheMember(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.eligibleCompany(t, models.ProviderBridge)
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(businessID, ramps.ClientSourceCurrency).Return(nil, nil).Times(3)
	h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(businessID, ramps.ClientSourceCurrency).Return(nil, nil).Once()
	var reserved models.CustomerVirtualAccount
	h.accounts.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) {
			row := args.Get(0).(*models.CustomerVirtualAccount)
			row.ID = 99
			reserved = *row
		})
	h.standing.MockVirtualAccountAdapter.EXPECT().
		ProvisionVirtualAccount(testifymock.Anything, testifymock.MatchedBy(theCompanys),
			testifymock.MatchedBy(func(req ramp.VirtualAccountRequest) bool {
				return req.IdempotencyKey == "va:1000000000007:brl:99"
			})).
		Return(bridgeVirtualAccount(), nil).Once()
	h.accounts.EXPECT().FindByID(uint(99)).RunAndReturn(func(uint) (*models.CustomerVirtualAccount, error) {
		row := reserved
		return &row, nil
	}).Once()
	h.accounts.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.registered[wallet] = businessID

	_, created, err := h.service.Provision(context.Background(), user, theCompany(models.CustomerStatusActive), provisionInput())

	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, businessID, reserved.CustomerID)
	require.Equal(t, userID, reserved.UserID, "the member who provisioned it")
	require.Equal(t, "provisioning:1000000000007:brl", reserved.ExternalAccountID)
}

// --- the money rules of a business, in the contract's order (§10) ------------

// noProviderAsked asserts no provider was asked to price or place anything.
func (h *harness) noProviderAsked(t *testing.T) {
	t.Helper()
	for _, method := range []string{"EstimateOnramp", "EstimateOfframp", "CreateOnramp", "CreateOfframp"} {
		h.adapter.AssertNotCalled(t, method, testifymock.Anything, testifymock.Anything, testifymock.Anything)
		h.second.AssertNotCalled(t, method, testifymock.Anything, testifymock.Anything, testifymock.Anything)
	}
}

// A company's quote is refused before any provider is asked: a company not
// active yet hears kyc_pending, the refusal the app already opens the KYB
// with; an address it did not register is wallet_not_registered; a payout key
// that is not its own CNPJ is holder_mismatch — each after the router's
// gates, so a company the provider has not linked hears that first. A refusal
// of the customer is not failed over to the next provider.
func TestACompanysQuoteIsRefusedInTheContractsOrder(t *testing.T) {
	cases := map[string]struct {
		rail     string
		status   string
		linked   bool
		register bool
		input    func() dto.EstimateOfframpDTO
		want     error
	}{
		"draft deposit":                     {rail: models.RailPixPayin, status: models.CustomerStatusDraft, linked: true, want: ramp.ErrKycPending},
		"deposit to an unregistered wallet": {rail: models.RailPixPayin, status: models.CustomerStatusActive, linked: true, want: ramps.ErrWalletNotRegistered},
		"unlinked company, unregistered":    {rail: models.RailPixPayin, status: models.CustomerStatusActive, want: ramp.ErrIntegrationRequired},
		"payout from an unregistered wallet": {rail: models.RailPixPayout, status: models.CustomerStatusActive, linked: true,
			input: offrampInput, want: ramps.ErrWalletNotRegistered},
		"payout to a CPF key": {rail: models.RailPixPayout, status: models.CustomerStatusActive, linked: true, register: true,
			input: offrampInput, want: ramps.ErrHolderMismatch},
		"payout to another CNPJ": {rail: models.RailPixPayout, status: models.CustomerStatusActive, linked: true, register: true,
			input: func() dto.EstimateOfframpDTO {
				in := companyOfframpInput()
				in.PixKey = "11222333000181"
				return in
			}, want: ramps.ErrHolderMismatch},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, c.rail, models.ProviderBridge, models.ProviderOzav)
			if c.linked {
				h.seedCompany(t)
			} else {
				h.integ.EXPECT().FindByCustomerAndProvider(businessID, testifymock.Anything).Return(nil, nil).Maybe()
			}
			if c.register {
				h.registered[wallet] = businessID
			}
			company := theCompany(c.status)

			var err error
			if c.rail == models.RailPixPayin {
				_, err = h.onramp.Estimate(context.Background(), user, company, onrampInput())
			} else {
				_, err = h.offramp.Estimate(context.Background(), user, company, c.input())
			}

			require.ErrorIs(t, err, c.want)
			h.noProviderAsked(t)
			h.estimateRepo.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// The company's own CNPJ key, from a wallet it registered, is quoted.
func TestACompanysPayoutToItsOwnCNPJIsQuoted(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.seedCompany(t)
	h.registered[wallet] = businessID
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.MatchedBy(theCompanys), testifymock.Anything).
		Return(bridgeEstimate(), nil).Once()
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	in := companyOfframpInput()
	in.PixKey = "12.abc.345/01de-35"
	_, err := h.offramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusActive), in)

	require.NoError(t, err)
}

// A company's payout spends no MFA attempt on a refusal: its step-up comes
// after its local gates and its money rules, and before anything is reserved.
// A person's step-up still comes first, as it always did.
func TestACompanysPayoutAsksForTheStepUpLast(t *testing.T) {
	t.Run("a key not its own is refused before the step-up", func(t *testing.T) {
		h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
		h.seedCompany(t)
		h.registered[wallet] = businessID
		// A quote no estimate would give — the estimate refuses the key too —
		// bound to the company as the holder, so the create's own refusal is
		// what answers.
		stored := storedOfframpEstimate()
		stored.CustomerID = businessID
		stored.RawResponse = strings.Replace(stored.RawResponse, `"chain":"solana"`,
			`"chain":"solana","recipient_name":"`+companyLegalName+`"`, 1)
		h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(stored, nil).Once()
		h.txRepo.EXPECT().FindByCustomerAndEstimate(businessID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
			Return(nil, nil).Twice()

		_, _, err := h.offramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
			dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})

		require.ErrorIs(t, err, ramps.ErrHolderMismatch)
		h.mfa.AssertNotCalled(t, "Enrolled", testifymock.Anything)
		h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
	})
	t.Run("a local gate is answered before the money rules", func(t *testing.T) {
		h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
		h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(nil, nil).Once()

		_, _, err := h.offramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
			dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})

		require.ErrorIs(t, err, ramps.ErrEstimateNotFound)
		h.mfa.AssertNotCalled(t, "Enrolled", testifymock.Anything)
	})
	t.Run("its own key is asked for the step-up before anything is reserved", func(t *testing.T) {
		h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
		h.seedCompany(t)
		h.registered[wallet] = businessID
		stored := companyOfframpEstimate()
		h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(stored, nil).Once()
		h.txRepo.EXPECT().FindByCustomerAndEstimate(businessID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
			Return(nil, nil).Twice()
		h.mfa.EXPECT().Enrolled(user).Return(true, nil).Once()

		_, _, err := h.offramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
			dto.CreateOfframpDTO{EstimateOfframpDTO: companyOfframpInput(), EstimateID: estimateID})

		require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
		h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
	})
	t.Run("a person's step-up still comes first", func(t *testing.T) {
		h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
		h.mfa.EXPECT().Enrolled(user).Return(true, nil).Once()

		_, _, err := h.offramp.Create(context.Background(), user, person,
			dto.CreateOfframpDTO{EstimateOfframpDTO: offrampInput(), EstimateID: estimateID})

		require.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)
		h.estimateRepo.AssertNotCalled(t, "FindByCustomerAndExternalID", testifymock.Anything, testifymock.Anything)
	})
}

// A company's order-first deposit to an address it did not register reserves
// nothing.
func TestACompanysDepositOrderToAnUnregisteredWalletIsRefused(t *testing.T) {
	h := newHarness(t, models.RailPixPayin, models.ProviderOzav)
	h.seedCompany(t)
	stored := storedOnrampEstimate()
	stored.CustomerID = businessID
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(businessID, models.ProviderOzav, stored.ID, models.TransactionTypeOnramp).
		Return(nil, nil).Twice()

	_, _, err := h.onramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
		dto.CreateOnrampDTO{EstimateOnrampDTO: onrampInput(), EstimateID: estimateID})

	require.ErrorIs(t, err, ramps.ErrWalletNotRegistered)
	h.txRepo.AssertNotCalled(t, "Create", testifymock.Anything)
	h.noProviderAsked(t)
}

// A company's standing account delivers only to an address it registered,
// and a company not active yet is told kyc_pending; nothing is reserved.
func TestACompanysStandingAccountIsRefusedBeforeItIsReserved(t *testing.T) {
	for name, c := range map[string]struct {
		status string
		want   error
	}{
		"draft":                {models.CustomerStatusDraft, ramp.ErrKycPending},
		"unregistered address": {models.CustomerStatusActive, ramps.ErrWalletNotRegistered},
	} {
		t.Run(name, func(t *testing.T) {
			h := newVirtualAccountHarness(t, models.ProviderBridge)
			h.eligibleCompany(t, models.ProviderBridge)
			h.accounts.EXPECT().FindActiveByCustomerAndCurrency(businessID, ramps.ClientSourceCurrency).Return(nil, nil).Twice()
			h.accounts.EXPECT().FindProvisioningByCustomerAndCurrency(businessID, ramps.ClientSourceCurrency).Return(nil, nil).Once()

			_, _, err := h.service.Provision(context.Background(), user, theCompany(c.status), provisionInput())

			require.ErrorIs(t, err, c.want)
			h.accounts.AssertNotCalled(t, "Create", testifymock.Anything)
		})
	}
}

// Pointing a company's account at an address it did not register is refused
// before the step-up; a person's update asks for the step-up first, as it
// always did.
func TestACompanysAccountUpdateAsksForTheStepUpLast(t *testing.T) {
	h := newVirtualAccountHarness(t, models.ProviderBridge)
	h.eligibleCompany(t, models.ProviderBridge)
	account := storedAccount(models.ProviderBridge)
	account.CustomerID = businessID
	h.accounts.EXPECT().FindActiveByCustomerAndCurrency(businessID, ramps.ClientSourceCurrency).Return(account, nil).Once()

	_, err := h.service.UpdateDestination(context.Background(), user, theCompany(models.CustomerStatusActive),
		updateInput("NewWalletAddress", ""))

	require.ErrorIs(t, err, ramps.ErrWalletNotRegistered)
	h.mfa.AssertNotCalled(t, "Enrolled", testifymock.Anything)
	h.standing.MockVirtualAccountAdapter.AssertNotCalled(t, "UpdateVirtualAccount",
		testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything)
}

// A company's payout names the company as the key's holder, whatever the
// request typed: the key is the company's own CNPJ, and the provider records
// the holder XIP sends (API contract §10). The quote and the order are bound
// to it, so the create redeems the quote it was given.
func TestACompanysPayoutNamesTheCompanyAsTheHolder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.seedCompany(t)
	h.registered[wallet] = businessID
	h.adapter.EXPECT().EstimateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpQuoteRequest) bool {
			return req.Destination.AccountOwnerName == companyLegalName
		})).Return(bridgeEstimate(), nil).Once()
	var stored *models.RampEstimate
	h.estimateRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { stored = args.Get(0).(*models.RampEstimate) })
	in := companyOfframpInput()
	in.RecipientName = "Fulano de Tal"

	_, err := h.offramp.Estimate(context.Background(), user, theCompany(models.CustomerStatusActive), in)

	require.NoError(t, err)
	require.Contains(t, stored.RawResponse, `"recipient_name":"`+companyLegalName+`"`)
}

func TestACompanysPayoutOrderNamesTheCompanyAsTheHolder(t *testing.T) {
	h := newHarness(t, models.RailPixPayout, models.ProviderBridge)
	h.seedCompany(t)
	h.mfa.EXPECT().Enrolled(testifymock.Anything).Return(false, nil).Once()
	h.registered[wallet] = businessID
	stored := companyOfframpEstimate()
	h.estimateRepo.EXPECT().FindByCustomerAndExternalID(businessID, estimateID).Return(stored, nil).Once()
	h.txRepo.EXPECT().FindByCustomerAndEstimate(businessID, models.ProviderBridge, stored.ID, models.TransactionTypeOfframp).
		Return(nil, nil).Twice()
	var persisted *models.Transaction
	h.txRepo.EXPECT().Create(testifymock.Anything).Return(nil).Once().
		Run(func(args testifymock.Arguments) { persisted = args.Get(0).(*models.Transaction) })
	h.adapter.EXPECT().CreateOfframp(testifymock.Anything, testifymock.Anything,
		testifymock.MatchedBy(func(req ramp.OfframpCreateRequest) bool {
			return req.Destination.AccountOwnerName == companyLegalName
		})).Return(&ramp.RampOrder{ProviderOrderID: "trf_company", Status: models.TxStatusAwaitingFunds}, nil).Once()
	h.txRepo.EXPECT().Update(testifymock.Anything).Return(nil).Once()
	h.estimateRepo.EXPECT().MarkConsumed(stored).Return(nil).Once()
	h.broadcaster.EXPECT().Publish(testifymock.Anything, testifymock.Anything).Maybe()
	in := companyOfframpInput()
	in.RecipientName = "Fulano de Tal"

	_, _, err := h.offramp.Create(context.Background(), user, theCompany(models.CustomerStatusActive),
		dto.CreateOfframpDTO{EstimateOfframpDTO: in, EstimateID: estimateID})

	require.NoError(t, err)
	require.Equal(t, companyLegalName, *persisted.RecipientName)
}
