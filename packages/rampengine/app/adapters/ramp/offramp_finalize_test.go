package ramp

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// fakeFinalizeAdapter exists only to assert at compile time that one struct
// can satisfy the finalize and bound-read ports, which is what the real
// provider package does. Behavioural mocks come from mockery.
type fakeFinalizeAdapter struct{}

func (fakeFinalizeAdapter) FinalizeOfframp(context.Context, CustomerRef, FinalizeRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

func (fakeFinalizeAdapter) FindOfframpByQuote(context.Context, CustomerRef, FindByQuoteRequest) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

func (fakeFinalizeAdapter) GetBoundOrder(context.Context, CustomerRef, string, OrderBinding) (*RampOrder, error) {
	return nil, ErrProviderNotImplemented
}

var (
	_ OfframpFinalizeAdapter = fakeFinalizeAdapter{}
	_ BoundOrderReader       = fakeFinalizeAdapter{}
)

// A provider refusal is a *FinalizeRefusedError, found with errors.As so the
// service can read its code, and it is still ErrProviderFailed for every
// caller that only knows that one.
func TestFinalizeRefusedErrorIsAFailedProviderCall(t *testing.T) {
	refused := &FinalizeRefusedError{Code: "payout_rejected"}

	wrapped := fmt.Errorf("blindpay: POST /payouts/solana: %w", refused)

	var got *FinalizeRefusedError
	assert.True(t, errors.As(wrapped, &got))
	assert.Equal(t, "payout_rejected", got.Code)
	assert.True(t, errors.Is(wrapped, ErrProviderFailed))
}

// The service must tell a definitive refusal apart from an ambiguous failure
// (a plain ErrProviderFailed). An unrelated error is neither.
func TestFinalizeRefusedErrorIsNotEveryProviderFailure(t *testing.T) {
	var got *FinalizeRefusedError
	assert.False(t, errors.As(ErrProviderFailed, &got))
	assert.False(t, errors.As(ErrOutcomeUnknown, &got))
	assert.False(t, errors.As(ErrProviderNotImplemented, &got))
}

// Each new sentinel survives the adapter's wrapping with errors.Is.
func TestNewFinalizeSentinelsSurviveWrapping(t *testing.T) {
	cases := []struct {
		name     string
		sentinel error
	}{
		{"quote already used", ErrQuoteAlreadyUsed},
		{"binding mismatch", ErrBindingMismatch},
		{"invalid idempotency key", ErrInvalidIdempotencyKey},
		{"idempotency key reused", ErrIdempotencyKeyReused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wrapped := fmt.Errorf("blindpay: finalize: %w", tc.sentinel)
			assert.True(t, errors.Is(wrapped, tc.sentinel))
		})
	}
}

// The four new sentinels are distinct: a binding mismatch is never mistaken
// for a reused key, nor a used quote for an invalid key.
func TestNewFinalizeSentinelsAreDistinct(t *testing.T) {
	all := []error{ErrQuoteAlreadyUsed, ErrBindingMismatch, ErrInvalidIdempotencyKey, ErrIdempotencyKeyReused}
	for i, a := range all {
		for j, b := range all {
			if i == j {
				continue
			}
			assert.False(t, errors.Is(a, b), "sentinel %d matches sentinel %d", i, j)
		}
	}
}

func (fakeFinalizeAdapter) PayoutLeg(context.Context, string, string) (string, string, error) {
	return "", "", ErrProviderNotImplemented
}

var _ PayoutLegNamer = fakeFinalizeAdapter{}
