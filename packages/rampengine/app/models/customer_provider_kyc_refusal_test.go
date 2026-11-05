package models

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func refusedRow(status string) *CustomerProviderKyc {
	row := &CustomerProviderKyc{KycStatus: status, RejectionCategories: "[]"}
	row.RecordSubmissionFailure(KycRejectionDocumentUnreadable, "refused")
	return row
}

func TestSettleDocumentRefusalStandsWhileTheProviderStillAsks(t *testing.T) {
	for _, status := range []string{KycStatusIncomplete, KycStatusRequiresInfo} {
		row := refusedRow(status)
		row.SettleDocumentRefusal()
		require.NotNil(t, row.LastErrorCode, status)
		require.Equal(t, []string{KycRejectionDocumentUnreadable}, row.DecodedRejectionCategories(), status)
	}
}

func TestSettleDocumentRefusalEndsWhenTheProviderMovesOn(t *testing.T) {
	for _, status := range []string{KycStatusUnderReview, KycStatusApproved, KycStatusRejected} {
		row := refusedRow(status)
		row.SettleDocumentRefusal()
		require.Nil(t, row.LastErrorCode, status)
		require.Empty(t, row.DecodedRejectionCategories(), status)
	}
}

func TestSettleDocumentRefusalLeavesAnyOtherFailure(t *testing.T) {
	row := &CustomerProviderKyc{KycStatus: KycStatusApproved, RejectionCategories: "[]"}
	row.RecordSubmissionFailure("provider_unavailable", "down")
	row.SettleDocumentRefusal()
	require.NotNil(t, row.LastErrorCode)
	require.Empty(t, row.DecodedRejectionCategories())
}
