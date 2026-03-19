package mpc

import (
	"context"
	"fmt"
)

// Sign performs a 2-party MPC signing ceremony.
// Not yet implemented — see Task 5.
func (s *TSSService) Sign(ctx context.Context, curve Curve, shareA, shareB []byte, inputs SignInputs) ([]byte, error) {
	return nil, fmt.Errorf("Sign: not yet implemented")
}
