package ramps

import (
	"context"
	"errors"
	"testing"

	"github.com/goravel/framework/database/orm"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/mfa"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

type walletHarness struct {
	wallets    *mocks.MockCustomerWalletRepository
	accounts   *mocks.MockCustomerVirtualAccountRepository
	approval   *mocks.MockCustomerApproval
	stepUp     *mocks.MockMfaStepUpValidator
	activation BusinessActivation
}

func newWalletHarness(t *testing.T) *walletHarness {
	t.Helper()
	return &walletHarness{
		wallets:  mocks.NewMockCustomerWalletRepository(t),
		accounts: mocks.NewMockCustomerVirtualAccountRepository(t),
		approval: mocks.NewMockCustomerApproval(t),
		stepUp:   mocks.NewMockMfaStepUpValidator(t),
	}
}

func (h *walletHarness) service() *WalletService {
	return NewWalletService(WalletDeps{Wallets: h.wallets, Accounts: h.accounts, Approval: h.approval, MfaStepUp: h.stepUp,
		Activation: h.activation})
}

const walletAddress = "9xQeWvG816bUx9EPjHmaT23yvVM2ZWbrrpZb9PusVFin"

func walletUser() *models.User {
	user := &models.User{Handle: "ana"}
	user.ID = 7
	return user
}

func acmeCustomer() *models.Customer {
	return &models.Customer{Model: orm.Model{ID: 1_000_000_000_007}, Type: models.CustomerTypeBusiness, Status: models.CustomerStatusActive}
}

func individualOf(user *models.User) *models.Customer {
	return &models.Customer{Model: orm.Model{ID: user.ID}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

func registration(code string) dto.RegisterWalletDTO {
	return dto.RegisterWalletDTO{Chain: models.ChainSolana, Address: walletAddress, Code: code, CodeType: "totp"}
}

func (h *walletHarness) stepUpPasses(user *models.User) {
	h.stepUp.EXPECT().Enrolled(user).Return(true, nil).Once()
	h.stepUp.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "123456", "totp").Return(nil).Once()
}

func (h *walletHarness) addressIsFree(customerID uint) {
	h.wallets.EXPECT().FindByAddress(models.ChainSolana, walletAddress).Return(nil, nil).Once()
	h.accounts.EXPECT().DestinationOfAnother(customerID, models.ChainSolana, walletAddress).Return(false, nil).Once()
}

// An approved company registers a new address after the step-up, the member
// who registered it recorded.
func TestWalletService_Register_AnApprovedBusinessRegistersAnAddress(t *testing.T) {
	h := newWalletHarness(t)
	user, acme := walletUser(), acmeCustomer()
	h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
	h.stepUpPasses(user)
	h.addressIsFree(acme.ID)
	h.wallets.EXPECT().Create(&models.CustomerWallet{
		CustomerID: acme.ID, Chain: models.ChainSolana, Address: walletAddress, RegisteredByUserID: user.ID,
	}).Return(nil).Once()

	wallet, created, err := h.service().Register(context.Background(), user, acme, registration("123456"))

	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, acme.ID, wallet.CustomerID)
}

// A company not approved anywhere hears the KYC refusal before the step-up:
// no MFA attempt is spent on a registration that cannot pass.
func TestWalletService_Register_ABusinessShortOfApprovalIsRefusedFirst(t *testing.T) {
	h := newWalletHarness(t)
	acme := acmeCustomer()
	pending := &ramp.KycBlocked{Provider: models.ProviderBridge, PendingSteps: []string{"owners"}, Err: ramp.ErrKycPending}
	h.approval.EXPECT().RequireApproved(acme.ID).Return(pending).Once()

	_, _, err := h.service().Register(context.Background(), walletUser(), acme, registration("123456"))

	assert.Same(t, pending, errors.Unwrap(err))
	assert.ErrorIs(t, err, ramp.ErrKycPending)
}

// A company the overview reads approved that is not active — a draft another
// company's activation outran, or one the approval has not reached yet —
// registers nothing: kyc_pending, before the step-up, as the money writes of
// a business that is not active refuse it.
func TestWalletService_Register_ABusinessNotActiveIsPending(t *testing.T) {
	h := newWalletHarness(t)
	acme := acmeCustomer()
	acme.Status = models.CustomerStatusDraft
	h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
	h.activation = &activationAnswer{active: false}

	_, _, err := h.service().Register(context.Background(), walletUser(), acme, registration("123456"))

	assert.ErrorIs(t, err, ramp.ErrKycPending)
	h.stepUp.AssertNotCalled(t, "Enrolled", testifymock.Anything)
}

// An approved draft the verdict could not activate — the activation failed,
// or a rival's document was released since — is activated by the
// registration itself, and registers (round-2 1b).
func TestWalletService_Register_AnApprovedDraftIsReconciledAndRegisters(t *testing.T) {
	h := newWalletHarness(t)
	user, acme := walletUser(), acmeCustomer()
	acme.Status = models.CustomerStatusDraft
	h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
	h.activation = &activationAnswer{active: true}
	h.stepUpPasses(user)
	h.addressIsFree(acme.ID)
	h.wallets.EXPECT().Create(testifymock.Anything).Return(nil).Once()

	_, created, err := h.service().Register(context.Background(), user, acme, registration("123456"))

	require.NoError(t, err)
	assert.True(t, created)
}

// A person registers no wallet (round-2 review, item 2): no rule of theirs
// reads the registry, the app registers only a company's, and an address a
// person took would be a company's lost for good — the unique is global. It
// is refused before anything else is spent or read.
func TestWalletService_Register_APersonIsRefused(t *testing.T) {
	h := newWalletHarness(t)
	user := walletUser()

	_, _, err := h.service().Register(context.Background(), user, individualOf(user), registration(""))

	assert.ErrorIs(t, err, ErrWalletNotForPerson)
	h.stepUp.AssertNotCalled(t, "Enrolled", testifymock.Anything)
	h.wallets.AssertNotCalled(t, "Create", testifymock.Anything)
}

// The step-up is the user's, exactly as the deposit account's: no code with
// MFA enrolled is mfa_required, a wrong one mfa_invalid, and nothing is read
// of the address before it passes.
func TestWalletService_Register_TheStepUpComesBeforeTheAddress(t *testing.T) {
	h := newWalletHarness(t)
	user, acme := walletUser(), acmeCustomer()
	h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Twice()
	h.stepUp.EXPECT().Enrolled(user).Return(true, nil).Twice()
	h.stepUp.EXPECT().ValidateStepUpCode(testifymock.Anything, user, "000000", "totp").Return(mfa.ErrInvalidCode).Once()

	_, _, err := h.service().Register(context.Background(), user, acme, registration(""))
	assert.ErrorIs(t, err, ramp.ErrMfaStepUpRequired)

	_, _, err = h.service().Register(context.Background(), user, acme, registration("000000"))
	assert.ErrorIs(t, err, mfa.ErrInvalidCode)
}

// An address another customer registered, or another customer's standing
// account delivers to, belongs to that holder.
func TestWalletService_Register_AnAddressAnotherHolderHoldsIsInUse(t *testing.T) {
	user, acme := walletUser(), acmeCustomer()
	for name, arrange := range map[string]func(h *walletHarness){
		"registered to another customer": func(h *walletHarness) {
			h.wallets.EXPECT().FindByAddress(models.ChainSolana, walletAddress).
				Return(&models.CustomerWallet{CustomerID: 1_000_000_000_008}, nil).Once()
		},
		"another customer's deposit destination": func(h *walletHarness) {
			h.wallets.EXPECT().FindByAddress(models.ChainSolana, walletAddress).Return(nil, nil).Once()
			h.accounts.EXPECT().DestinationOfAnother(acme.ID, models.ChainSolana, walletAddress).Return(true, nil).Once()
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newWalletHarness(t)
			h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
			h.stepUpPasses(user)
			arrange(h)

			_, _, err := h.service().Register(context.Background(), user, acme, registration("123456"))

			assert.ErrorIs(t, err, ErrWalletInUse)
		})
	}
}

// The customer's own address again answers its registration, unchanged — after
// the step-up, which a registration always costs.
func TestWalletService_Register_TheCustomersOwnAddressAgainIsIdempotent(t *testing.T) {
	h := newWalletHarness(t)
	user, acme := walletUser(), acmeCustomer()
	h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
	h.stepUpPasses(user)
	mine := &models.CustomerWallet{CustomerID: acme.ID, Chain: models.ChainSolana, Address: walletAddress}
	h.wallets.EXPECT().FindByAddress(models.ChainSolana, walletAddress).Return(mine, nil).Once()

	wallet, created, err := h.service().Register(context.Background(), user, acme, registration("123456"))

	require.NoError(t, err)
	assert.False(t, created)
	assert.Same(t, mine, wallet)
}

// Two registrations racing for one address: the unique settles it, and the
// loser reads who won — its own customer is the 200 a retry would get, any
// other the address in use.
func TestWalletService_Register_ALostRaceReadsTheWinner(t *testing.T) {
	user, acme := walletUser(), acmeCustomer()
	for name, tc := range map[string]struct {
		winner  uint
		wantErr error
	}{
		"the same customer": {acme.ID, nil},
		"another customer":  {1_000_000_000_008, ErrWalletInUse},
	} {
		t.Run(name, func(t *testing.T) {
			h := newWalletHarness(t)
			h.approval.EXPECT().RequireApproved(acme.ID).Return(nil).Once()
			h.stepUpPasses(user)
			h.addressIsFree(acme.ID)
			h.wallets.EXPECT().Create(testifymock.Anything).Return(fmtWrap(repositories.ErrWalletTaken)).Once()
			h.wallets.EXPECT().FindByAddress(models.ChainSolana, walletAddress).
				Return(&models.CustomerWallet{CustomerID: tc.winner}, nil).Once()

			_, created, err := h.service().Register(context.Background(), user, acme, registration("123456"))

			assert.False(t, created)
			if tc.wantErr == nil {
				require.NoError(t, err)
				return
			}
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func fmtWrap(err error) error { return errors.Join(errors.New("customer wallet repo: create"), err) }
