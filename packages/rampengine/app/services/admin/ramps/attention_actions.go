package ramps

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	auditdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/audit"
	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/ramps"
	clientdto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/client/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/services/admin/audit"
	kycsvc "github.com/macrowallets/waas/packages/rampengine/app/services/client/kyc"
	clientramps "github.com/macrowallets/waas/packages/rampengine/app/services/client/ramps"
)

// The bounds of a support ticket, in characters (the columns' sizes).
const (
	SupportTicketReferenceMaxLength = 64
	SupportTicketNoteMaxLength      = 500
)

// The refusals of the attention actions. The controller maps these only; the
// ones below the services that act are aliases or are translated here.
var (
	// ErrAttentionNotFound is an action on an id no intent, finalization or
	// transaction has.
	ErrAttentionNotFound = errors.New("admin ramps: attention item not found")
	// ErrAttentionNotResolvable is an item no longer in the state the action
	// takes: already resolved (the second click), owned by the automation, or
	// a transaction whose money the provider no longer holds.
	ErrAttentionNotResolvable = errors.New("admin ramps: attention item is not open to this action")
	// ErrInvalidResolution is a body that is not exactly one verdict, or a
	// ticket outside its bounds.
	ErrInvalidResolution = errors.New("admin ramps: invalid resolution")
	// ErrCustomerConflict refuses linking a customer when the person's link
	// already names another one.
	ErrCustomerConflict = kycsvc.ErrIntentCustomerConflict
	// ErrBindingMismatch is a payout that is not the one the authorization was
	// bound to: nothing is bound.
	ErrBindingMismatch = clientramps.ErrBindingMismatch
	// ErrQuoteStillValid refuses "not created" while the quote could still
	// produce the payout.
	ErrQuoteStillValid = clientramps.ErrQuoteStillValid
	// ErrPayoutExists refuses "not created" when the provider's listing shows
	// a payout for the quote.
	ErrPayoutExists = clientramps.ErrPayoutExists
	// ErrOrderNotMovable is a verdict on an order another path already closed
	// (expired, failed): the verdict rolled back.
	ErrOrderNotMovable = repositories.ErrOrderNotMovable
	// ErrProviderFailed is the provider failing while the action asked it.
	ErrProviderFailed = ramp.ErrProviderFailed
)

// ticketStatuses are the statuses whose money the provider holds, the ones a
// support ticket is opened about.
var ticketStatuses = []string{models.TxStatusRefundAttention, models.TxStatusReturning}

// IntentResolver closes a KYC intent held for review
// (kycsvc.ProviderKycService).
type IntentResolver interface {
	ResolveIntent(ctx context.Context, r kycsvc.IntentResolution) error
}

// FinalizationResolver closes a finalization the recovery handed to an
// operator (clientramps.FinalizationRecovery).
type FinalizationResolver interface {
	Resolve(ctx context.Context, authExternalID string, in clientdto.ResolveFinalizationDTO) error
}

// AttentionActionsDeps is what the attention actions are built from.
type AttentionActionsDeps struct {
	Transactions   repositories.TransactionRepository
	Authorizations repositories.OfframpFundingAuthorizationRepository
	Intents        repositories.ProviderKycSubmissionIntentRepository
	Kyc            IntentResolver
	Finalizations  FinalizationResolver
	Audit          *audit.Service
	// Now is the clock of the ticket's time; nil is time.Now.
	Now func() time.Time
}

// AttentionActions are the operator's verdicts on what the attention queue
// holds. A verdict is the client services' own (ResolveIntent, Resolve): this
// service checks the item is there and open, hands the verdict over, and
// records the act once it landed. A ticket is a database write only, and its
// audit row lands with it.
type AttentionActions struct {
	txs           repositories.TransactionRepository
	auths         repositories.OfframpFundingAuthorizationRepository
	intents       repositories.ProviderKycSubmissionIntentRepository
	kyc           IntentResolver
	finalizations FinalizationResolver
	audit         *audit.Service
	now           func() time.Time
}

// NewAttentionActions wires the actions.
func NewAttentionActions(d AttentionActionsDeps) *AttentionActions {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	return &AttentionActions{
		txs: d.Transactions, auths: d.Authorizations, intents: d.Intents,
		kyc: d.Kyc, finalizations: d.Finalizations, audit: d.Audit, now: now,
	}
}

// ResolveKycIntent closes a KYC intent in manual review: a customer id
// reconciles it with that provider customer, abandon cancels it. The reason
// on record is the admin who acted, never free text.
//
// The KYC service links the customer and asks the provider where it stands,
// so the act is recorded after it landed and a failed insert does not undo
// it.
func (s *AttentionActions) ResolveKycIntent(
	ctx context.Context, actor auditdto.ActorDTO, id string, in dto.ResolveKycIntentDTO,
) (*dto.AttentionResolutionDTO, error) {
	customerID := strings.TrimSpace(in.CustomerID)
	if (customerID == "") == !in.Abandon || strings.ContainsAny(customerID, "\r\n") {
		return nil, ErrInvalidResolution
	}
	intent, _, err := s.intents.FindByExternalID(id)
	// Material that does not open does not stop a verdict: the state decides.
	if err != nil && !errors.Is(err, repositories.ErrKycIntentIntegrity) {
		return nil, fmt.Errorf("admin ramps: load kyc intent: %w", err)
	}
	if intent == nil {
		return nil, ErrAttentionNotFound
	}
	if intent.State != models.KycIntentManualReview {
		return nil, ErrAttentionNotResolvable
	}

	outcome := models.KycIntentOutcomeReconciled
	if in.Abandon {
		outcome = models.KycIntentOutcomeCancelled
	}
	err = s.kyc.ResolveIntent(ctx, kycsvc.IntentResolution{
		ExternalID: intent.ExternalID, Outcome: outcome, Reason: resolvedBy(actor), CustomerID: customerID,
	})
	if err != nil {
		return nil, kycRefusal(err)
	}

	s.audit.RecordIrreversible(actor, auditdto.EntryDTO{
		Action: audit.ActionRampsKycIntentResolved, SubjectType: audit.SubjectCustomers, SubjectID: intent.CustomerID,
		Provider: intent.Provider, Metadata: map[string]any{"intent_id": intent.ExternalID, "outcome": outcome},
	})
	return &dto.AttentionResolutionDTO{
		Kind: dto.AttentionKindKycIntent, ID: intent.ExternalID, CustomerID: intent.CustomerID, Outcome: outcome,
	}, nil
}

// kycRefusal translates what ResolveIntent refused into this service's
// sentinels.
func kycRefusal(err error) error {
	switch {
	case errors.Is(err, repositories.ErrKycIntentNotFound):
		return ErrAttentionNotFound
	case errors.Is(err, repositories.ErrKycIntentNotResolvable), errors.Is(err, ramp.ErrIntegrationRequired):
		// Resolved by someone else since it was read (the second click), or a
		// person with no link at the provider to put the customer on.
		return fmt.Errorf("%w: %w", ErrAttentionNotResolvable, err)
	case errors.Is(err, repositories.ErrInvalidKycResolution):
		return fmt.Errorf("%w: %w", ErrInvalidResolution, err)
	}
	return fmt.Errorf("admin ramps: resolve kyc intent: %w", err)
}

// ResolveFinalization closes a finalization the recovery handed over: a payout
// id is bound only when it matches the stored binding (ErrBindingMismatch),
// "not created" is taken only once the quote lapsed (ErrQuoteStillValid) and
// the provider's listing answers without a payout (ErrPayoutExists).
//
// The recovery's Resolve asks the provider and writes the authorization and
// the order together; the act is recorded after that, and only then.
func (s *AttentionActions) ResolveFinalization(
	ctx context.Context, actor auditdto.ActorDTO, id string, in dto.ResolveFinalizationDTO,
) (*dto.AttentionResolutionDTO, error) {
	payoutID := strings.TrimSpace(in.PayoutID)
	if (payoutID == "") == !in.NotCreated {
		return nil, ErrInvalidResolution
	}
	auth, err := s.auths.FindByExternalID(id)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: load finalization: %w", err)
	}
	if auth == nil {
		return nil, ErrAttentionNotFound
	}
	if auth.State != models.FundingAuthUnknown && auth.State != models.FundingAuthNeedsOperator {
		return nil, ErrAttentionNotResolvable
	}
	tx, err := s.txs.FindByID(auth.TransactionID)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: load order of finalization: %w", err)
	}
	if tx == nil {
		return nil, ErrAttentionNotFound
	}

	err = s.finalizations.Resolve(ctx, auth.ExternalID, clientdto.ResolveFinalizationDTO{PayoutID: payoutID, NotCreated: in.NotCreated})
	if err != nil {
		return nil, finalizationRefusal(err)
	}

	outcome := dto.AttentionOutcomeFinalized
	metadata := map[string]any{"authorization_id": auth.ExternalID, "transaction_id": tx.ExternalID}
	if in.NotCreated {
		outcome = dto.AttentionOutcomeNotCreated
	} else {
		metadata["payout_id"] = payoutID
	}
	metadata["outcome"] = outcome
	s.audit.RecordIrreversible(actor, auditdto.EntryDTO{
		Action: audit.ActionRampsFinalizationResolved, SubjectType: audit.SubjectCustomers, SubjectID: tx.CustomerID,
		Provider: auth.Provider, Metadata: metadata,
	})
	resolution := &dto.AttentionResolutionDTO{
		Kind: dto.AttentionKindFinalization, ID: auth.ExternalID, CustomerID: tx.CustomerID,
		TransactionID: tx.ExternalID, Outcome: outcome,
	}
	if !in.NotCreated {
		resolution.PayoutID = payoutID
	}
	return resolution, nil
}

// finalizationRefusal translates what Resolve refused into this service's
// sentinels; ErrBindingMismatch, ErrQuoteStillValid, ErrPayoutExists,
// ErrOrderNotMovable and ErrProviderFailed are aliases and pass as they are.
func finalizationRefusal(err error) error {
	switch {
	case errors.Is(err, clientramps.ErrFinalizationNotFound):
		return ErrAttentionNotFound
	case errors.Is(err, clientramps.ErrFinalizationNotResolvable):
		return fmt.Errorf("%w: %w", ErrAttentionNotResolvable, err)
	case errors.Is(err, clientramps.ErrInvalidFinalizationResolution):
		return fmt.Errorf("%w: %w", ErrInvalidResolution, err)
	}
	return fmt.Errorf("admin ramps: resolve finalization: %w", err)
}

// RecordSupportTicket records the ticket an operator opened with the provider
// about a payout whose money it holds. It does not move the status: the
// webhook or a refresh does, once the provider returns the money. A second
// ticket replaces the first, each with its own audit row, written with it.
func (s *AttentionActions) RecordSupportTicket(
	actor auditdto.ActorDTO, id string, in dto.SupportTicketDTO,
) (*dto.SupportTicketRecordedDTO, error) {
	ref, note := strings.TrimSpace(in.Reference), strings.TrimSpace(in.Note)
	if ref == "" || utf8.RuneCountInString(ref) > SupportTicketReferenceMaxLength ||
		utf8.RuneCountInString(note) > SupportTicketNoteMaxLength || strings.ContainsAny(ref+note, "\r\n") {
		return nil, ErrInvalidResolution
	}
	tx, err := s.txs.FindByExternalID(id)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: load transaction: %w", err)
	}
	if tx == nil {
		return nil, ErrAttentionNotFound
	}
	if !slices.Contains(ticketStatuses, tx.Status) {
		return nil, ErrAttentionNotResolvable
	}

	now := s.now()
	entry := audit.Entry(actor, auditdto.EntryDTO{
		Action: audit.ActionRampsSupportTicketRecorded, SubjectType: audit.SubjectCustomers, SubjectID: tx.CustomerID,
		Provider: tx.Provider, Metadata: map[string]any{"transaction_id": tx.ExternalID, "reference": ref},
	})
	recorded, err := s.txs.RecordSupportTicket(tx.ID, ref, note, now, entry)
	if err != nil {
		return nil, fmt.Errorf("admin ramps: record support ticket: %w", err)
	}
	if !recorded {
		return nil, ErrAttentionNotFound
	}
	return &dto.SupportTicketRecordedDTO{
		TransactionID: tx.ExternalID, CustomerID: tx.CustomerID, Status: tx.Status,
		Reference: ref, Note: note, At: now,
	}, nil
}

// resolvedBy is the reason a verdict carries: the admin who acted.
func resolvedBy(actor auditdto.ActorDTO) string {
	if actor.Admin == nil {
		return "resolved by admin"
	}
	return fmt.Sprintf("resolved by admin %d", actor.Admin.ID)
}
