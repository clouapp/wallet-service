package ramps

import (
	"fmt"
	"sort"
	"time"

	"github.com/goravel/framework/support/carbon"

	dto "github.com/macrowallets/waas/packages/rampengine/app/dto/api/admin/ramps"
	"github.com/macrowallets/waas/packages/rampengine/app/models"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
	"github.com/macrowallets/waas/packages/rampengine/app/support/period"
)

// returningOverdueBusinessDays is how long a payout may sit in returning
// before it reaches the queue (weekends do not count, holidays do).
const returningOverdueBusinessDays = 5

// AttentionService builds the queue of what only an operator can move: held
// refunds, payouts returning for too long, finalizations the automation gave
// up on, KYC intents in manual review and bindings the provider contradicted.
//
// Each source is read with its own limit and the rows are merged here, oldest
// first. It reads only: the actions that resolve an item are elsewhere.
type AttentionService struct {
	txs     repositories.TransactionRepository
	auths   repositories.OfframpFundingAuthorizationRepository
	intents repositories.ProviderKycSubmissionIntentRepository
	now     func() time.Time
}

// NewAttentionService wires the queue. A nil clock is time.Now.
func NewAttentionService(
	txs repositories.TransactionRepository,
	auths repositories.OfframpFundingAuthorizationRepository,
	intents repositories.ProviderKycSubmissionIntentRepository,
	now func() time.Time,
) *AttentionService {
	if now == nil {
		now = time.Now
	}
	return &AttentionService{txs: txs, auths: auths, intents: intents, now: now}
}

// Queue returns every item, oldest first, and the count of each kind (every
// kind present, zero included). The items are never nil.
func (s *AttentionService) Queue(in dto.AdminListAttentionDTO) (*dto.AttentionQueueDTO, error) {
	limit := in.Limit
	if limit <= 0 || limit > dto.AttentionPerSourceLimit {
		limit = dto.AttentionPerSourceLimit
	}
	now := s.now()
	items := []dto.AttentionItemDTO{}

	refunds, err := s.txs.ListByStatus(models.TxStatusRefundAttention, limit)
	if err != nil {
		return nil, fmt.Errorf("admin ramps attention: refund attention: %w", err)
	}
	for i := range refunds {
		items = append(items, heldTransaction(dto.AttentionKindRefund, &refunds[i], stdTime(refunds[i].UpdatedAt), now))
	}

	cutoff := period.AddBusinessDays(now, -returningOverdueBusinessDays)
	returning, err := s.txs.ListReturningBefore(cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("admin ramps attention: returning overdue: %w", err)
	}
	for i := range returning {
		items = append(items, heldTransaction(dto.AttentionKindReturningOverdue, &returning[i], stdTime(returning[i].CreatedAt), now))
	}

	marked, err := s.txs.ListWithAttention(limit)
	if err != nil {
		return nil, fmt.Errorf("admin ramps attention: binding divergence: %w", err)
	}
	for i := range marked {
		tx := &marked[i]
		since := stdTime(tx.UpdatedAt)
		if tx.AttentionAt != nil {
			since = *tx.AttentionAt
		}
		items = append(items, dto.AttentionItemDTO{
			Kind: dto.AttentionKindBindingDivergence, ID: tx.ExternalID, CustomerID: tx.CustomerID,
			TransactionID: tx.ExternalID, Provider: tx.Provider, Since: since, AgeSeconds: age(since, now),
			Detail: dto.AttentionDetailDTO{Code: tx.AttentionCode},
		})
	}

	funding, err := s.auths.ListNeedingOperator(limit)
	if err != nil {
		return nil, fmt.Errorf("admin ramps attention: finalization: %w", err)
	}
	for i := range funding {
		a, tx := &funding[i].Authorization, &funding[i].Transaction
		state := a.State
		detail := dto.AttentionDetailDTO{
			State: &state, RecoveryAttempts: &a.RecoveryAttempts, RecoveryNextAttemptAt: a.RecoveryNextAttemptAt,
		}
		// failure_code is stored for the failed state only (a CHECK).
		if a.State == models.FundingAuthFailed {
			detail.FailureCode = a.FailureCode
		}
		updated := stdTime(a.UpdatedAt)
		items = append(items, dto.AttentionItemDTO{
			Kind: dto.AttentionKindFinalization, ID: a.ExternalID, CustomerID: tx.CustomerID,
			TransactionID: tx.ExternalID, Provider: a.Provider, Since: updated, AgeSeconds: age(updated, now),
			Detail: detail,
		})
	}

	intents, err := s.intents.ListManualReview(limit)
	if err != nil {
		return nil, fmt.Errorf("admin ramps attention: kyc intents: %w", err)
	}
	for i := range intents {
		in := &intents[i]
		state := in.State
		updated := stdTime(in.UpdatedAt)
		items = append(items, dto.AttentionItemDTO{
			Kind: dto.AttentionKindKycIntent, ID: in.ExternalID, CustomerID: in.CustomerID,
			Provider: in.Provider, Since: updated, AgeSeconds: age(updated, now),
			Detail: dto.AttentionDetailDTO{State: &state, FailureCode: in.FailureCode},
		})
	}

	sort.SliceStable(items, func(i, j int) bool { return items[i].Since.Before(items[j].Since) })

	counts := make(map[string]int, len(dto.AttentionKinds))
	for _, k := range dto.AttentionKinds {
		counts[k] = 0
	}
	for _, it := range items {
		counts[it.Kind]++
	}
	return &dto.AttentionQueueDTO{Items: items, Counts: counts}, nil
}

func stdTime(t *carbon.DateTime) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.StdTime()
}

func age(since, now time.Time) int64 {
	if now.Before(since) {
		return 0
	}
	return int64(now.Sub(since).Seconds())
}

// heldTransaction is a payout whose money the provider holds: the order, the
// hash of the collection when there is one, and the support ticket once an
// operator recorded it.
func heldTransaction(kind string, tx *models.Transaction, since, now time.Time) dto.AttentionItemDTO {
	detail := dto.AttentionDetailDTO{PayoutID: &tx.GatewayOrderID, CollectionHash: tx.DepositTxSignature}
	if tx.SupportTicketRef != nil {
		ticket := &dto.AttentionTicketDTO{Ref: *tx.SupportTicketRef, At: tx.SupportTicketAt}
		if tx.SupportTicketNote != nil {
			ticket.Note = *tx.SupportTicketNote
		}
		detail.SupportTicket = ticket
	}
	return dto.AttentionItemDTO{
		Kind: kind, ID: tx.ExternalID, CustomerID: tx.CustomerID, TransactionID: tx.ExternalID,
		Provider: tx.Provider, Since: since, AgeSeconds: age(since, now), Detail: detail,
	}
}
