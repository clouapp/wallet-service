package ramps

import (
	"context"
	"errors"
	"github.com/macrowallets/waas/app/models"
	"testing"
)

func TestFundingRefusalBeforeSubmissionCarriesExplicitProof(t *testing.T) {
	_, err := NewService(Deps{}).Fund(context.Background(), &models.Account{}, nil, nil, FundInput{})
	var proof *FundingNotSubmittedError
	if !errors.As(err, &proof) || !errors.Is(err, ErrFundingForbidden) {
		t.Fatalf("refusal lacks no-submission proof: %v", err)
	}
}
