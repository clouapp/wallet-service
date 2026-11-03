package ramps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/client/pixkeys"
	"github.com/macrowallets/waas/packages/rampengine/app/services/locking"
	"github.com/macrowallets/waas/packages/rampengine/app/support/brazil"
	"github.com/macrowallets/waas/packages/rampengine/app/support/keyedhash"
)

// pixRegistrationWindow is how long a provider keeps an Idempotency-Key:
// within it a pending row is replayed with its key, past it the row is looked
// up by its deterministic name.
const pixRegistrationWindow = 24 * time.Hour

// failure_code of a failed destination row.
const (
	pixFailureRefused      = "pix_account_refused"
	pixFailureEchoMismatch = "echo_mismatch"
)

// PixAccountEnsurer answers the registered, approved provider account (ba_)
// of a Pix destination, registering it first if it has to. Production adapts
// PixAccountService to it, whose errors pass through unchanged.
type PixAccountEnsurer interface {
	Ensure(ctx context.Context, registrar ramp.PixAccountRegistrar, ref ramp.CustomerRef, customer *models.Customer, in dto.RegisterPixAccountDTO) (string, error)
}

// PixAccountService registers a Pix destination at a provider that holds it
// as an object of its own, once per customer, provider, key and document. It
// never registers twice: the pending row is written before the POST, a replay
// reuses its key and bytes, and past the key window the deterministic name
// finds what an earlier attempt created. It mirrors the bank beneficiary's
// registration (bank.ProviderAccountService) on offramp_destinations.
type PixAccountService struct {
	destinations repositories.OfframpDestinationRepository
	profiles     repositories.CustomerIndividualProfileRepository
	locker       locking.Locker
	now          func() time.Time
	// alertf logs at Error level for a human to act on; ids only.
	alertf func(format string, args ...any)
}

func NewPixAccountService(
	destinations repositories.OfframpDestinationRepository,
	profiles repositories.CustomerIndividualProfileRepository,
	locker locking.Locker,
) *PixAccountService {
	return &PixAccountService{
		destinations: destinations, profiles: profiles, locker: locker, now: time.Now,
		alertf: func(format string, args ...any) { facades.Log().Errorf(format, args...) },
	}
}

// WithClock replaces the clock and the alert sink; for tests.
func (s *PixAccountService) WithClock(now func() time.Time, alertf func(string, ...any)) *PixAccountService {
	s.now, s.alertf = now, alertf
	return s
}

// pixRun is one Ensure's resolved inputs. Its fields are exported so that even
// a stray %v of it goes through the registration's Format (fmt does not call
// methods on unexported fields). It is never logged nor put in an error.
type pixRun struct {
	Registrar ramp.PixAccountRegistrar
	Ref       ramp.CustomerRef
	Identity  models.OfframpDestination
	Request   ramp.PixAccountRegistration
	Hash      string
}

// Ensure answers the provider account id of the destination.
//
// Errors: ramp.ErrIntegrationRequired, ramp.ErrBankAccountVerifying,
// ramp.ErrBankAccountRejected (the provider refused the account or, with a
// 400/422, the data itself), ramp.ErrBankAccountEchoMismatch, and the upstream
// errors a retry may cure. They carry ids and statuses only, never a key, a
// document or a name.
func (s *PixAccountService) Ensure(
	ctx context.Context, registrar ramp.PixAccountRegistrar, ref ramp.CustomerRef, customer *models.Customer, in dto.RegisterPixAccountDTO,
) (string, error) {
	run, err := s.resolve(registrar, ref, customer, in)
	if err != nil {
		return "", err
	}
	lockKey := fmt.Sprintf("pix-account:%d:%s:%s:%s", run.Identity.CustomerID, run.Identity.Provider,
		run.Identity.PixKeyHash, run.Identity.DocumentHash)
	var out string
	err = s.locker.WithLock(ctx, lockKey, func(ctx context.Context) error {
		id, err := s.ensureLocked(ctx, run)
		out = id
		return err
	})
	if err != nil {
		return "", err
	}
	return out, nil
}

func (s *PixAccountService) resolve(
	registrar ramp.PixAccountRegistrar, ref ramp.CustomerRef, customer *models.Customer, in dto.RegisterPixAccountDTO,
) (*pixRun, error) {
	if ref.ExternalID == "" {
		return nil, fmt.Errorf("pix account: no %s customer: %w", in.Provider, ramp.ErrIntegrationRequired)
	}
	identity := models.OfframpDestination{
		CustomerID: customer.ID, Provider: in.Provider,
		PixKeyHash:   pixkeys.HashKey(in.PixKeyType, in.PixKey),
		DocumentHash: pixkeys.DestinationDocumentHash(in.PixKeyType, in.RecipientDocument),
	}
	document := in.RecipientDocument
	if t := strings.ToLower(in.PixKeyType); t == "cpf" || t == "cnpj" {
		document = in.PixKey
	}
	relationship, err := s.relationship(customer, document)
	if err != nil {
		return nil, err
	}
	reg := ramp.PixAccountRegistration{
		Name:       ramp.PixAccountName(identity.PixKeyHash, identity.DocumentHash),
		PixKeyType: strings.ToLower(in.PixKeyType), PixKey: brazil.NormalizePixKey(in.PixKeyType, in.PixKey),
		RecipientName: in.RecipientName, RecipientDocument: brazil.CanonicalDocument(document),
		Relationship: relationship,
	}
	return &pixRun{Registrar: registrar, Ref: ref, Identity: identity, Request: reg, Hash: pixRegistrationHash(reg)}, nil
}

// relationship is first_party when the document the key is held under is the
// customer's own: the person's CPF, or the company's CNPJ. It is compared by
// keyed digest, so no document is read in clear from the store. A person with
// no retained profile is not provably the holder, so the answer is other.
func (s *PixAccountService) relationship(customer *models.Customer, document string) (string, error) {
	if document == "" {
		return ramp.RelationshipOther, nil
	}
	if customer.IsBusiness() {
		if customer.TaxIDHash != nil && pixkeys.HashKey("cnpj", document) == *customer.TaxIDHash {
			return ramp.RelationshipFirstParty, nil
		}
		return ramp.RelationshipOther, nil
	}
	profile, err := s.profiles.Find(customer.ID)
	if err != nil {
		return "", fmt.Errorf("pix account: read the holder's profile: %w", err)
	}
	if profile == nil {
		return ramp.RelationshipOther, nil
	}
	own, _ := repositories.IdentityDigests(*profile)
	if own != nil && profile.TaxIdentity().IsCPF() && pixkeys.HashKey("cpf", document) == *own {
		return ramp.RelationshipFirstParty, nil
	}
	return ramp.RelationshipOther, nil
}

func (s *PixAccountService) ensureLocked(ctx context.Context, run *pixRun) (string, error) {
	row, err := s.destinations.FindForRegistration(run.Identity)
	if err != nil {
		return "", fmt.Errorf("pix account: %w", err)
	}
	if row == nil {
		return s.registerNew(ctx, run)
	}
	switch row.Status {
	case models.DestinationReady:
		if row.DestinationRef == "" {
			return "", fmt.Errorf("pix account %d: ready without an account id", row.ID)
		}
		return row.DestinationRef, nil
	case models.DestinationRejected:
		return "", fmt.Errorf("pix account %d: %w", row.ID, ramp.ErrBankAccountRejected)
	case models.DestinationFailed:
		if row.FailureCode != nil && *row.FailureCode == pixFailureEchoMismatch {
			// The provider holds an account under this name that a human must
			// look at first; registering again would make a second one.
			return "", fmt.Errorf("pix account %d: %s: %w", row.ID, pixFailureEchoMismatch, ramp.ErrBankAccountEchoMismatch)
		}
		row.Status = models.DestinationPending
		row.RegistrationRequestHash = run.Hash
		return s.recoverByName(ctx, run, row)
	case models.DestinationVerifying:
		return s.refresh(ctx, run, row)
	case models.DestinationPending:
		if row.RegistrationRequestHash != run.Hash {
			// The same identity, another request: its old key may have created
			// an account already, and replaying it would send other bytes under
			// it. Look the name up first.
			row.RegistrationRequestHash = run.Hash
			return s.recoverByName(ctx, run, row)
		}
		if row.RetryDeadline != nil && s.now().Before(*row.RetryDeadline) {
			res, err := run.Registrar.RegisterPixAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
			if errors.Is(err, ramp.ErrIdempotencyConflict) {
				return s.recoverByName(ctx, run, row)
			}
			return s.afterPost(run, row, res, err)
		}
		return s.recoverByName(ctx, run, row)
	}
	return "", fmt.Errorf("pix account %d: unexpected status %q", row.ID, row.Status)
}

// refresh reads a verifying account again. A failed read leaves the row as it
// is; a deprecated account is gone and is registered again.
func (s *PixAccountService) refresh(ctx context.Context, run *pixRun, row *models.OfframpDestination) (string, error) {
	got, err := run.Registrar.GetPixAccount(ctx, run.Ref, row.DestinationRef)
	if err != nil {
		return "", fmt.Errorf("pix account %d: read: %w", row.ID, err)
	}
	if got.Status == ramp.BankAccountDeprecated {
		row.Status, row.DestinationRef = models.DestinationPending, ""
		return s.recoverByName(ctx, run, row)
	}
	return s.apply(row, got)
}

// registerNew writes the pending row and only then POSTs, so a crash between
// the two leaves a row to replay rather than an account nobody knows of.
func (s *PixAccountService) registerNew(ctx context.Context, run *pixRun) (string, error) {
	deadline := s.now().Add(pixRegistrationWindow)
	row := run.Identity
	row.Name, row.RegistrationRequestHash, row.RegistrationAttempt = run.Request.Name, run.Hash, 1
	row.RetryDeadline, row.ProviderCustomerID = &deadline, run.Ref.ExternalID
	row.RegistrationIdempotencyKey = pixIdempotencyKey(run, 1)
	created, err := s.destinations.CreatePending(&row)
	if err != nil {
		return "", fmt.Errorf("pix account: %w", err)
	}
	if !created {
		// Another caller wrote the row between our read and our write; it is
		// registering, and this call is told to ask again.
		return "", fmt.Errorf("pix account: another registration is running: %w", ramp.ErrBankAccountVerifying)
	}
	res, err := run.Registrar.RegisterPixAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
	return s.afterPost(run, &row, res, err)
}

// recoverByName looks up by its deterministic name the account an earlier
// attempt may have created, and POSTs again under a new key when there is
// none. The row is saved — with the run's hash — before that POST.
func (s *PixAccountService) recoverByName(ctx context.Context, run *pixRun, row *models.OfframpDestination) (string, error) {
	found, err := run.Registrar.FindPixAccountByName(ctx, run.Ref, run.Request.Name)
	if err != nil {
		if errors.Is(err, ramp.ErrBankAccountNameAmbiguous) {
			s.alertf("ramp.attention: pix account %d at %s: several accounts share its name; none is linked until the duplicates are removed",
				row.ID, row.Provider)
		}
		return "", fmt.Errorf("pix account %d: find by name: %w", row.ID, err)
	}
	if found != nil {
		return s.apply(row, found)
	}
	deadline := s.now().Add(pixRegistrationWindow)
	row.Status, row.FailureCode = models.DestinationPending, nil
	row.RegistrationAttempt++
	row.RegistrationIdempotencyKey = pixIdempotencyKey(run, row.RegistrationAttempt)
	row.RetryDeadline, row.ProviderCustomerID = &deadline, run.Ref.ExternalID
	row.Name, row.RegistrationRequestHash = run.Request.Name, run.Hash
	if err := s.destinations.SaveRegistration(row); err != nil {
		return "", fmt.Errorf("pix account %d: %w", row.ID, err)
	}
	res, err := run.Registrar.RegisterPixAccount(ctx, run.Ref, run.Request, row.RegistrationIdempotencyKey)
	return s.afterPost(run, row, res, err)
}

// afterPost records a POST's outcome. An answer that may still come (or may
// have been committed) leaves the row pending for a replay; a definitive
// refusal fails it.
func (s *PixAccountService) afterPost(run *pixRun, row *models.OfframpDestination, res *ramp.PixAccountResult, err error) (string, error) {
	switch {
	case err == nil:
		return s.apply(row, res)
	case errors.Is(err, ramp.ErrOutcomeUnknown), errors.Is(err, ramp.ErrRetryableUpstream), errors.Is(err, ramp.ErrRequestNotSent):
		return "", fmt.Errorf("pix account %d: still pending: %w", row.ID, err)
	}
	code := pixFailureRefused
	if errors.Is(err, ramp.ErrBankAccountEchoMismatch) {
		code = pixFailureEchoMismatch
	}
	return "", s.fail(row, code, err)
}

// fail records a definitive failure. An echo mismatch alerts once, here, and
// stays terminal; a refusal of the data itself answers ErrBankAccountRejected.
func (s *PixAccountService) fail(row *models.OfframpDestination, code string, cause error) error {
	row.Status, row.FailureCode = models.DestinationFailed, &code
	if saveErr := s.destinations.SaveRegistration(row); saveErr != nil {
		return fmt.Errorf("pix account %d: %w (after %w)", row.ID, saveErr, cause)
	}
	if code == pixFailureEchoMismatch {
		s.alertf("ramp.attention: pix account %d at %s: %s; the provider holds an account the registration does not match, and it is not registered again",
			row.ID, row.Provider, pixFailureEchoMismatch)
	}
	if code == pixFailureRefused && ramp.RefusesTheData(cause) {
		return fmt.Errorf("pix account %d: %s: %w: %w", row.ID, code, ramp.ErrBankAccountRejected, cause)
	}
	return fmt.Errorf("pix account %d: %s: %w", row.ID, code, cause)
}

// apply stores what the provider answered for the row's account and answers
// the account id only when it is approved.
func (s *PixAccountService) apply(row *models.OfframpDestination, res *ramp.PixAccountResult) (string, error) {
	row.DestinationRef, row.FailureCode = res.ExternalAccountID, nil
	switch res.Status {
	case ramp.BankAccountApproved:
		row.Status = models.DestinationReady
	case ramp.BankAccountRejected:
		row.Status = models.DestinationRejected
	default:
		row.Status = models.DestinationVerifying
	}
	if err := s.destinations.SaveRegistration(row); err != nil {
		return "", fmt.Errorf("pix account %d: %w", row.ID, err)
	}
	switch row.Status {
	case models.DestinationReady:
		return row.DestinationRef, nil
	case models.DestinationRejected:
		return "", fmt.Errorf("pix account %d: %w", row.ID, ramp.ErrBankAccountRejected)
	}
	return "", fmt.Errorf("pix account %d: %s: %w", row.ID, row.Status, ramp.ErrBankAccountVerifying)
}

// pixRegistrationHash digests the registration's fields (not its String,
// which hides them), keyed, so a replay is sent only for the very request it
// repeats and the row never keeps an unkeyed digest of a key.
func pixRegistrationHash(r ramp.PixAccountRegistration) string {
	type plain ramp.PixAccountRegistration // drops the Format/String methods
	raw, _ := json.Marshal(plain(r))
	return keyedhash.Hex("pix-account-registration:" + string(raw))
}

// pixIdempotencyKey names attempt n of the identity's registration. It holds
// the account's deterministic name, never the key.
func pixIdempotencyKey(run *pixRun, attempt int) string {
	return "pix-account:" + run.Request.Name + ":" + strconv.Itoa(attempt)
}
