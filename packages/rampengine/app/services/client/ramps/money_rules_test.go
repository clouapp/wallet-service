package ramps

import (
	"errors"
	"testing"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// companyCNPJ is the company's own CNPJ, written as its Pix key would be; the
// alphanumeric example the Serpro published.
const companyCNPJ = "12ABC34501DE35"

func company(t *testing.T) *models.Customer {
	t.Helper()
	mock.Factory().Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()
	digest := repositories.BusinessTaxIDDigest(models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, companyCNPJ)
	return &models.Customer{Type: models.CustomerTypeBusiness, Status: models.CustomerStatusActive, TaxIDHash: &digest}
}

func person(id uint) *models.Customer {
	return &models.Customer{Model: orm.Model{ID: id}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

func companyWithID(t *testing.T, id uint) *models.Customer {
	c := company(t)
	c.ID = id
	return c
}

// A business deposits only to, and withdraws only from, an address it
// registered (spec §9.1, §9.3): one nobody registered, or another customer
// did, is wallet_not_registered. The lookup is the one registration wrote,
// trimmed as registration trims it.
func TestOwnWalletRefusesAnAddressTheBusinessDidNotRegister(t *testing.T) {
	const address = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"
	business := companyWithID(t, 1_000_000_000_001)
	for name, tc := range map[string]struct {
		registered *models.CustomerWallet
		want       error
	}{
		"registered to it":      {&models.CustomerWallet{CustomerID: business.ID, Chain: models.ChainSolana, Address: address}, nil},
		"registered to another": {&models.CustomerWallet{CustomerID: 7, Chain: models.ChainSolana, Address: address}, ErrWalletNotRegistered},
		"registered to no one":  {nil, ErrWalletNotRegistered},
	} {
		t.Run(name, func(t *testing.T) {
			wallets := mocks.NewMockCustomerWalletRepository(t)
			wallets.EXPECT().FindByAddress(models.ChainSolana, address).Return(tc.registered, nil).Once()
			err := holderRules{wallets: wallets}.ownWallet(business, models.ChainSolana, " "+address+" ")
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.want)
		})
	}
}

// A person is never asked: none of their money rules read the registry
// (contract §10, "PF: nothing changes").
func TestOwnWalletNeverAsksAboutAPerson(t *testing.T) {
	wallets := mocks.NewMockCustomerWalletRepository(t)
	require.NoError(t, holderRules{wallets: wallets}.ownWallet(person(7), models.ChainSolana, "any"))
}

func TestOwnWalletSaysWhenItCouldNotRead(t *testing.T) {
	wallets := mocks.NewMockCustomerWalletRepository(t)
	boom := errors.New("db down")
	wallets.EXPECT().FindByAddress(models.ChainSolana, "addr").Return(nil, boom).Once()
	err := holderRules{wallets: wallets}.ownWallet(companyWithID(t, 1_000_000_000_001), models.ChainSolana, "addr")
	assert.ErrorIs(t, err, boom)
	assert.NotErrorIs(t, err, ErrWalletNotRegistered)
}

// A business pays out only to its own CNPJ key (spec §9.2, §18.8): the key,
// however it is written, digests to the document the company is held by. Any
// other CNPJ, and any other key type — whose document is only what the user
// typed — is holder_mismatch, one code for both.
func TestOwnPixKeyAcceptsOnlyTheCompanysOwnCNPJ(t *testing.T) {
	business := company(t)
	for name, tc := range map[string]struct {
		keyType, key string
		ok           bool
	}{
		"its own CNPJ":                       {"cnpj", companyCNPJ, true},
		"its own CNPJ, punctuated and lower": {"cnpj", "12.abc.345/01de-35", true},
		"another company's CNPJ":             {"cnpj", "11222333000181", false},
		"a CPF key":                          {"cpf", "39053344705", false},
		"an e-mail key":                      {"email", "finance@acme.com", false},
		"a phone key":                        {"phone", "+5511999998888", false},
		"a random key":                       {"evp", "c0ffee00-0000-4000-8000-000000000000", false},
	} {
		t.Run(name, func(t *testing.T) {
			err := ownPixKey(business, tc.keyType, tc.key)
			if tc.ok {
				require.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, ErrHolderMismatch)
		})
	}
}

// A business that holds no document — a company from abroad never has a
// CNPJ — has no key of its own to pay to.
func TestOwnPixKeyRefusesABusinessWithNoDocument(t *testing.T) {
	business := company(t)
	business.TaxIDHash = nil
	assert.ErrorIs(t, ownPixKey(business, "cnpj", companyCNPJ), ErrHolderMismatch)
}

func TestOwnPixKeyNeverAsksAboutAPerson(t *testing.T) {
	require.NoError(t, ownPixKey(person(7), "email", "a@x.com"))
}

// A money rule's refusal is about the customer: the next provider in the rail
// order would refuse it the same way, so an estimate does not fail over on it.
func TestARefusalOfTheCustomerDoesNotFailOver(t *testing.T) {
	assert.False(t, failoverUnlessRefused(ErrWalletNotRegistered))
	assert.False(t, failoverUnlessRefused(ErrHolderMismatch))
	assert.True(t, failoverUnlessRefused(errors.New("provider timeout")))
	assert.False(t, failoverUnlessRefused(nil))
}

// A company that is not active moves no money (a draft — approved nowhere
// yet, or approved and kept a draft because another company holds its CNPJ):
// it hears kyc_pending, the refusal the app already opens the KYB with, before
// any other money rule. A closed one is refused before it gets here
// (customer_closed). A person is never asked.
func TestAMoneyRuleRefusesACompanyThatIsNotActive(t *testing.T) {
	draft := companyWithID(t, 1_000_000_000_001)
	draft.Status = models.CustomerStatusDraft
	wallets := mocks.NewMockCustomerWalletRepository(t)
	rules := holderRules{wallets: wallets}

	assert.ErrorIs(t, rules.onramp(draft, models.ChainSolana, "addr"), ramp.ErrKycPending)
	assert.ErrorIs(t, rules.offramp(draft, models.ChainSolana, "addr", "cpf", "39053344705"), ramp.ErrKycPending)
	assert.False(t, failoverUnlessRefused(rules.onramp(draft, models.ChainSolana, "addr")))
	require.NoError(t, rules.onramp(person(7), models.ChainSolana, "addr"))
}

// activationAnswer is a BusinessActivation answering active, or not.
type activationAnswer struct {
	active bool
	asked  []uint
}

func (a *activationAnswer) ActivateIfApproved(customerID uint) (bool, error) {
	a.asked = append(a.asked, customerID)
	return a.active, nil
}

// A draft a provider approved, whose activation the verdict could not make —
// it failed, or a rival's document was released since — is reconciled by the
// money rule itself (round-2 1): activated, it moves money; still a draft, it
// hears kyc_pending.
func TestAMoneyRuleReconcilesAnApprovedDraftFirst(t *testing.T) {
	draft := companyWithID(t, 1_000_000_000_001)
	draft.Status = models.CustomerStatusDraft
	wallets := mocks.NewMockCustomerWalletRepository(t)
	wallets.EXPECT().FindByAddress(models.ChainSolana, "addr").
		Return(&models.CustomerWallet{CustomerID: draft.ID, Chain: models.ChainSolana, Address: "addr"}, nil).Once()
	approved := &activationAnswer{active: true}

	require.NoError(t, holderRules{wallets: wallets, activation: approved}.onramp(draft, models.ChainSolana, "addr"))
	assert.Equal(t, []uint{draft.ID}, approved.asked)
	assert.Equal(t, models.CustomerStatusActive, draft.Status, "the rest of the request reads it active")

	other := companyWithID(t, 1_000_000_000_002)
	other.Status = models.CustomerStatusDraft
	pending := &activationAnswer{active: false}
	assert.ErrorIs(t, holderRules{wallets: wallets, activation: pending}.onramp(other, models.ChainSolana, "addr"), ramp.ErrKycPending)
}
