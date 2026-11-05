package bank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// registrationWindow is how long a provider keeps an Idempotency-Key: within
// it a pending row is replayed with its key, past it the row is looked up by
// its deterministic name.
const registrationWindow = 24 * time.Hour

// failure_code of a failed mapping row.
const (
	failureRefused      = "bank_account_refused"
	failureEchoMismatch = "echo_mismatch"
)

// ProviderAccountDeps wires a ProviderAccountService.
type ProviderAccountDeps struct {
	Beneficiaries repositories.CustomerBankBeneficiaryRepository
	Accounts      repositories.BankProviderAccountRepository
	Integrations  repositories.CustomerProviderIntegrationRepository
	Factory       *ramp.Factory
	Locker        locking.Locker
	Now           func() time.Time
	// Alertf logs at Error level for a human to act on; facades.Log by
	// default. Its arguments are ids only.
	Alertf func(format string, args ...any)
}

// ProviderAccountService turns a beneficiary revision into an approved
// provider bank account, once per revision, rail and provider customer
// (spec §5.5). It never registers twice: the pending row is written before
// the POST, a replay reuses its key and bytes, and past the 24h key window
// the deterministic name finds what an earlier attempt created.
type ProviderAccountService struct{ d ProviderAccountDeps }

func NewProviderAccountService(d ProviderAccountDeps) *ProviderAccountService {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.Alertf == nil {
		d.Alertf = func(format string, args ...any) { facades.Log().Errorf(format, args...) }
	}
	return &ProviderAccountService{d: d}
}

// ProviderAccount is the approved provider bank account Ensure answers, read
// from the row it returned under the lock, so a quote snapshots the revision
// and provider customer the account was registered for.
//
// It is ramp.BankPayoutAccount, aliased, so the ramps service's port can name
// it without importing this package (its mock would close a cycle with this
// package's tests).
type ProviderAccount = ramp.BankPayoutAccount

func accountOf(row *models.CustomerBankBeneficiaryProviderAccount) *ProviderAccount {
	return &ProviderAccount{
		ExternalAccountID: *row.ExternalAccountID, BeneficiaryRevision: row.BeneficiaryRevision,
		CustomerExternalID: row.CustomerExternalID,
	}
}

// Ensure answers the approved provider account (ba_) of the beneficiary's
// current revision on rail.
//
// Errors: ErrBankBeneficiaryNotFound, ramp.ErrIntegrationRequired (no
// provider customer yet), ramp.ErrProviderNotImplemented (the provider
// registers no bank accounts), ramp.ErrBankAccountVerifying,
// ramp.ErrBankAccountRejected (the provider refused the account or, with a
// 400/422, the data itself), ramp.ErrBankAccountEchoMismatch, and the upstream errors a retry may cure
// (ramp.ErrRetryableUpstream, ramp.ErrOutcomeUnknown). They carry ids and
// statuses only, never a number, a name or an address.
func (s *ProviderAccountService) Ensure(ctx context.Context, customerID uint, beneficiaryExternalID string, provider ramp.ProviderCode, rail ramp.Rail) (*ProviderAccount, error) {
	var out *ProviderAccount
	err := s.d.Locker.WithLock(ctx, "bank-account:"+beneficiaryExternalID, func(ctx context.Context) error {
		account, err := s.ensureLocked(ctx, customerID, beneficiaryExternalID, provider, rail)
		out = account
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ensureRun is one Ensure's resolved inputs. It holds the registration in
// full, so it is never logged nor put in an error; its fields are exported
// so that even a stray %v of it would go through the registration's Format
// (fmt does not call methods on unexported fields).
type ensureRun struct {
	Registrar   ramp.BankAccountRegistrar
	Ref         ramp.CustomerRef
	Beneficiary *repositories.BankBeneficiaryRecord
	Provider    string
	Rail        string
	Request     ramp.BeneficiaryAccountRegistration
	Hash        string
}

func (s *ProviderAccountService) ensureLocked(ctx context.Context, customerID uint, extID string, provider ramp.ProviderCode, rail ramp.Rail) (*ProviderAccount, error) {
	run, err := s.resolve(customerID, extID, provider, rail)
	if err != nil {
		return nil, err
	}
	row, err := s.d.Accounts.FindCurrent(run.Beneficiary.ID, run.Provider, run.Rail)
	if err != nil {
		return nil, fmt.Errorf("bank provider account: %w", err)
	}
	if row != nil && s.outdated(row, run) {
		if err := s.d.Accounts.Supersede(row); err != nil {
			return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, err)
		}
		row = nil
	}
	if row == nil {
		return s.registerNew(ctx, run)
	}

	switch row.Status {
	case models.ProviderAccountApproved:
		if row.ExternalAccountID == nil {
			return nil, fmt.Errorf("bank provider account %s: approved without an account id", row.ExternalID)
		}
		return accountOf(row), nil
	case models.ProviderAccountRejected:
		return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, ramp.ErrBankAccountRejected)
	case models.ProviderAccountFailed:
		// Only an echo mismatch stays live (see outdated): the provider holds
		// an account under this name that a human must look at first.
		return nil, fmt.Errorf("bank provider account %s: %s: %w", row.ExternalID, failureEchoMismatch, ramp.ErrBankAccountEchoMismatch)
	case models.ProviderAccountVerifying:
		return s.refresh(ctx, run, row)
	case models.ProviderAccountPending:
		if row.RegistrationRequestHash != run.Hash {
			// The same revision under the same name, but another request:
			// its old key may have created an account already, and replaying
			// it would send other bytes under it. Look the name up first; a
			// new attempt, if any, goes under a new key with the new hash.
			row.RegistrationRequestHash = run.Hash
			return s.recoverByName(ctx, run, row)
		}
		if row.RetryDeadline != nil && s.d.Now().Before(*row.RetryDeadline) {
			res, err := run.Registrar.RegisterBankAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
			if errors.Is(err, ramp.ErrIdempotencyConflict) {
				return s.recoverByName(ctx, run, row)
			}
			return s.afterPost(run, row, res, err)
		}
		return s.recoverByName(ctx, run, row)
	}
	return nil, fmt.Errorf("bank provider account %s: unexpected status %q", row.ExternalID, row.Status)
}

// resolve reads the beneficiary again under the lock — its revision is the
// one registered from here on — and the provider customer and port.
func (s *ProviderAccountService) resolve(customerID uint, extID string, provider ramp.ProviderCode, rail ramp.Rail) (*ensureRun, error) {
	b, err := s.d.Beneficiaries.FindByExternalID(customerID, extID)
	if err != nil {
		return nil, fmt.Errorf("bank provider account: %w", err)
	}
	link, err := s.d.Integrations.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return nil, fmt.Errorf("bank provider account: integration: %w", err)
	}
	if link == nil || link.ExternalCustomerID == nil || *link.ExternalCustomerID == "" {
		return nil, fmt.Errorf("bank provider account: no %s customer: %w", provider, ramp.ErrIntegrationRequired)
	}
	adapter, err := s.d.Factory.Get(provider)
	if err != nil {
		return nil, fmt.Errorf("bank provider account: %w", err)
	}
	registrar, ok := adapter.(ramp.BankAccountRegistrar)
	if !ok {
		return nil, fmt.Errorf("bank provider account: %s registers no bank accounts: %w", provider, ramp.ErrProviderNotImplemented)
	}
	run := &ensureRun{
		Registrar: registrar, Ref: ramp.NewCustomerRef(customerID, link.ExternalCustomerID), Beneficiary: b,
		Provider: provider, Rail: string(rail), Request: registrationOf(b, rail),
	}
	run.Hash = registrationHash(run.Request)
	return run, nil
}

// outdated is a live row this Ensure must not use: another revision, another
// provider customer, or a refused attempt. A pending row of another request is
// not outdated: it is recovered by name (ensureLocked), never superseded blind.
//
// A row failed on an echo mismatch is not outdated for its own revision and
// customer: the provider answered 200, so an account exists under that name,
// and registering again would make a second one. Another revision or customer
// registers under another name, so it starts over.
func (s *ProviderAccountService) outdated(row *models.CustomerBankBeneficiaryProviderAccount, run *ensureRun) bool {
	if row.BeneficiaryRevision != run.Beneficiary.Revision || row.CustomerExternalID != run.Ref.ExternalID {
		return true
	}
	return row.Status == models.ProviderAccountFailed && !isEchoMismatchRow(row)
}

func isEchoMismatchRow(row *models.CustomerBankBeneficiaryProviderAccount) bool {
	return row.FailureCode != nil && *row.FailureCode == failureEchoMismatch
}

// refresh reads a verifying account again. A failed read leaves the row as
// it is; a deprecated account is gone and is registered again.
func (s *ProviderAccountService) refresh(ctx context.Context, run *ensureRun, row *models.CustomerBankBeneficiaryProviderAccount) (*ProviderAccount, error) {
	if row.ExternalAccountID == nil {
		return nil, fmt.Errorf("bank provider account %s: verifying without an account id", row.ExternalID)
	}
	got, err := run.Registrar.GetBankAccount(ctx, run.Ref, *row.ExternalAccountID)
	if err != nil {
		return nil, fmt.Errorf("bank provider account %s: read: %w", row.ExternalID, err)
	}
	if got.Status == ramp.BankAccountDeprecated {
		if err := s.d.Accounts.Supersede(row); err != nil {
			return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, err)
		}
		return s.registerNew(ctx, run)
	}
	return s.bind(run, row, got)
}

// registerNew writes the pending row and only then POSTs, so a crash between
// the two leaves a row to replay rather than an account nobody knows of.
func (s *ProviderAccountService) registerNew(ctx context.Context, run *ensureRun) (*ProviderAccount, error) {
	now := s.d.Now()
	deadline := now.Add(registrationWindow)
	row := &models.CustomerBankBeneficiaryProviderAccount{
		ExternalID: uuid.NewString(), BeneficiaryID: run.Beneficiary.ID, BeneficiaryRevision: run.Beneficiary.Revision,
		Provider: run.Provider, Rail: run.Rail, CustomerExternalID: run.Ref.ExternalID,
		Status: models.ProviderAccountPending, RegistrationRequestHash: run.Hash, RegistrationAttempt: 1,
		RegistrationAttemptedAt: &now, RetryDeadline: &deadline,
	}
	row.RegistrationIdempotencyKey = idempotencyKeyFor(row)
	if err := s.d.Accounts.CreatePending(row); err != nil {
		if errors.Is(err, repositories.ErrProviderAccountStale) {
			return nil, fmt.Errorf("bank provider account: another registration is running: %w", ramp.ErrBankAccountVerifying)
		}
		return nil, fmt.Errorf("bank provider account: %w", err)
	}
	res, err := run.Registrar.RegisterBankAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
	return s.afterPost(run, row, res, err)
}

// recoverByName looks up by its deterministic name the account an earlier
// attempt may have created, and POSTs again under a new key when there is
// none. The row is saved — with the run's hash — before that POST.
func (s *ProviderAccountService) recoverByName(ctx context.Context, run *ensureRun, row *models.CustomerBankBeneficiaryProviderAccount) (*ProviderAccount, error) {
	found, err := run.Registrar.FindBankAccountByName(ctx, run.Ref, run.Request.Name)
	if err != nil {
		if errors.Is(err, ramp.ErrBankAccountNameAmbiguous) {
			s.d.Alertf("ramp.attention: bank provider account %s of beneficiary %s: several accounts at %s share its name; none is linked until the duplicates are removed",
				row.ExternalID, run.Beneficiary.ExternalID, row.Provider)
		}
		return nil, fmt.Errorf("bank provider account %s: find by name: %w", row.ExternalID, err)
	}
	if found != nil {
		return s.bind(run, row, found)
	}
	now := s.d.Now()
	deadline := now.Add(registrationWindow)
	row.RegistrationAttempt++
	row.RegistrationIdempotencyKey = idempotencyKeyFor(row)
	row.RegistrationAttemptedAt, row.RetryDeadline = &now, &deadline
	if err := s.d.Accounts.Save(row); err != nil {
		return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, err)
	}
	res, err := run.Registrar.RegisterBankAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
	return s.afterPost(run, row, res, err)
}

// afterPost records a POST's outcome. An answer that may still come (or may
// have been committed) leaves the row pending for a replay; a definitive
// refusal fails it, and the next Ensure starts over on a new row.
func (s *ProviderAccountService) afterPost(run *ensureRun, row *models.CustomerBankBeneficiaryProviderAccount, res *ramp.BankAccountResult, err error) (*ProviderAccount, error) {
	switch {
	case err == nil:
		return s.apply(row, res)
	case errors.Is(err, ramp.ErrOutcomeUnknown), errors.Is(err, ramp.ErrRetryableUpstream), errors.Is(err, ramp.ErrRequestNotSent):
		return nil, fmt.Errorf("bank provider account %s: still pending: %w", row.ExternalID, err)
	}
	code := failureRefused
	if errors.Is(err, ramp.ErrBankAccountEchoMismatch) {
		code = failureEchoMismatch
	}
	return nil, s.fail(run, row, code, err)
}

// bind links an account the provider already holds — found by name or read
// by GET — after an echo check of the last four digits it returned. BankAccountOut
// declares the numbers nullable, so a number left out is not compared; one
// returned and different is a mismatch, terminal like a POST's (whose echo
// stays strict, in the adapter).
func (s *ProviderAccountService) bind(run *ensureRun, row *models.CustomerBankBeneficiaryProviderAccount, res *ramp.BankAccountResult) (*ProviderAccount, error) {
	if differs(res.AccountLast4, run.Request.AccountNumber) || differs(res.RoutingLast4, run.Request.RoutingNumber) {
		id := res.ExternalAccountID
		row.ExternalAccountID = &id
		return nil, s.fail(run, row, failureEchoMismatch,
			fmt.Errorf("account %s: numbers differ: %w", id, ramp.ErrBankAccountEchoMismatch))
	}
	return s.apply(row, res)
}

// differs is a returned last4 that is not the registered number's.
func differs(returned, number string) bool { return returned != "" && returned != Last4(number) }

// fail records a definitive failure. An echo mismatch alerts once, here, and
// stays terminal; a refusal of the data itself (refusesTheData) answers
// ErrBankAccountRejected so the customer is sent to fix the beneficiary.
func (s *ProviderAccountService) fail(run *ensureRun, row *models.CustomerBankBeneficiaryProviderAccount, code string, cause error) error {
	row.Status, row.FailureCode = models.ProviderAccountFailed, &code
	if saveErr := s.d.Accounts.Save(row); saveErr != nil {
		return fmt.Errorf("bank provider account %s: %w (after %w)", row.ExternalID, saveErr, cause)
	}
	if code == failureEchoMismatch {
		s.d.Alertf("ramp.attention: bank provider account %s of beneficiary %s: %s at %s; the provider holds an account the registration does not match, and it is not registered again",
			row.ExternalID, run.Beneficiary.ExternalID, failureEchoMismatch, row.Provider)
	}
	if code == failureRefused && refusesTheData(cause) {
		return fmt.Errorf("bank provider account %s: %s: %w: %w", row.ExternalID, code, ramp.ErrBankAccountRejected, cause)
	}
	return fmt.Errorf("bank provider account %s: %s: %w", row.ExternalID, code, cause)
}

// refusesTheData is a provider answering 400 or 422 to the registration with
// no condition of its own: the account as the user typed it.
func refusesTheData(err error) bool { return ramp.RefusesTheData(err) }

// apply stores what the provider answered for the row's account and answers
// the account only when it is approved.
func (s *ProviderAccountService) apply(row *models.CustomerBankBeneficiaryProviderAccount, res *ramp.BankAccountResult) (*ProviderAccount, error) {
	now := s.d.Now()
	id := res.ExternalAccountID
	row.ExternalAccountID, row.LastCheckedAt = &id, &now
	switch res.Status {
	case ramp.BankAccountApproved:
		row.Status = models.ProviderAccountApproved
	case ramp.BankAccountRejected:
		row.Status = models.ProviderAccountRejected
	case ramp.BankAccountDeprecated:
		row.Status = models.ProviderAccountSuperseded
	default:
		row.Status = models.ProviderAccountVerifying
	}
	if err := s.d.Accounts.Save(row); err != nil {
		return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, err)
	}
	switch row.Status {
	case models.ProviderAccountApproved:
		return accountOf(row), nil
	case models.ProviderAccountRejected:
		return nil, fmt.Errorf("bank provider account %s: %w", row.ExternalID, ramp.ErrBankAccountRejected)
	}
	return nil, fmt.Errorf("bank provider account %s: %s: %w", row.ExternalID, row.Status, ramp.ErrBankAccountVerifying)
}

// registrationOf is the registration of the beneficiary's revision on rail,
// named so that it can be found again past the key window.
func registrationOf(b *repositories.BankBeneficiaryRecord, rail ramp.Rail) ramp.BeneficiaryAccountRegistration {
	return ramp.BeneficiaryAccountRegistration{
		Name: ramp.BankAccountName(b.ExternalID, b.Revision, rail), Rail: rail,
		Relationship: b.Relationship, AccountClass: b.AccountClass, AccountType: b.AccountType,
		BeneficiaryName: b.BeneficiaryName, RoutingNumber: b.RoutingNumber, AccountNumber: b.AccountNumber,
		AddressLine1: b.AddressLine1, AddressLine2: b.AddressLine2, City: b.City,
		StateCode: b.StateCode, PostalCode: b.PostalCode, BusinessIndustry: b.BusinessIndustry,
	}
}

// registrationHash digests the registration's fields (not its String, which
// hides them), so a replay is sent only for the very request it repeats. It
// is keyed like E3's fingerprint: the row keeps the clear last4s beside it,
// and an unkeyed digest of a few known fields is a brute force away.
func registrationHash(r ramp.BeneficiaryAccountRegistration) string {
	type plain ramp.BeneficiaryAccountRegistration // drops the Format/String methods
	raw, _ := json.Marshal(plain(r))
	return keyedhash.Hex("bank-account-registration:" + string(raw))
}

func idempotencyKeyFor(row *models.CustomerBankBeneficiaryProviderAccount) string {
	return "bank-account:" + row.ExternalID + ":" + strconv.Itoa(row.RegistrationAttempt)
}
