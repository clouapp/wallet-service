package ramp

import (
	"encoding/json"
	"fmt"

	"github.com/macrowallets/waas/packages/rampengine/app/adapters/ramp"
	"github.com/macrowallets/waas/packages/rampengine/app/repositories"
)

// BindingSource reads the binding a bank payout was finalized under, the one
// source every later read of that payout is held to.
type BindingSource interface {
	// PayoutBinding is (nil, nil) when the transaction has no stored binding:
	// no authorization, or one that never reached the intent.
	PayoutBinding(txID uint) (*ramp.OrderBinding, error)
}

type authorizationBindings struct {
	funding repositories.OfframpFundingAuthorizationRepository
}

// NewBindingSource reads the stored binding off the funding authorization.
func NewBindingSource(funding repositories.OfframpFundingAuthorizationRepository) BindingSource {
	return &authorizationBindings{funding: funding}
}

func (s *authorizationBindings) PayoutBinding(txID uint) (*ramp.OrderBinding, error) {
	auth, err := s.funding.FindByTransaction(txID)
	if err != nil {
		return nil, fmt.Errorf("ramp binding: find authorization: %w", err)
	}
	if auth == nil || auth.PayoutBinding == nil || *auth.PayoutBinding == "" {
		return nil, nil
	}
	// No json tags: the keys are the Go field names, written by Finalize from
	// this same struct.
	var binding ramp.OrderBinding
	if err := json.Unmarshal([]byte(*auth.PayoutBinding), &binding); err != nil {
		return nil, fmt.Errorf("ramp binding: decode stored binding: %w", err)
	}
	return &binding, nil
}
