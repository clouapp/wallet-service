package withdrawrecoveryruntime

import (
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/macrowallets/waas/app/models"
	recovery "github.com/macrowallets/waas/app/services/withdrawrecovery"
)

// ApprovedReviewExpiry reads the immutable independent approval, never a caller
// supplied expiry. A review can shorten the proposal's lifetime, not extend it.
func ApprovedReviewExpiry(claim *models.WithdrawalRecoveryAttempt, proposal recovery.Proposal, now time.Time) (time.Time, error) {
	var review recovery.Review
	if claim == nil || digest(proposal) != claim.ProposalHash || json.Unmarshal([]byte(claim.ReviewJSON), &review) != nil || !review.Approved || review.ProposalHash != claim.ProposalHash {
		return time.Time{}, refuse("immutable independent fee review unavailable or changed")
	}
	evidence, err := hex.DecodeString(review.EvidenceHash)
	if err != nil || len(evidence) != 32 || strings.ToLower(review.EvidenceHash) != review.EvidenceHash || strings.TrimSpace(review.ReviewedBy) == "" || strings.TrimSpace(review.ReviewedBy) != review.ReviewedBy || review.ReviewedBy == proposal.PreparedBy || review.ReviewedBy == claim.Operator {
		return time.Time{}, refuse("immutable independent fee review identity differs")
	}
	if proposal.PreparedAt.IsZero() || proposal.PreparedAt.After(now.Add(5*time.Second)) || now.Sub(proposal.PreparedAt) > recovery.MaxReviewAge || !proposal.ExpiresAt.After(now) || proposal.ExpiresAt.After(proposal.PreparedAt.Add(recovery.MaxReviewAge)) || review.ReviewedAt.Before(proposal.PreparedAt) || review.ReviewedAt.After(now.Add(5*time.Second)) || now.Sub(review.ReviewedAt) > recovery.MaxReviewAge || !review.ExpiresAt.After(review.ReviewedAt) || review.ExpiresAt.After(proposal.ExpiresAt) {
		return time.Time{}, refuse("immutable independent fee review bounds differ")
	}
	expiry := proposal.ExpiresAt
	if review.ExpiresAt.Before(expiry) {
		expiry = review.ExpiresAt
	}
	if !expiry.After(now) {
		return time.Time{}, refuse("immutable independent fee review expired")
	}
	return expiry, nil
}
