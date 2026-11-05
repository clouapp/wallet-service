package bank

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp/blindpay"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
)

// passLocker runs the critical section without locking anything.
type passLocker struct{ keys []string }

func (l *passLocker) WithLock(ctx context.Context, key string, fn func(ctx context.Context) error) error {
	l.keys = append(l.keys, key)
	return fn(ctx)
}

// registrarAdapter is a provider with the bank account port: the adapter
// contract from one mock, the three port methods from the other.
type registrarAdapter struct {
	*mocks.MockProviderAdapter
	*mocks.MockBankAccountRegistrar
}

type ensureHarness struct {
	t             *testing.T
	beneficiary   *repositories.BankBeneficiaryRecord
	customerID    uint
	ref           ramp.CustomerRef
	reg           ramp.BeneficiaryAccountRegistration
	now           time.Time
	locker        *passLocker
	beneficiaries *mocks.MockCustomerBankBeneficiaryRepository
	accounts      *mocks.MockBankProviderAccountRepository
	integrations  *mocks.MockCustomerProviderIntegrationRepository
	registrar     *mocks.MockBankAccountRegistrar
	svc           *ProviderAccountService
	alerts        []string
}

const (
	testCustomerRef = "re_000000000001"
	testRowID       = "6a1b0f4e-0000-4000-8000-000000000001"
)

// newEnsureHarness builds the service over mocks: beneficiary revision 1,
// integration re_000000000001, an adapter with the port, a fixed clock.
func newEnsureHarness(t *testing.T) *ensureHarness {
	return newHarnessWith(t, true)
}

func newHarnessWith(t *testing.T, withPort bool) *ensureHarness {
	h := &ensureHarness{
		t:          t,
		customerID: 7,
		beneficiary: &repositories.BankBeneficiaryRecord{
			ID: 11, ExternalID: "0b6f3c1e-2d4a-4b8e-9c1f-5e6d7a8b9c0d", CustomerID: 7, Revision: 1,
			BankBeneficiaryWrite: repositories.BankBeneficiaryWrite{
				Label: "Chase", AccountClass: "individual", AccountType: "checking", Relationship: "first_party",
				AccountLast4: "6789", RoutingLast4: "0021", BeneficiaryName: "Ana Souza",
				RoutingNumber: "021000021", AccountNumber: "123456789",
				AddressLine1: "350 Fifth Avenue", City: "New York", StateCode: "NY", PostalCode: "10118",
			},
		},
		now:           time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC),
		locker:        &passLocker{},
		beneficiaries: mocks.NewMockCustomerBankBeneficiaryRepository(t),
		accounts:      mocks.NewMockBankProviderAccountRepository(t),
		integrations:  mocks.NewMockCustomerProviderIntegrationRepository(t),
		registrar:     mocks.NewMockBankAccountRegistrar(t),
	}
	h.ref = ramp.CustomerRef{CustomerID: h.customerID, ExternalID: testCustomerRef}
	h.reg = registrationOf(h.beneficiary, ramp.RailACHPayout)

	base := mocks.NewMockProviderAdapter(t)
	base.EXPECT().Code().Return(ramp.ProviderBlindPay).Maybe()
	var adapter ramp.ProviderAdapter = base
	if withPort {
		adapter = registrarAdapter{MockProviderAdapter: base, MockBankAccountRegistrar: h.registrar}
	}
	h.svc = NewProviderAccountService(ProviderAccountDeps{
		Beneficiaries: h.beneficiaries, Accounts: h.accounts, Integrations: h.integrations,
		Factory: ramp.NewFactory(adapter), Locker: h.locker, Now: func() time.Time { return h.now },
		Alertf: func(format string, args ...any) { h.alerts = append(h.alerts, fmt.Sprintf(format, args...)) },
	})
	return h
}

// ready answers the beneficiary and its BlindPay customer.
func (h *ensureHarness) ready() {
	h.beneficiaries.EXPECT().FindByExternalID(h.customerID, h.beneficiary.ExternalID).Return(h.beneficiary, nil)
	ext := testCustomerRef
	h.integrations.EXPECT().FindByCustomerAndProvider(h.customerID, "blindpay").
		Return(&models.CustomerProviderIntegration{CustomerID: h.customerID, Provider: "blindpay", ExternalCustomerID: &ext}, nil)
}

func (h *ensureHarness) ensure() (*ProviderAccount, error) {
	return h.svc.Ensure(context.Background(), h.customerID, h.beneficiary.ExternalID, ramp.ProviderBlindPay, ramp.RailACHPayout)
}

func (h *ensureHarness) current(row *models.CustomerBankBeneficiaryProviderAccount) {
	h.accounts.EXPECT().FindCurrent(h.beneficiary.ID, "blindpay", "ach_payout").Return(row, nil).Once()
}

func ptr[T any](v T) *T { return &v }

// echoed is an account as a GET or a lookup answers it, with the last four
// digits of the harness registration.
func (h *ensureHarness) echoed(ba string, status ramp.BankAccountStatus) *ramp.BankAccountResult {
	return &ramp.BankAccountResult{ExternalAccountID: ba, Status: status, AccountLast4: "6789", RoutingLast4: "0021"}
}

// row is a live mapping of the harness beneficiary's revision 1.
func (h *ensureHarness) row(status string, ba string) *models.CustomerBankBeneficiaryProviderAccount {
	attempted := h.now.Add(-time.Hour)
	deadline := attempted.Add(registrationWindow)
	r := &models.CustomerBankBeneficiaryProviderAccount{
		ExternalID: testRowID, BeneficiaryID: h.beneficiary.ID, BeneficiaryRevision: 1,
		Provider: "blindpay", Rail: "ach_payout", CustomerExternalID: testCustomerRef, Status: status,
		RegistrationIdempotencyKey: "bank-account:" + testRowID + ":1", RegistrationRequestHash: registrationHash(h.reg),
		RegistrationAttempt: 1, RegistrationAttemptedAt: &attempted, RetryDeadline: &deadline,
	}
	r.ID = 21
	if ba != "" {
		r.ExternalAccountID = ptr(ba)
	}
	return r
}

// expired moves a pending row's key window into the past.
func (h *ensureHarness) expired(r *models.CustomerBankBeneficiaryProviderAccount) *models.CustomerBankBeneficiaryProviderAccount {
	attempted := h.now.Add(-25 * time.Hour)
	deadline := attempted.Add(registrationWindow)
	r.RegistrationAttemptedAt, r.RetryDeadline = &attempted, &deadline
	return r
}

// expectNewRow expects the pending row of a fresh registration of revision
// rev for customer, and answers the key it was written with.
func (h *ensureHarness) expectNewRow(rev int, customer string) (*testifymock.Call, *string) {
	key := new(string)
	call := h.accounts.EXPECT().CreatePending(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountPending && r.BeneficiaryID == h.beneficiary.ID &&
			r.BeneficiaryRevision == rev && r.CustomerExternalID == customer && r.ExternalID != "" &&
			r.RegistrationAttempt == 1 && r.RegistrationIdempotencyKey == "bank-account:"+r.ExternalID+":1" &&
			r.RegistrationRequestHash != "" && r.RegistrationAttemptedAt != nil && r.RegistrationAttemptedAt.Equal(h.now) &&
			r.RetryDeadline != nil && r.RetryDeadline.Equal(h.now.Add(24*time.Hour))
	})).Run(func(r *models.CustomerBankBeneficiaryProviderAccount) { *key = r.RegistrationIdempotencyKey }).Return(nil).Once()
	return call, key
}

func savedAs(status, ba string) any {
	return testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == status && (ba == "" || (r.ExternalAccountID != nil && *r.ExternalAccountID == ba))
	})
}

func TestEnsureReturnsAnApprovedValidMapping(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	h.current(h.row(models.ProviderAccountApproved, ba))

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, &ProviderAccount{ExternalAccountID: ba, BeneficiaryRevision: 1, CustomerExternalID: testCustomerRef}, got)
	require.Equal(t, []string{"bank-account:" + h.beneficiary.ExternalID}, h.locker.keys)
}

func TestEnsurePromotesVerifyingByGet(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	h.current(h.row(models.ProviderAccountVerifying, ba))
	h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).
		Return(h.echoed(ba, ramp.BankAccountApproved), nil)
	h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountApproved && r.LastCheckedAt != nil && r.LastCheckedAt.Equal(h.now)
	})).Return(nil)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, ba, got.ExternalAccountID)
}

func TestEnsureKeepsWaitingWhileVerifying(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	h.current(h.row(models.ProviderAccountVerifying, ba))
	h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).
		Return(h.echoed(ba, ramp.BankAccountVerifying), nil)
	h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountVerifying && r.LastCheckedAt != nil && r.LastCheckedAt.Equal(h.now)
	})).Return(nil)

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
}

func TestEnsureSavesARejectionReadByGet(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	h.current(h.row(models.ProviderAccountVerifying, ba))
	h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).
		Return(h.echoed(ba, ramp.BankAccountRejected), nil)
	h.accounts.EXPECT().Save(savedAs(models.ProviderAccountRejected, ba)).Return(nil)

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
}

func TestEnsureLeavesTheRowWhenTheReadFails(t *testing.T) {
	for name, readErr := range map[string]error{
		"outcome unknown": fmt.Errorf("blindpay: GET: %w", ramp.ErrOutcomeUnknown),
		"echo mismatch":   fmt.Errorf("blindpay: id: %w", ramp.ErrBankAccountEchoMismatch),
	} {
		t.Run(name, func(t *testing.T) {
			h := newEnsureHarness(t)
			h.ready()
			ba := "ba_000000000001"
			h.current(h.row(models.ProviderAccountVerifying, ba))
			h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).Return(nil, readErr)

			_, err := h.ensure()
			require.ErrorIs(t, err, readErr)
			// No Save, Supersede or POST was expected: the mocks fail on one.
		})
	}
}

func TestEnsureRefusesARejectedAccount(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.row(models.ProviderAccountRejected, "ba_000000000001"))

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
}

func TestEnsureSupersedesAnOldRevision(t *testing.T) {
	h := newEnsureHarness(t)
	h.beneficiary.Revision = 2
	h.ready()
	old := h.row(models.ProviderAccountApproved, "ba_000000000001")
	h.current(old)
	r2 := registrationOf(h.beneficiary, ramp.RailACHPayout)
	require.Contains(t, r2.Name, "-r2-")

	supersede := h.accounts.EXPECT().Supersede(old).Return(nil).Once()
	create, key := h.expectNewRow(2, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, r2, testifymock.Anything).
		Run(func(_ context.Context, _ ramp.CustomerRef, _ ramp.BeneficiaryAccountRegistration, k string) {
			require.Equal(t, *key, k)
		}).Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

func TestEnsureSupersedesAnotherCustomer(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	old := h.row(models.ProviderAccountApproved, "ba_000000000001")
	old.CustomerExternalID = "re_000000000099"
	h.current(old)

	supersede := h.accounts.EXPECT().Supersede(old).Return(nil).Once()
	create, _ := h.expectNewRow(1, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

func TestEnsureReplaysAPendingWithinTheWindow(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	pending := h.row(models.ProviderAccountPending, "")
	h.current(pending)
	h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, "bank-account:"+testRowID+":1").
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000001", Status: ramp.BankAccountApproved}, nil).Once()
	h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000001")).Return(nil).Once()

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
}

// A pending row of the same revision whose request changed may have created
// an account already: it is looked up by name before anything is sent, never
// superseded and POSTed blind.
func TestEnsureFindsAPendingOfAnotherRequestByNameFirst(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	pending := h.row(models.ProviderAccountPending, "")
	pending.RegistrationRequestHash = "another request"
	h.current(pending)
	hash := registrationHash(h.reg)

	find := h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).
		Return(h.echoed("ba_000000000001", ramp.BankAccountApproved), nil).Once()
	save := h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.ID == pending.ID && r.Status == models.ProviderAccountApproved && r.RegistrationRequestHash == hash
	})).Return(nil).Once()
	testifymock.InOrder(find, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
	// No Supersede, CreatePending or RegisterBankAccount was expected.
}

func TestEnsurePostsAPendingOfAnotherRequestUnderANewKeyWhenNotFound(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	pending := h.row(models.ProviderAccountPending, "")
	pending.RegistrationRequestHash = "another request"
	h.current(pending)
	hash := registrationHash(h.reg)
	newKey := "bank-account:" + testRowID + ":2"

	find := h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).Return(nil, nil).Once()
	bump := h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountPending && r.RegistrationAttempt == 2 &&
			r.RegistrationIdempotencyKey == newKey && r.RegistrationRequestHash == hash
	})).Return(nil).Once()
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, newKey).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(find, bump, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

// A pending row of an older revision registers under the old name; the new
// revision starts over on a new row and name.
func TestEnsureSupersedesAPendingOldRevision(t *testing.T) {
	h := newEnsureHarness(t)
	h.beneficiary.Revision = 2
	h.ready()
	old := h.row(models.ProviderAccountPending, "")
	h.current(old)
	r2 := registrationOf(h.beneficiary, ramp.RailACHPayout)
	require.Contains(t, r2.Name, "-r2-")

	supersede := h.accounts.EXPECT().Supersede(old).Return(nil).Once()
	create, key := h.expectNewRow(2, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, r2, testifymock.Anything).
		Run(func(_ context.Context, _ ramp.CustomerRef, _ ramp.BeneficiaryAccountRegistration, k string) {
			require.Equal(t, *key, k)
		}).Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, &ProviderAccount{ExternalAccountID: "ba_000000000002", BeneficiaryRevision: 2, CustomerExternalID: testCustomerRef}, got)
}

func TestEnsureFindsByNameAfterTheWindow(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.expired(h.row(models.ProviderAccountPending, "")))
	h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).
		Return(h.echoed("ba_000000000001", ramp.BankAccountApproved), nil).Once()
	h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000001")).Return(nil).Once()

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
}

func TestEnsurePostsAgainWithANewKeyWhenNotFound(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.expired(h.row(models.ProviderAccountPending, "")))
	newKey := "bank-account:" + testRowID + ":2"

	find := h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).Return(nil, nil).Once()
	bump := h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountPending && r.RegistrationAttempt == 2 && r.RegistrationIdempotencyKey == newKey &&
			r.RegistrationAttemptedAt.Equal(h.now) && r.RetryDeadline.Equal(h.now.Add(24*time.Hour))
	})).Return(nil).Once()
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, newKey).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000001", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000001")).Return(nil).Once()
	testifymock.InOrder(find, bump, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
}

func TestEnsureFindsByNameAfterAnIdempotencyConflict(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.row(models.ProviderAccountPending, ""))

	replay := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, "bank-account:"+testRowID+":1").
		Return(nil, fmt.Errorf("blindpay: 422: %w", ramp.ErrIdempotencyConflict)).Once()
	find := h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).
		Return(h.echoed("ba_000000000001", ramp.BankAccountApproved), nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000001")).Return(nil).Once()
	testifymock.InOrder(replay, find, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
}

func TestEnsureWritesPendingBeforePosting(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)

	create, key := h.expectNewRow(1, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).
		Run(func(_ context.Context, _ ramp.CustomerRef, _ ramp.BeneficiaryAccountRegistration, k string) {
			require.Equal(t, *key, k)
		}).Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000001", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000001")).Return(nil).Once()
	testifymock.InOrder(create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000001", got.ExternalAccountID)
}

func TestEnsureAnswersVerifyingWhenAnotherRegistrationWroteFirst(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)
	h.accounts.EXPECT().CreatePending(testifymock.Anything).Return(repositories.ErrProviderAccountStale).Once()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
}

func TestEnsureLeavesPendingOnAnUnknownOutcome(t *testing.T) {
	for name, postErr := range map[string]error{
		"outcome unknown": fmt.Errorf("blindpay: POST: %w", ramp.ErrOutcomeUnknown),
		"retryable":       fmt.Errorf("blindpay: POST: %w", ramp.ErrRetryableUpstream),
		"not sent":        fmt.Errorf("blindpay: POST: %w", ramp.ErrRequestNotSent),
	} {
		t.Run(name, func(t *testing.T) {
			h := newEnsureHarness(t)
			h.ready()
			h.current(nil)
			h.expectNewRow(1, testCustomerRef)
			h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).Return(nil, postErr).Once()

			_, err := h.ensure()
			require.ErrorIs(t, err, postErr)
			// No Save was expected: the row stays pending.
		})
	}
}

func TestEnsureFailsOnAnEchoMismatch(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)
	h.expectNewRow(1, testCustomerRef)
	echo := fmt.Errorf("blindpay: bank account echo: numbers differ: %w", ramp.ErrBankAccountEchoMismatch)
	h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).Return(nil, echo).Once()
	h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountFailed && r.FailureCode != nil && *r.FailureCode == "echo_mismatch"
	})).Return(nil).Once()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.Len(t, h.alerts, 1)
	require.Contains(t, h.alerts[0], "ramp.attention")
	require.Contains(t, h.alerts[0], h.beneficiary.ExternalID)
	for _, secret := range []string{"123456789", "021000021", "Ana Souza", "350 Fifth Avenue"} {
		require.NotContains(t, h.alerts[0], secret)
	}
}

func (h *ensureHarness) echoMismatchRow() *models.CustomerBankBeneficiaryProviderAccount {
	failed := h.row(models.ProviderAccountFailed, "")
	failed.FailureCode = ptr("echo_mismatch")
	return failed
}

func TestEnsureKeepsAnEchoMismatchTerminal(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.echoMismatchRow())

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
	require.Empty(t, h.alerts, "the alert went out when the mismatch was recorded")
	// No Supersede, CreatePending or provider call was expected.
}

func TestEnsureRegistersANewRevisionAfterAnEchoMismatch(t *testing.T) {
	h := newEnsureHarness(t)
	h.beneficiary.Revision = 2
	h.ready()
	failed := h.echoMismatchRow()
	h.current(failed)
	r2 := registrationOf(h.beneficiary, ramp.RailACHPayout)

	supersede := h.accounts.EXPECT().Supersede(failed).Return(nil).Once()
	create, _ := h.expectNewRow(2, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, r2, testifymock.Anything).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

func TestEnsureFailsOnARefusal(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)
	h.expectNewRow(1, testCustomerRef)
	refusal := fmt.Errorf("blindpay: 400: %w", ramp.ErrProviderFailed)
	h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).Return(nil, refusal).Once()
	h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
		return r.Status == models.ProviderAccountFailed && r.FailureCode != nil && *r.FailureCode == "bank_account_refused"
	})).Return(nil).Once()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.NotErrorIs(t, err, ramp.ErrBankAccountRejected, "no status: not known to be the data")
	require.Empty(t, h.alerts)
}

// A 400/422 the provider gives the data itself is the user's to fix: E5 shows
// bank_account_rejected. The row fails as refused, and the next quote retries.
func TestEnsureReportsARefusalOfTheDataAsRejected(t *testing.T) {
	for _, status := range []int{400, 422} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := newEnsureHarness(t)
			h.ready()
			h.current(nil)
			h.expectNewRow(1, testCustomerRef)
			refusal := &blindpay.UpstreamError{Method: "POST", Path: "customers/re_000000000001/bank-accounts", Status: status, Code: "VALIDATION_ERROR"}
			h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).Return(nil, refusal).Once()
			h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
				return r.Status == models.ProviderAccountFailed && r.FailureCode != nil && *r.FailureCode == "bank_account_refused"
			})).Return(nil).Once()

			_, err := h.ensure()
			require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
			require.ErrorIs(t, err, ramp.ErrProviderFailed)
			require.Empty(t, h.alerts)
		})
	}
}

// A 422 that names a known condition keeps its own meaning.
func TestEnsureDoesNotReportAKnownConditionAsRejected(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)
	h.expectNewRow(1, testCustomerRef)
	kyc := fmt.Errorf("%w: %w", ramp.ErrKycPending, &blindpay.UpstreamError{Status: 422, Code: "CUSTOMERS_KYC_NOT_APPROVED"})
	h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).Return(nil, kyc).Once()
	h.accounts.EXPECT().Save(testifymock.Anything).Return(nil).Once()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrKycPending)
	require.NotErrorIs(t, err, ramp.ErrBankAccountRejected)
}

func TestEnsureAlertsOnceOnAnAmbiguousName(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(h.expired(h.row(models.ProviderAccountPending, "")))
	ambiguous := fmt.Errorf("blindpay: 2 bank accounts named %s: %w", h.reg.Name, ramp.ErrBankAccountNameAmbiguous)
	h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).Return(nil, ambiguous).Once()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrBankAccountNameAmbiguous)
	require.ErrorIs(t, err, ramp.ErrProviderFailed)
	require.Len(t, h.alerts, 1)
	require.Contains(t, h.alerts[0], "ramp.attention")
	require.Contains(t, h.alerts[0], testRowID)
	require.Contains(t, h.alerts[0], h.beneficiary.ExternalID)
	for _, secret := range []string{"123456789", "021000021", "Ana Souza", "350 Fifth Avenue"} {
		require.NotContains(t, h.alerts[0], secret)
	}
	// No Save or POST was expected: nothing is linked or sent.
}

// BankAccountOut declares the numbers nullable: a GET that omits them is no
// mismatch, and the account is promoted as usual.
func TestEnsurePromotesAReadAccountWithoutNumbers(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	h.current(h.row(models.ProviderAccountVerifying, ba))
	h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).
		Return(&ramp.BankAccountResult{ExternalAccountID: ba, Status: ramp.BankAccountApproved}, nil).Once()
	h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, ba)).Return(nil).Once()

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, ba, got.ExternalAccountID)
	require.Empty(t, h.alerts)
}

// An account linked by name or read by GET is checked against the
// registration in hand like a POST's echo: a mismatch is terminal.
func TestEnsureFailsOnAnEchoMismatchOfAFoundOrReadAccount(t *testing.T) {
	wrong := &ramp.BankAccountResult{ExternalAccountID: "ba_000000000001", Status: ramp.BankAccountApproved, AccountLast4: "1111", RoutingLast4: "0021"}
	for name, setup := range map[string]func(h *ensureHarness){
		"found by name": func(h *ensureHarness) {
			h.current(h.expired(h.row(models.ProviderAccountPending, "")))
			h.registrar.EXPECT().FindBankAccountByName(testifymock.Anything, h.ref, h.reg.Name).Return(wrong, nil).Once()
		},
		"read by GET": func(h *ensureHarness) {
			h.current(h.row(models.ProviderAccountVerifying, "ba_000000000001"))
			h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, "ba_000000000001").Return(wrong, nil).Once()
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newEnsureHarness(t)
			h.ready()
			setup(h)
			h.accounts.EXPECT().Save(testifymock.MatchedBy(func(r *models.CustomerBankBeneficiaryProviderAccount) bool {
				return r.Status == models.ProviderAccountFailed && r.FailureCode != nil && *r.FailureCode == "echo_mismatch"
			})).Return(nil).Once()

			_, err := h.ensure()
			require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
			require.Len(t, h.alerts, 1)
			require.Contains(t, h.alerts[0], "ramp.attention")
			for _, secret := range []string{"123456789", "021000021", "6789", "1111"} {
				require.NotContains(t, h.alerts[0], secret)
				require.NotContains(t, err.Error(), secret)
			}
		})
	}
}

func TestEnsureSupersedesAFailedRowAndTriesAgain(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	failed := h.row(models.ProviderAccountFailed, "")
	failed.FailureCode = ptr("bank_account_refused")
	h.current(failed)

	supersede := h.accounts.EXPECT().Supersede(failed).Return(nil).Once()
	create, _ := h.expectNewRow(1, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

func TestEnsureTreatsDeprecatedAsGone(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	ba := "ba_000000000001"
	verifying := h.row(models.ProviderAccountVerifying, ba)
	h.current(verifying)

	get := h.registrar.EXPECT().GetBankAccount(testifymock.Anything, h.ref, ba).
		Return(&ramp.BankAccountResult{ExternalAccountID: ba, Status: ramp.BankAccountDeprecated}, nil).Once()
	supersede := h.accounts.EXPECT().Supersede(verifying).Return(nil).Once()
	create, _ := h.expectNewRow(1, testCustomerRef)
	post := h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).
		Return(&ramp.BankAccountResult{ExternalAccountID: "ba_000000000002", Status: ramp.BankAccountApproved}, nil).Once()
	save := h.accounts.EXPECT().Save(savedAs(models.ProviderAccountApproved, "ba_000000000002")).Return(nil).Once()
	testifymock.InOrder(get, supersede, create, post, save)

	got, err := h.ensure()
	require.NoError(t, err)
	require.Equal(t, "ba_000000000002", got.ExternalAccountID)
}

func TestEnsureNeedsTheProviderCustomer(t *testing.T) {
	for name, link := range map[string]*models.CustomerProviderIntegration{
		"no integration":    nil,
		"no external id":    {CustomerID: 7, Provider: "blindpay"},
		"empty external id": {CustomerID: 7, Provider: "blindpay", ExternalCustomerID: ptr("")},
	} {
		t.Run(name, func(t *testing.T) {
			h := newEnsureHarness(t)
			h.beneficiaries.EXPECT().FindByExternalID(h.customerID, h.beneficiary.ExternalID).Return(h.beneficiary, nil)
			h.integrations.EXPECT().FindByCustomerAndProvider(h.customerID, "blindpay").Return(link, nil)

			_, err := h.ensure()
			require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
		})
	}
}

func TestEnsureNeedsTheRegistrarPort(t *testing.T) {
	h := newHarnessWith(t, false)
	h.ready()

	_, err := h.ensure()
	require.ErrorIs(t, err, ramp.ErrProviderNotImplemented)
}

func TestEnsureAnswersAMissingBeneficiary(t *testing.T) {
	h := newEnsureHarness(t)
	h.beneficiaries.EXPECT().FindByExternalID(h.customerID, h.beneficiary.ExternalID).Return(nil, repositories.ErrBankBeneficiaryNotFound)

	_, err := h.ensure()
	require.ErrorIs(t, err, ErrBankBeneficiaryNotFound)
}

func TestRegistrationHashCoversTheNumbersAndIsStable(t *testing.T) {
	h := newEnsureHarness(t)
	other := h.reg
	other.AccountNumber = "987654321"

	require.Equal(t, registrationHash(h.reg), registrationHash(registrationOf(h.beneficiary, ramp.RailACHPayout)))
	require.NotEqual(t, registrationHash(h.reg), registrationHash(other), "a hash of String would not see the number")
	require.Len(t, registrationHash(h.reg), 64)
}

// The hash sits next to the clear last4s: unkeyed, a dump would let anyone
// try every number. It is the keyed digest E3 uses.
func TestRegistrationHashIsKeyed(t *testing.T) {
	h := newEnsureHarness(t)
	type plain ramp.BeneficiaryAccountRegistration
	raw, err := json.Marshal(plain(h.reg))
	require.NoError(t, err)
	unkeyed := sha256.Sum256(raw)

	require.NotEqual(t, hex.EncodeToString(unkeyed[:]), registrationHash(h.reg))
	require.Equal(t, keyedhash.Hex("bank-account-registration:"+string(raw)), registrationHash(h.reg))
}

func TestEnsureErrorsCarryNoAccountData(t *testing.T) {
	h := newEnsureHarness(t)
	h.ready()
	h.current(nil)
	h.expectNewRow(1, testCustomerRef)
	h.registrar.EXPECT().RegisterBankAccount(testifymock.Anything, h.ref, h.reg, testifymock.Anything).
		Return(nil, fmt.Errorf("blindpay: POST: %w", ramp.ErrOutcomeUnknown)).Once()

	_, err := h.ensure()
	require.Error(t, err)
	for _, secret := range []string{"123456789", "021000021", "Ana Souza", "350 Fifth Avenue"} {
		require.NotContains(t, err.Error(), secret)
	}
	require.False(t, errors.Is(err, ramp.ErrBankAccountVerifying))
}
