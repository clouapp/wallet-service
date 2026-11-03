package ramps_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/goravel/framework/database/orm"
	"github.com/goravel/framework/testing/mock"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/tests/mocks"
	"github.com/macrowallets/waas/packages/rampengine/tests/support/race"
)

const (
	pixAcctEmail    = "ana.souza@example.com"
	pixAcctDocA     = "39053344705"
	pixAcctDocB     = "11144477735"
	pixAcctName     = "Ana Souza"
	pixAcctRef      = "ba_000000000001"
	pixAcctProvider = "blindpay"
)

// pixAcctHarness is the service over an in-memory destination table with the
// repository's semantics (one row per identity, CreatePending writes once)
// and a scripted registrar.
type pixAcctHarness struct {
	svc       *ramps.PixAccountService
	registrar *mocks.MockPixAccountRegistrar
	profiles  *mocks.MockCustomerIndividualProfileRepository

	mu     sync.Mutex
	rows   []*models.OfframpDestination
	nextID uint
	now    time.Time
	alerts []string

	// Scripted answers, read under mu.
	register func(reg ramp.PixAccountRegistration, key string) (*ramp.PixAccountResult, error)
	find     func(name string) (*ramp.PixAccountResult, error)
	get      func(id string) (*ramp.PixAccountResult, error)
	// Recorded calls.
	posts  []postedPix
	finds  []string
	gets   []string
	ensure func() // optional hook run at the top of every POST
}

type postedPix struct {
	Reg ramp.PixAccountRegistration
	Key string
}

func approvedPix(id string) func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
	return func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: id, Status: ramp.BankAccountApproved}, nil
	}
}

func newPixAcctHarness(t *testing.T, locker func(t *testing.T) *mocks.MockLocker) *pixAcctHarness {
	t.Helper()
	mock.Factory().Config().EXPECT().GetString("app.key").Return("test-app-key").Maybe()
	h := &pixAcctHarness{
		registrar: mocks.NewMockPixAccountRegistrar(t),
		profiles:  mocks.NewMockCustomerIndividualProfileRepository(t),
		now:       time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC),
		register:  approvedPix(pixAcctRef),
		find:      func(string) (*ramp.PixAccountResult, error) { return nil, nil },
		get: func(string) (*ramp.PixAccountResult, error) {
			return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountApproved}, nil
		},
	}
	h.profiles.EXPECT().Find(testifymock.Anything).Return(nil, nil).Maybe()

	repo := mocks.NewMockOfframpDestinationRepository(t)
	same := func(a, b models.OfframpDestination) bool {
		return a.CustomerID == b.CustomerID && a.Provider == b.Provider && a.PixKeyHash == b.PixKeyHash && a.DocumentHash == b.DocumentHash
	}
	repo.EXPECT().FindForRegistration(testifymock.Anything).RunAndReturn(func(d models.OfframpDestination) (*models.OfframpDestination, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, r := range h.rows {
			if same(*r, d) {
				c := *r
				return &c, nil
			}
		}
		return nil, nil
	}).Maybe()
	repo.EXPECT().CreatePending(testifymock.Anything).RunAndReturn(func(row *models.OfframpDestination) (bool, error) {
		h.mu.Lock()
		defer h.mu.Unlock()
		for _, r := range h.rows {
			if same(*r, *row) {
				return false, nil
			}
		}
		h.nextID++
		row.ID, row.Status = h.nextID, models.DestinationPending
		c := *row
		h.rows = append(h.rows, &c)
		return true, nil
	}).Maybe()
	repo.EXPECT().SaveRegistration(testifymock.Anything).RunAndReturn(func(row *models.OfframpDestination) error {
		h.mu.Lock()
		defer h.mu.Unlock()
		for i, r := range h.rows {
			if r.ID == row.ID {
				c := *row
				h.rows[i] = &c
				return nil
			}
		}
		return fmt.Errorf("no row %d", row.ID)
	}).Maybe()

	h.registrar.EXPECT().RegisterPixAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, reg ramp.PixAccountRegistration, key string) (*ramp.PixAccountResult, error) {
			if h.ensure != nil {
				h.ensure()
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			h.posts = append(h.posts, postedPix{reg, key})
			return h.register(reg, key)
		}).Maybe()
	h.registrar.EXPECT().FindPixAccountByName(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, name string) (*ramp.PixAccountResult, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.finds = append(h.finds, name)
			return h.find(name)
		}).Maybe()
	h.registrar.EXPECT().GetPixAccount(testifymock.Anything, testifymock.Anything, testifymock.Anything).
		RunAndReturn(func(_ context.Context, _ ramp.CustomerRef, id string) (*ramp.PixAccountResult, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.gets = append(h.gets, id)
			return h.get(id)
		}).Maybe()

	var lk *mocks.MockLocker
	if locker != nil {
		lk = locker(t)
	} else {
		lk = race.New().Locker(t, true)
	}
	h.svc = ramps.NewPixAccountService(repo, h.profiles, lk).WithClock(
		func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		func(format string, args ...any) {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.alerts = append(h.alerts, fmt.Sprintf(format, args...))
		})
	return h
}

func (h *pixAcctHarness) advance(d time.Duration) { h.mu.Lock(); h.now = h.now.Add(d); h.mu.Unlock() }

func (h *pixAcctHarness) rowsNow() []models.OfframpDestination {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]models.OfframpDestination, 0, len(h.rows))
	for _, r := range h.rows {
		out = append(out, *r)
	}
	return out
}

func (h *pixAcctHarness) ensure1(customer *models.Customer, in dto.RegisterPixAccountDTO) (string, error) {
	return h.svc.Ensure(context.Background(), h.registrar, ramp.CustomerRef{ExternalID: "re_000000000001"}, customer, in)
}

func pixAcctPerson() *models.Customer {
	return &models.Customer{Model: orm.Model{ID: 7}, Type: models.CustomerTypeIndividual, Status: models.CustomerStatusActive}
}

const pixAcctCNPJ = "12ABC34501DE35"

func pixAcctCompany() *models.Customer {
	digest := repositories.BusinessTaxIDDigest(models.TaxIdentity{Type: models.TaxIDTypeCNPJ, Country: "BRA"}, pixAcctCNPJ)
	return &models.Customer{Model: orm.Model{ID: 9}, Type: models.CustomerTypeBusiness, Status: models.CustomerStatusActive, TaxIDHash: &digest}
}

func pixAcctInput(doc string) dto.RegisterPixAccountDTO {
	return dto.RegisterPixAccountDTO{
		Provider: pixAcctProvider, PixKey: pixAcctEmail, PixKeyType: "email",
		RecipientName: pixAcctName, RecipientDocument: doc,
	}
}

func TestPixAccountEnsure_RegistersOncePerKeyAndDocument(t *testing.T) {
	h := newPixAcctHarness(t, nil)

	first, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.NoError(t, err)
	second, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.NoError(t, err)

	assert.Equal(t, pixAcctRef, first)
	assert.Equal(t, first, second)
	assert.Len(t, h.posts, 1, "the second call is answered from the ready row")
	rows := h.rowsNow()
	require.Len(t, rows, 1)
	assert.Equal(t, models.DestinationReady, rows[0].Status)
	assert.Equal(t, "re_000000000001", rows[0].ProviderCustomerID)
}

// The same key under another document is another account, and one never
// reuses the other's ba_ (Review Focus 3).
func TestPixAccountEnsure_SameKeyAnotherDocumentIsAnotherAccount(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	n := 0
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		n++
		return &ramp.PixAccountResult{ExternalAccountID: fmt.Sprintf("ba_00000000000%d", n), Status: ramp.BankAccountApproved}, nil
	}

	a, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.NoError(t, err)
	b, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocB))
	require.NoError(t, err)

	assert.NotEqual(t, a, b)
	require.Len(t, h.posts, 2)
	assert.NotEqual(t, h.posts[0].Reg.Name, h.posts[1].Reg.Name)
	assert.NotEqual(t, h.posts[0].Key, h.posts[1].Key)
}

// The POST went and the answer did not come back: the next call, past the
// key window, finds the account by name and registers nothing more
// (Review Focus 4).
func TestPixAccountEnsure_ALostAnswerIsFoundByNameAndNeverRegisteredTwice(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		return nil, ramp.ErrOutcomeUnknown
	}
	_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.ErrorIs(t, err, ramp.ErrOutcomeUnknown)
	require.Len(t, h.posts, 1)

	h.advance(25 * time.Hour)
	h.find = func(name string) (*ramp.PixAccountResult, error) {
		assert.Equal(t, h.posts[0].Reg.Name, name)
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountApproved}, nil
	}
	got, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.NoError(t, err)
	assert.Equal(t, pixAcctRef, got)
	assert.Len(t, h.posts, 1, "the account the first attempt created is linked, not made again")
}

func TestPixAccountEnsure_RetriesTheSameBytesUnderTheSameKeyWithinTheWindow(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	attempts := 0
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		attempts++
		if attempts == 1 {
			return nil, ramp.ErrRetryableUpstream
		}
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountApproved}, nil
	}
	_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.ErrorIs(t, err, ramp.ErrRetryableUpstream)
	h.advance(time.Hour)

	got, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.NoError(t, err)
	assert.Equal(t, pixAcctRef, got)
	require.Len(t, h.posts, 2)
	assert.Equal(t, h.posts[0], h.posts[1], "the same registration under the same key")
	assert.Empty(t, h.finds, "no lookup by name while the key is still good")
}

func TestPixAccountEnsure_AnIdempotencyConflictFallsBackToTheNameLookup(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	calls := 0
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		calls++
		if calls == 1 {
			return nil, ramp.ErrOutcomeUnknown
		}
		return nil, ramp.ErrIdempotencyConflict
	}
	_, _ = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	h.find = func(string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountApproved}, nil
	}

	got, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.NoError(t, err)
	assert.Equal(t, pixAcctRef, got)
	assert.Len(t, h.finds, 1)
}

// Two callers at once register once (the lock serializes them).
func TestPixAccountEnsure_TwoConcurrentCallsRegisterOnce(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	var wg sync.WaitGroup
	results := make([]string, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		}()
	}
	wg.Wait()

	require.NoError(t, errors.Join(errs...))
	assert.Equal(t, []string{pixAcctRef, pixAcctRef}, results)
	assert.Len(t, h.posts, 1)
}

// A caller whose row write lost to another's sends nothing and is told to ask
// again, rather than registering a second account.
func TestPixAccountEnsure_ALostRowWriteSendsNothing(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	loser := mocks.NewMockOfframpDestinationRepository(t)
	loser.EXPECT().FindForRegistration(testifymock.Anything).Return(nil, nil).Once()
	loser.EXPECT().CreatePending(testifymock.Anything).Return(false, nil).Once()
	svc := ramps.NewPixAccountService(loser, h.profiles, race.New().Locker(t, true))

	_, err := svc.Ensure(context.Background(), h.registrar, ramp.CustomerRef{ExternalID: "re_000000000001"}, pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
	assert.Empty(t, h.posts)
}

func TestPixAccountEnsure_VerifyingThenApprovedOnTheNextCall(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountVerifying}, nil
	}
	_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)
	assert.Equal(t, models.DestinationVerifying, h.rowsNow()[0].Status)

	// Still verifying: the same answer, and still no second POST.
	h.get = func(string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountVerifying}, nil
	}
	_, err = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.ErrorIs(t, err, ramp.ErrBankAccountVerifying)

	h.get = func(string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountApproved}, nil
	}
	got, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.NoError(t, err)
	assert.Equal(t, pixAcctRef, got)
	assert.Len(t, h.posts, 1)
	assert.Equal(t, models.DestinationReady, h.rowsNow()[0].Status)
}

func TestPixAccountEnsure_ADeprecatedAccountIsRegisteredAgain(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountVerifying}, nil
	}
	_, _ = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	h.get = func(string) (*ramp.PixAccountResult, error) {
		return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountDeprecated}, nil
	}
	h.register = approvedPix("ba_000000000002")

	got, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	require.NoError(t, err)
	assert.Equal(t, "ba_000000000002", got)
	require.Len(t, h.posts, 2)
	assert.NotEqual(t, h.posts[0].Key, h.posts[1].Key, "a new attempt goes under a new key")
}

func TestPixAccountEnsure_RejectedAndEchoMismatchAreNotRetried(t *testing.T) {
	t.Run("rejected by the provider", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
			return &ramp.PixAccountResult{ExternalAccountID: pixAcctRef, Status: ramp.BankAccountRejected}, nil
		}
		_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
		_, err = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
		assert.Len(t, h.posts, 1)
	})
	t.Run("echo mismatch", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
			return nil, fmt.Errorf("blindpay: %w", ramp.ErrBankAccountEchoMismatch)
		}
		_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
		_, err = h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.ErrorIs(t, err, ramp.ErrBankAccountEchoMismatch)
		assert.Len(t, h.posts, 1, "the provider holds an account a human must look at")
		require.Len(t, h.alerts, 1, "alerted once")
	})
	t.Run("a refusal of the data answers rejected", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
			return nil, statusError{status: 422}
		}
		_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.ErrorIs(t, err, ramp.ErrBankAccountRejected)
	})
	t.Run("a 5xx is not a refusal", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
			return nil, statusError{status: 503}
		}
		_, err := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
		require.Error(t, err)
		assert.NotErrorIs(t, err, ramp.ErrBankAccountRejected)
	})
}

func TestPixAccountEnsure_NoProviderCustomerIsIntegrationRequired(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	_, err := h.svc.Ensure(context.Background(), h.registrar, ramp.CustomerRef{}, pixAcctPerson(), pixAcctInput(pixAcctDocA))
	require.ErrorIs(t, err, ramp.ErrIntegrationRequired)
	assert.Empty(t, h.posts)
}

// first_party only for the customer's own document (D7).
func TestPixAccountEnsure_RelationshipIsFirstPartyOnlyForTheCustomersOwnDocument(t *testing.T) {
	ownCPF := "39053344705"
	profile := models.KycProfile{TaxID: ownCPF, TaxIDType: "cpf", TaxIDCountry: "BRA"}

	person := func(h *pixAcctHarness) {
		h.profiles.ExpectedCalls = nil
		h.profiles.EXPECT().Find(testifymock.Anything).Return(&profile, nil).Maybe()
	}
	cases := []struct {
		name     string
		customer *models.Customer
		prepare  func(*pixAcctHarness)
		in       dto.RegisterPixAccountDTO
		want     string
	}{
		{"person, own CPF as the key", pixAcctPerson(), person,
			dto.RegisterPixAccountDTO{Provider: pixAcctProvider, PixKey: "390.533.447-05", PixKeyType: "cpf", RecipientName: pixAcctName}, ramp.RelationshipFirstParty},
		{"person, email key under the own CPF", pixAcctPerson(), person, pixAcctInput(ownCPF), ramp.RelationshipFirstParty},
		{"person, email key under another CPF", pixAcctPerson(), person, pixAcctInput(pixAcctDocB), ramp.RelationshipOther},
		{"person, another CPF as the key", pixAcctPerson(), person,
			dto.RegisterPixAccountDTO{Provider: pixAcctProvider, PixKey: pixAcctDocB, PixKeyType: "cpf"}, ramp.RelationshipOther},
		{"person with no retained profile", pixAcctPerson(), nil, pixAcctInput(ownCPF), ramp.RelationshipOther},
		{"person, no document", pixAcctPerson(), person, pixAcctInput(""), ramp.RelationshipOther},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newPixAcctHarness(t, nil)
			if c.prepare != nil {
				c.prepare(h)
			}
			_, err := h.ensure1(c.customer, c.in)
			require.NoError(t, err)
			require.Len(t, h.posts, 1)
			assert.Equal(t, c.want, h.posts[0].Reg.Relationship)
		})
	}

	t.Run("company, its own CNPJ", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		_, err := h.ensure1(pixAcctCompany(), dto.RegisterPixAccountDTO{Provider: pixAcctProvider, PixKey: pixAcctCNPJ, PixKeyType: "cnpj", RecipientName: "ACME"})
		require.NoError(t, err)
		assert.Equal(t, ramp.RelationshipFirstParty, h.posts[0].Reg.Relationship)
	})
	t.Run("company, another CNPJ", func(t *testing.T) {
		h := newPixAcctHarness(t, nil)
		_, err := h.ensure1(pixAcctCompany(), dto.RegisterPixAccountDTO{Provider: pixAcctProvider, PixKey: "11222333000181", PixKeyType: "cnpj"})
		require.NoError(t, err)
		assert.Equal(t, ramp.RelationshipOther, h.posts[0].Reg.Relationship)
	})
}

func TestPixAccountEnsure_SendsTheKeyInItsNormalFormUnderTheDocument(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	in := dto.RegisterPixAccountDTO{Provider: pixAcctProvider, PixKey: "  Ana.Souza@Example.com ", PixKeyType: "email", RecipientName: pixAcctName, RecipientDocument: "390.533.447-05"}

	_, err := h.ensure1(pixAcctPerson(), in)

	require.NoError(t, err)
	reg := h.posts[0].Reg
	assert.Equal(t, "ana.souza@example.com", reg.PixKey)
	assert.Equal(t, "39053344705", reg.RecipientDocument)
	assert.Equal(t, "email", reg.PixKeyType)
	assert.Regexp(t, `^xip-pix-[0-9a-f]{20}$`, reg.Name)
}

// Nothing the user typed reaches an error, an alert, the row or the key of
// an attempt (Review Focus 10).
func TestPixAccountEnsure_NeverPutsTheKeyTheDocumentOrTheNameInAnErrorAnAlertOrTheRow(t *testing.T) {
	h := newPixAcctHarness(t, nil)
	h.register = func(ramp.PixAccountRegistration, string) (*ramp.PixAccountResult, error) {
		return nil, fmt.Errorf("blindpay: %w", ramp.ErrBankAccountEchoMismatch)
	}
	_, err1 := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))
	_, err2 := h.ensure1(pixAcctPerson(), pixAcctInput(pixAcctDocA))

	var out []string
	out = append(out, err1.Error(), err2.Error())
	out = append(out, h.alerts...)
	for _, r := range h.rowsNow() {
		out = append(out, fmt.Sprintf("%+v", r))
	}
	for _, p := range h.posts {
		out = append(out, p.Key)
	}
	for _, text := range out {
		for _, secret := range []string{pixAcctEmail, pixAcctDocA, pixAcctName} {
			assert.NotContains(t, text, secret)
		}
	}
}
