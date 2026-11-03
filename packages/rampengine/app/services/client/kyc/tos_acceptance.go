package kyc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/facades"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
)

// TosAcceptance is one provider's signed agreement, as the client reports it
// after the user accepted that provider's terms.
type TosAcceptance struct {
	Provider          string
	SignedAgreementID string
	// LegalName is who accepted, for a provider that takes XIP's word for it.
	// It comes from the client because it is the name the user typed into the
	// KYC form a step earlier, and nothing on this side holds it yet: the
	// application that carries it has not been submitted, and `users` has no
	// name column. It must match the one the application later sends, or the
	// provider is told about two different people.
	LegalName string
	// IPAddress and UserAgent are the evidence of where the acceptance came
	// from, taken from the request that reported it rather than from its body.
	IPAddress string
	UserAgent string
}

// RecordTosAcceptances stores each provider's signed agreement on its own row.
//
// The agreement is issued by one provider and means nothing at another, so it
// cannot travel on the application body: that body goes to every provider, and
// a single field there could only ever carry one of them. There was no fix for
// that shape, only a guard to fail earlier.
//
// Recording it as it happens rather than at the end of the terms step also
// survives the app closing: a user who accepted one provider's terms and left
// does not accept them twice. That makes the server the source of truth for
// which terms are signed, which is what the client's step derives from.
//
// The batch is one answer to the client: applying half of it under an error
// it is told to retry leaves the two sides disagreeing about which providers
// are signed, and the signed half is then skipped by StartTosForEnabled — so
// the retry cannot even show the user what is left. Every entry is therefore
// checked, and — for an attesting provider — attested upstream, before any of
// them is written, in three passes rather than one:
//
//  1. checkTosEntries reads what each entry needs (the adapter, whether it
//     hosts its own page or takes an attestation, an attesting entry's open
//     session) and fails the whole batch on the first entry that cannot even
//     be attempted. Nothing here reaches a provider.
//  2. attestTosEntries is the only pass that does: it calls AcceptTos for
//     each attesting entry in order, stopping at the first refusal.
//  3. writeTosEntries persists every entry now that every one of them has
//     cleared both earlier passes.
//
// The cost of the shape: for an attesting provider, "checked" in pass 1 can
// only mean "a session exists to attest", not "attestation will succeed" —
// that answer comes from calling the provider, which is pass 2's job. If a
// batch names two attesting providers and the second's AcceptTos call fails,
// the first has already told its provider the user accepted, with no local
// row yet to show for it, because pass 3 has not run. The provider and this
// side disagree until the client retries — at which point RecordTosAcceptances
// runs again and re-attests the first entry. That repeat call is a second
// round trip, not a free one, and whether it is safe is a property of the
// provider's own AcceptTos, not of this method: today's only attester, Ozav,
// documents its AcceptTos as idempotent upstream for exactly this reason
// (app/adapters/ramp/ozav/tos.go), so the retry heals with one extra round
// trip and no session invalidation. A future attester without that guarantee
// would need its own answer here — an idempotency key, most likely — rather
// than inheriting this one's contract by assumption.
//
// Reversing the risk — writing pass 3 before calling out in pass 2 — was
// rejected: that is the bug this method used to have, only moved. A local
// write can still be rolled back if a later entry fails; a provider that has
// already recorded an acceptance cannot be un-told.
func (s *ProviderKycService) RecordTosAcceptances(
	ctx context.Context, user *models.User, customerID uint, acceptances []TosAcceptance,
) ([]models.CustomerProviderKyc, error) {
	checks, err := s.checkTosEntries(customerID, acceptances)
	if err != nil {
		return nil, err
	}
	if err := s.attestTosEntries(ctx, user, customerID, acceptances, checks); err != nil {
		return nil, err
	}
	return s.writeTosEntries(ctx, customerID, acceptances, checks)
}

// tosEntryCheck is what pass 1 established about one acceptance, so passes 2
// and 3 do not re-derive it — in particular, so pass 3 never re-reads the row
// a hosted entry was checked against: the check happens once, before any
// entry in the batch is touched, and is not repeated under the write lock.
type tosEntryCheck struct {
	adapter ramp.ProviderAdapter
	// attester is nil for a hosted provider — the type assertion in
	// checkTosEntries is the whole distinction, and the rest of the batch
	// reads it from here rather than re-asserting.
	attester ramp.TosAttester
	// sessionID is an attesting provider's own session token, read once here
	// so attestTosEntries does not load the row a second time.
	sessionID string
}

// checkTosEntries is RecordTosAcceptances' pass 1: everything that can be
// decided from this side's own state, for every entry, before any of them is
// written or any provider is called. It stops at the first entry that fails —
// a later entry's check is worth nothing once the batch has already failed.
//
// It is itself two loops, cheapest first, for the same reason StartTos checks
// RequiresTos before starting an integration: reading this side's own
// database for an entry a later, free check is about to fail anyway is a
// round trip bought for nothing. The first loop costs nothing but a map
// lookup and an interface check; only once every entry has cleared it does
// the second loop spend a query on any of them.
func (s *ProviderKycService) checkTosEntries(customerID uint, acceptances []TosAcceptance) ([]tosEntryCheck, error) {
	checks := make([]tosEntryCheck, len(acceptances))
	for i, acceptance := range acceptances {
		adapter, err := s.factory.Get(acceptance.Provider)
		if err != nil {
			return nil, err
		}
		// Recording a signature for a provider that issues none would put an
		// agreement on the record that nobody ever presented.
		if !adapter.RequiresTos() {
			return nil, ramp.ErrTosNotRequired
		}
		checks[i].adapter = adapter
	}

	for i, acceptance := range acceptances {
		// An attesting provider never saw the user accept — it takes XIP's
		// word, sent before the row may claim anything. The type assertion is
		// the whole distinction: a provider that hosts its own terms page does
		// not implement this, and never reaches the branch.
		if attester, attested := checks[i].adapter.(ramp.TosAttester); attested {
			sessionID, err := s.checkTosSession(customerID, acceptance)
			if err != nil {
				return nil, err
			}
			checks[i].attester = attester
			checks[i].sessionID = sessionID
		} else {
			opened, err := s.tosSessionOpened(customerID, acceptance.Provider)
			if err != nil {
				return nil, err
			}
			if !opened {
				// The hosted twin of checkTosSession's own check: the id is
				// the provider's word relayed by the client, and nothing
				// about it can be verified here. What can be is that this
				// side opened a session at all — without that a stale id, a
				// truncated postMessage or a typo is recorded as a
				// signature, and the provider then refuses the customer it
				// is presented on.
				return nil, ramp.ErrTosSessionUnknown
			}
		}
	}
	return checks, nil
}

// checkTosSession is the local half of what attestTos used to check before
// calling out: a session token this side actually holds, and a name to
// present it under. Splitting it out is what lets checkTosEntries run it for
// every entry before attestTosEntries dials a single provider.
func (s *ProviderKycService) checkTosSession(customerID uint, acceptance TosAcceptance) (string, error) {
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, acceptance.Provider)
	if err != nil {
		return "", fmt.Errorf("kyc: load kyc row: %w", err)
	}
	// No token means no session was ever opened for this user, whatever the
	// client believes. Attesting against nothing would leave the row signed
	// and the customer create refused, with no step left to send them back to.
	if row == nil || row.TosSessionID == nil || *row.TosSessionID == "" {
		return "", ramp.ErrTosSessionUnknown
	}
	// The provider takes our word for who accepted, and the name is the whole
	// of that word.
	if acceptance.LegalName == "" {
		return "", ramp.ErrTosLegalNameRequired
	}
	return *row.TosSessionID, nil
}

// tosSessionOpened reports whether a hosted provider's own terms page was
// ever presented to this user. An unlocked read, like StartTosForEnabled's
// single signed-terms read and for the same reason: this decides whether the
// batch may proceed at all, not what gets written, and writeTosEntries reads
// the row again under lock when it actually persists it.
//
// Unlike that read, a failure here is returned rather than swallowed. Its
// silence is justified by its own consequence — offering a terms page again
// costs the user a page they can close. Here the consequence
// is a compliance batch refused with a client error code (tos_session_unknown,
// "you never opened this") for what was actually a database fault; that is
// not an honest answer to give, and it sends the client through the terms
// step to fix something that was never broken.
//
// This trades a narrow window for the guarantee above: a concurrent event
// that resets this same row between this read and the write in pass 3 (a KYC
// submission refused over stale terms — releaseInFlight's own ResetTos call)
// would not be caught a second time. It is not the same exposure the
// "recording a signature nobody presented" check above guards against: at the
// moment this read passes, a session genuinely was open, so what it protects
// against is a typo or a replay, not a race with the row's own future. The
// alternative — re-checking under the write lock, as the code used to — is
// exactly the shape this method no longer has: a check inside the per-entry
// write step, which is what let an earlier entry's write survive a later
// entry's failure.
func (s *ProviderKycService) tosSessionOpened(customerID uint, provider string) (bool, error) {
	row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
	if err != nil {
		return false, fmt.Errorf("kyc: load kyc row: %w", err)
	}
	if row == nil {
		return false, nil
	}
	return row.TosLinkURL != nil && *row.TosLinkURL != "", nil
}

// attestTosEntries is RecordTosAcceptances' pass 2, the only one that reaches
// a provider. It runs once every entry has cleared checkTosEntries, in the
// batch's own order, and stops at the first refusal — a later entry is never
// attested once an earlier one has already failed the batch.
//
// The attestation names user, who accepted, by their account e-mail.
func (s *ProviderKycService) attestTosEntries(
	ctx context.Context, user *models.User, customerID uint, acceptances []TosAcceptance, checks []tosEntryCheck,
) error {
	for i, acceptance := range acceptances {
		attester := checks[i].attester
		if attester == nil {
			continue
		}
		if err := s.attestTos(ctx, customerID, acceptance, attester, checks[i].sessionID, user.Email); err != nil {
			return err
		}
	}
	return nil
}

// writeTosEntries is RecordTosAcceptances' pass 3: every entry has cleared
// checkTosEntries and, if it attests, attestTosEntries — nothing from here on
// can fail on anything the client did wrong, only on this side's own
// database.
func (s *ProviderKycService) writeTosEntries(
	ctx context.Context, customerID uint, acceptances []TosAcceptance, checks []tosEntryCheck,
) ([]models.CustomerProviderKyc, error) {
	updated := make([]models.CustomerProviderKyc, 0, len(acceptances))
	for i, acceptance := range acceptances {
		check := checks[i]
		agreementID := acceptance.SignedAgreementID
		var acceptedAt *time.Time
		if check.attester != nil {
			// The session token is the agreement id for an attesting
			// provider — the client never sees one of its own. It was
			// confirmed accepted upstream in pass 2; this pass only writes it
			// down.
			agreementID = check.sessionID
			now := time.Now()
			acceptedAt = &now
		}

		// On the status read, and read again when it moved: see startTos.
		err := s.locker.WithLock(ctx, lockKey("tos", customerID, acceptance.Provider), func(context.Context) error {
			return untilSettled(func() error {
				row, isNew, err := s.loadRow(customerID, acceptance.Provider, check.adapter)
				if err != nil {
					return err
				}
				row.SignedAgreementID = &agreementID
				row.TosAcceptedAt = acceptedAt
				// Our own record that the user signed. The provider confirms it on
				// the customer it later creates, and that confirmation is what
				// overwrites this on the next sync.
				row.TosStatus = models.TosStatusApproved
				if err := s.saveRowUnmoved(row, isNew); err != nil {
					return err
				}
				updated = append(updated, *row)
				return nil
			})
		})
		if err != nil {
			return nil, err
		}
	}
	return updated, nil
}

// attestTos tells the provider the user accepted. sessionID and email are
// what checkTosEntries and attestTosEntries already resolved, so this makes
// exactly one call: the upstream one.
//
// It runs outside the row lock deliberately, for the reason startTos gives
// before it takes the lock.
func (s *ProviderKycService) attestTos(
	ctx context.Context, customerID uint, acceptance TosAcceptance, attester ramp.TosAttester,
	sessionID, email string,
) error {
	err := attester.AcceptTos(ctx, ramp.NewCustomerRef(customerID, nil), ramp.TosAttestation{
		SessionID: sessionID,
		LegalName: acceptance.LegalName,
		Email:     email,
		IPAddress: acceptance.IPAddress,
		UserAgent: acceptance.UserAgent,
	})
	if err != nil {
		// A session the provider will not honour must not stay on the row:
		// the next terms step reads it as live, presents it again, and fails
		// identically forever. Forgetting it is what lets the step recover.
		if errors.Is(err, ramp.ErrTosSessionExpired) || errors.Is(err, ramp.ErrTosSessionUnknown) {
			s.forgetTosSession(ctx, customerID, acceptance.Provider)
		}
		return fmt.Errorf("kyc: attest tos at %s: %w", acceptance.Provider, err)
	}
	return nil
}

// forgetTosSession drops a refused session from the row so the terms step can
// mint a fresh one. A failure to persist is logged rather than returned: the
// caller is already reporting why the acceptance did not take, and replacing
// that reason with a database error would hide it.
func (s *ProviderKycService) forgetTosSession(ctx context.Context, customerID uint, provider string) {
	err := s.locker.WithLock(ctx, lockKey("tos", customerID, provider), func(context.Context) error {
		return untilSettled(func() error {
			row, err := s.kycRepo.FindByCustomerAndProvider(customerID, provider)
			if err != nil || row == nil {
				return err
			}
			row.ResetTos()
			return s.saveRowUnmoved(row, false)
		})
	})
	if err != nil {
		facades.Log().WithContext(ctx).Errorf("kyc: forget refused tos session at %s: %v", provider, err)
	}
}
